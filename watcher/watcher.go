package watcher

import (
	"os"
	"sync"
	"sync/atomic"
	"time"

	"code.cloudfoundry.org/bbs"
	"code.cloudfoundry.org/bbs/events"
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/nats_emitter"
	"code.cloudfoundry.org/route-emitter/routing_table"
	"code.cloudfoundry.org/route-emitter/syncer"
	"code.cloudfoundry.org/routing-info/cfroutes"
	"code.cloudfoundry.org/runtimeschema/metric"
)

var (
	routesTotal  = metric.Metric("RoutesTotal")
	routesSynced = metric.Counter("RoutesSynced")

	routeSyncDuration = metric.Duration("RouteEmitterSyncDuration")

	routesRegistered   = metric.Counter("RoutesRegistered")
	routesUnregistered = metric.Counter("RoutesUnregistered")
)

type Watcher struct {
	bbsClient  bbs.Client
	clock      clock.Clock
	table      routing_table.RoutingTable
	emitter    nats_emitter.NATSEmitter
	syncEvents syncer.Events
	cellID     string
	logger     lager.Logger
}

type syncEndEvent struct {
	table    routing_table.RoutingTable
	domains  models.DomainSet
	callback func(routing_table.RoutingTable)

	logger lager.Logger
}

type set map[interface{}]struct{}

func (set set) contains(value interface{}) bool {
	_, found := set[value]
	return found
}

func (set set) add(value interface{}) {
	set[value] = struct{}{}
}

func NewWatcher(
	cellID string,
	bbsClient bbs.Client,
	clock clock.Clock,
	table routing_table.RoutingTable,
	emitter nats_emitter.NATSEmitter,
	syncEvents syncer.Events,
	logger lager.Logger,
) *Watcher {
	return &Watcher{
		bbsClient:  bbsClient,
		clock:      clock,
		table:      table,
		emitter:    emitter,
		syncEvents: syncEvents,
		cellID:     cellID,
		logger:     logger.Session("watcher"),
	}
}

func (watcher *Watcher) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	watcher.logger.Info("starting")

	close(ready)
	watcher.logger.Info("started")
	defer watcher.logger.Info("finished")

	var cachedEvents map[string]models.Event

	eventChan := make(chan models.Event)
	syncEndChan := make(chan syncEndEvent)

	syncing := false

	var eventSource atomic.Value
	var stopEventSource int32

	startEventSource := func() {
		go func() {
			var err error
			var es events.EventSource

			for {
				if atomic.LoadInt32(&stopEventSource) == 1 {
					watcher.logger.Info("stop-event-source-received")
					return
				}

				es, err = watcher.bbsClient.SubscribeToEvents(watcher.logger)
				if err != nil {
					watcher.logger.Error("failed-subscribing-to-events", err)
					continue
				}

				watcher.logger.Info("succeeded-subscribing-to-events")
				eventSource.Store(es)

				var event models.Event
				for {
					event, err = es.Next()
					if err != nil {
						watcher.logger.Error("failed-getting-next-event", err)
						watcher.logger.Debug("closing-event-source-on-error")
						closeErr := es.Close()
						if closeErr != nil {
							watcher.logger.Error("failed-closing-event-source", closeErr)
						}
						// wait a bit before retrying
						time.Sleep(time.Second)
						break
					}

					if event != nil {
						eventChan <- event
					}
				}
			}
		}()
	}

	startedEventSource := false
	for {
		select {
		case <-watcher.syncEvents.Sync:
			if syncing == false {
				logger := watcher.logger.Session("sync")
				logger.Info("starting")

				cachedEvents = make(map[string]models.Event)
				syncing = true

				if !startedEventSource {
					startedEventSource = true
					startEventSource()
				}

				go watcher.sync(logger, syncEndChan)
			}

		case syncEnd := <-syncEndChan:
			watcher.completeSync(syncEnd, cachedEvents)
			cachedEvents = nil
			syncing = false
			syncEnd.logger.Info("complete")

		case <-watcher.syncEvents.Emit:
			logger := watcher.logger.Session("emit")
			watcher.emit(logger)

		case event := <-eventChan:
			if syncing {
				watcher.logger.Info("caching-event", lager.Data{
					"type": event.EventType(),
				})

				cachedEvents[event.Key()] = event
			} else {
				watcher.handleEvent(watcher.logger, event)
			}

		case <-signals:
			watcher.logger.Info("stopping")
			atomic.StoreInt32(&stopEventSource, 1)
			if es := eventSource.Load(); es != nil {
				err := es.(events.EventSource).Close()
				if err != nil {
					watcher.logger.Error("failed-closing-event-source", err)
				}
			}
			return nil
		}
	}
}

func (watcher *Watcher) emit(logger lager.Logger) {
	messagesToEmit := watcher.table.MessagesToEmit()

	logger.Debug("emitting-messages", lager.Data{"messages": messagesToEmit})
	err := watcher.emitter.Emit(messagesToEmit)
	if err != nil {
		logger.Error("failed-to-emit-routes", err)
	}

	routesSynced.Add(messagesToEmit.RouteRegistrationCount())
	err = routesTotal.Send(watcher.table.RouteCount())
	if err != nil {
		logger.Error("failed-to-send-routes-total-metric", err)
	}
}

func (watcher *Watcher) sync(logger lager.Logger, syncEndChan chan syncEndEvent) {
	endEvent := syncEndEvent{logger: logger}
	defer func() {
		syncEndChan <- endEvent
	}()

	before := watcher.clock.Now()

	var runningActualLRPs []*routing_table.ActualLRPRoutingInfo
	var getActualLRPsErr error
	var schedulingInfos []*models.DesiredLRPSchedulingInfo
	var getSchedulingInfosErr error
	var domains models.DomainSet
	var getDomainErr error

	wg := sync.WaitGroup{}

	getSchedulingInfos := func(guids []string) {
		logger.Debug("getting-scheduling-infos")
		var err error
		schedulingInfos, err = watcher.bbsClient.DesiredLRPSchedulingInfos(logger, models.DesiredLRPFilter{
			ProcessGuids: guids,
		})
		if err != nil {
			logger.Error("failed-getting-desired-lrps", err)
			getSchedulingInfosErr = err
			return
		}
		logger.Debug("succeeded-getting-scheduling-infos", lager.Data{"num-desired-responses": len(schedulingInfos)})
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		logger.Debug("getting-actual-lrps")
		actualLRPGroups, err := watcher.bbsClient.ActualLRPGroups(logger, models.ActualLRPFilter{CellID: watcher.cellID})
		if err != nil {
			logger.Error("failed-getting-actual-lrps", err)
			getActualLRPsErr = err
			return
		}
		logger.Debug("succeeded-getting-actual-lrps", lager.Data{"num-actual-responses": len(actualLRPGroups)})

		runningActualLRPs = make([]*routing_table.ActualLRPRoutingInfo, 0, len(actualLRPGroups))
		for _, actualLRPGroup := range actualLRPGroups {
			actualLRP, evacuating := actualLRPGroup.Resolve()
			if actualLRP.State == models.ActualLRPStateRunning {
				runningActualLRPs = append(runningActualLRPs, &routing_table.ActualLRPRoutingInfo{
					ActualLRP:  actualLRP,
					Evacuating: evacuating,
				})
			}
		}

		if watcher.cellID != "" {
			guids := make([]string, 0, len(runningActualLRPs))
			// filter the desired lrp scheduling info by process guids
			for _, actualLRP := range actualLRPGroups {
				lrp, _ := actualLRP.Resolve()
				guids = append(guids, lrp.ProcessGuid)
			}
			getSchedulingInfos(guids)
		}
	}()

	if watcher.cellID == "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			getSchedulingInfos(nil)
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		logger.Debug("getting-domains")
		domainArray, err := watcher.bbsClient.Domains(logger)
		if err != nil {
			logger.Error("failed-getting-domains", err)
			getDomainErr = err
			return
		}
		domains = models.NewDomainSet(domainArray)
		logger.Debug("succeeded-getting-domains", lager.Data{"num-domains": len(domains)})
	}()

	wg.Wait()

	if getActualLRPsErr != nil || getSchedulingInfosErr != nil || getDomainErr != nil {
		return
	}

	schedInfoMap := make(map[string]*models.DesiredLRPSchedulingInfo)
	for _, schedInfo := range schedulingInfos {
		schedInfoMap[schedInfo.ProcessGuid] = schedInfo
	}

	newTable := routing_table.NewTempTable(
		routing_table.RoutesByRoutingKeyFromSchedulingInfos(schedulingInfos),
		routing_table.EndpointsByRoutingKeyFromActuals(runningActualLRPs, schedInfoMap),
	)

	endEvent.table = newTable
	endEvent.domains = domains
	endEvent.callback = func(table routing_table.RoutingTable) {
		after := watcher.clock.Now()
		err := routeSyncDuration.Send(after.Sub(before))
		if err != nil {
			logger.Error("failed-to-send-route-sync-duration-metric", err)
		}
	}
}

func (watcher *Watcher) completeSync(syncEnd syncEndEvent, cachedEvents map[string]models.Event) {
	logger := syncEnd.logger

	if syncEnd.table == nil {
		// sync failed, process the events on the current table
		logger.Debug("handling-events-from-failed-sync")
		for _, e := range cachedEvents {
			watcher.handleEvent(logger, e)
		}
		logger.Debug("done-handling-events-from-failed-sync")

		return
	}

	emitter := watcher.emitter
	watcher.emitter = nil

	table := watcher.table
	watcher.table = syncEnd.table

	logger.Debug("handling-cached-events")
	for _, e := range cachedEvents {
		watcher.handleEvent(logger, e)
	}
	logger.Debug("done-handling-cached-events")

	watcher.table = table
	watcher.emitter = emitter

	messages := watcher.table.Swap(syncEnd.table, syncEnd.domains)
	logger.Debug("start-emitting-messages", lager.Data{
		"num-registration-messages":   len(messages.RegistrationMessages),
		"num-unregistration-messages": len(messages.UnregistrationMessages),
	})
	watcher.emitMessages(logger, messages)
	logger.Debug("done-emitting-messages", lager.Data{
		"num-registration-messages":   len(messages.RegistrationMessages),
		"num-unregistration-messages": len(messages.UnregistrationMessages),
	})

	if syncEnd.callback != nil {
		syncEnd.callback(watcher.table)
	}
}

// returns true if the event is relevant to the local cell, e.g. an actual lrp
// started or stopped on the local cell
func (watcher *Watcher) eventCellIDMatches(logger lager.Logger, event models.Event) bool {
	if watcher.cellID == "" {
		return true
	}

	switch event := event.(type) {
	case *models.DesiredLRPCreatedEvent:
		return true
	case *models.DesiredLRPChangedEvent:
		return true
	case *models.DesiredLRPRemovedEvent:
		return true
	case *models.ActualLRPCreatedEvent:
		lrp, _ := event.ActualLrpGroup.Resolve()
		return lrp.ActualLRPInstanceKey.CellId == watcher.cellID
	case *models.ActualLRPChangedEvent:
		beforeLRP, _ := event.Before.Resolve()
		afterLRP, _ := event.After.Resolve()
		if beforeLRP.State == models.ActualLRPStateRunning {
			return beforeLRP.ActualLRPInstanceKey.CellId == watcher.cellID
		} else if afterLRP.State == models.ActualLRPStateRunning {
			return afterLRP.ActualLRPInstanceKey.CellId == watcher.cellID
		}
		// this shouldn't matter if we pass it through or not, since the event is
		// a no-op from the route-emitter point of view
		return true
	case *models.ActualLRPRemovedEvent:
		lrp, _ := event.ActualLrpGroup.Resolve()
		return lrp.ActualLRPInstanceKey.CellId == watcher.cellID
	default:
		return false
	}
}

func (watcher *Watcher) handleEvent(logger lager.Logger, event models.Event) {
	if !watcher.eventCellIDMatches(logger, event) {
		logSkippedEvent(logger, event)
		return
	}

	switch event := event.(type) {
	case *models.DesiredLRPCreatedEvent:
		schedulingInfo := event.DesiredLrp.DesiredLRPSchedulingInfo()
		watcher.handleDesiredCreate(logger, &schedulingInfo)
	case *models.DesiredLRPChangedEvent:
		before := event.Before.DesiredLRPSchedulingInfo()
		after := event.After.DesiredLRPSchedulingInfo()
		watcher.handleDesiredUpdate(logger, &before, &after)
	case *models.DesiredLRPRemovedEvent:
		schedulingInfo := event.DesiredLrp.DesiredLRPSchedulingInfo()
		watcher.handleDesiredDelete(logger, &schedulingInfo)
	case *models.ActualLRPCreatedEvent:
		watcher.handleActualCreate(logger, routing_table.NewActualLRPRoutingInfo(event.ActualLrpGroup))
	case *models.ActualLRPChangedEvent:
		watcher.handleActualUpdate(logger,
			routing_table.NewActualLRPRoutingInfo(event.Before),
			routing_table.NewActualLRPRoutingInfo(event.After),
		)
	case *models.ActualLRPRemovedEvent:
		watcher.handleActualDelete(logger, routing_table.NewActualLRPRoutingInfo(event.ActualLrpGroup))
	default:
		logger.Info("did-not-handle-unrecognizable-event", lager.Data{"event-type": event.EventType()})
	}
}

func (watcher *Watcher) handleDesiredCreate(logger lager.Logger, schedulingInfo *models.DesiredLRPSchedulingInfo) {
	logger = logger.Session("handle-desired-create", desiredLRPData(schedulingInfo))
	logger.Info("starting")
	defer logger.Info("complete")

	watcher.setRoutesForDesired(logger, schedulingInfo)
}

func (watcher *Watcher) handleDesiredUpdate(logger lager.Logger, before, after *models.DesiredLRPSchedulingInfo) {
	logger = logger.Session("handling-desired-update", lager.Data{
		"before": desiredLRPData(before),
		"after":  desiredLRPData(after),
	})
	logger.Info("starting")
	defer logger.Info("complete")

	afterKeysSet := watcher.setRoutesForDesired(logger, after)

	beforeRoutingKeys := routing_table.RoutingKeysFromSchedulingInfo(before)
	afterRoutes, _ := cfroutes.CFRoutesFromRoutingInfo(after.Routes)

	afterContainerPorts := set{}
	for _, route := range afterRoutes {
		afterContainerPorts.add(route.Port)
	}

	requestedInstances := after.Instances - before.Instances

	for _, key := range beforeRoutingKeys {
		if !afterKeysSet.contains(key) || !afterContainerPorts.contains(key.ContainerPort) {
			messagesToEmit := watcher.table.RemoveRoutes(key, &after.ModificationTag)
			watcher.emitMessages(logger, messagesToEmit)
		}

		// in case of scale down, remove endpoints
		if requestedInstances < 0 {
			logger.Info("removing-endpoints", lager.Data{"removal_count": -1 * requestedInstances, "routing_key": key})

			for index := before.Instances - 1; index >= after.Instances; index-- {
				endpoints := watcher.table.EndpointsForIndex(key, index)

				for i, _ := range endpoints {
					messagesToEmit := watcher.table.RemoveEndpoint(key, endpoints[i])
					watcher.emitMessages(logger, messagesToEmit)
				}
			}
		}
	}
}

func (watcher *Watcher) setRoutesForDesired(logger lager.Logger, schedulingInfo *models.DesiredLRPSchedulingInfo) set {
	routes, _ := cfroutes.CFRoutesFromRoutingInfo(schedulingInfo.Routes)
	routingKeySet := set{}

	routeEntries := make(map[routing_table.RoutingKey][]routing_table.Route)
	for _, route := range routes {
		key := routing_table.RoutingKey{ProcessGuid: schedulingInfo.ProcessGuid, ContainerPort: route.Port}
		routingKeySet.add(key)

		routes := []routing_table.Route{}
		for _, hostname := range route.Hostnames {
			routes = append(routes, routing_table.Route{
				Hostname:        hostname,
				LogGuid:         schedulingInfo.LogGuid,
				RouteServiceUrl: route.RouteServiceUrl,
			})
		}
		routeEntries[key] = append(routeEntries[key], routes...)
	}
	for key := range routeEntries {
		messagesToEmit := watcher.table.SetRoutes(key, routeEntries[key], nil)
		watcher.emitMessages(logger, messagesToEmit)
	}

	return routingKeySet
}

func (watcher *Watcher) handleDesiredDelete(logger lager.Logger, schedulingInfo *models.DesiredLRPSchedulingInfo) {
	logger = logger.Session("handling-desired-delete", desiredLRPData(schedulingInfo))
	logger.Info("starting")
	defer logger.Info("complete")

	for _, key := range routing_table.RoutingKeysFromSchedulingInfo(schedulingInfo) {
		messagesToEmit := watcher.table.RemoveRoutes(key, &schedulingInfo.ModificationTag)

		watcher.emitMessages(logger, messagesToEmit)
	}
}

func (watcher *Watcher) handleActualCreate(logger lager.Logger, actualLRPInfo *routing_table.ActualLRPRoutingInfo) {
	logger = logger.Session("handling-actual-create", actualLRPData(actualLRPInfo))
	logger.Info("starting")
	defer logger.Info("complete")

	if actualLRPInfo.ActualLRP.State == models.ActualLRPStateRunning {
		watcher.addAndEmit(logger, actualLRPInfo)
	}
}

func (watcher *Watcher) handleActualUpdate(logger lager.Logger, before, after *routing_table.ActualLRPRoutingInfo) {
	logger = logger.Session("handling-actual-update", lager.Data{
		"before": actualLRPData(before),
		"after":  actualLRPData(after),
	})
	logger.Info("starting")
	defer logger.Info("complete")

	switch {
	case after.ActualLRP.State == models.ActualLRPStateRunning:
		watcher.addAndEmit(logger, after)
		watcher.removeAnyEvacuatingAndEmit(logger, after)
	case after.ActualLRP.State != models.ActualLRPStateRunning && before.ActualLRP.State == models.ActualLRPStateRunning:
		watcher.removeAndEmit(logger, before)
	}
}

func (watcher *Watcher) handleActualDelete(logger lager.Logger, actualLRPInfo *routing_table.ActualLRPRoutingInfo) {
	logger = logger.Session("handling-actual-delete", actualLRPData(actualLRPInfo))
	logger.Info("starting")
	defer logger.Info("complete")

	if actualLRPInfo.ActualLRP.State == models.ActualLRPStateRunning {
		watcher.removeAndEmit(logger, actualLRPInfo)
	}
}

func (watcher *Watcher) removeAnyEvacuatingAndEmit(logger lager.Logger, actualLRPInfo *routing_table.ActualLRPRoutingInfo) {
	logger.Info("watcher-remove-any-evacuating-and-emit", lager.Data{"net_info": actualLRPInfo.ActualLRP.ActualLRPNetInfo})
	endpoints, err := routing_table.EndpointsFromActual(actualLRPInfo)
	if err != nil {
		logger.Error("failed-to-extract-endpoint-from-actual", err)
		return
	}

	for _, endpoint := range endpoints {
		key := routing_table.RoutingKey{ProcessGuid: actualLRPInfo.ActualLRP.ProcessGuid, ContainerPort: uint32(endpoint.ContainerPort)}
		currentEndpoints := watcher.table.EndpointsForIndex(key, actualLRPInfo.ActualLRP.Index)
		for _, e := range currentEndpoints {
			if e.Evacuating && e != endpoint {
				messagesToEmit := watcher.table.RemoveEndpoint(key, e)
				watcher.emitMessages(logger, messagesToEmit)
			}
		}
	}
}

func (watcher *Watcher) addAndEmit(logger lager.Logger, actualLRPInfo *routing_table.ActualLRPRoutingInfo) {
	logger.Info("watcher-add-and-emit", lager.Data{"net_info": actualLRPInfo.ActualLRP.ActualLRPNetInfo})
	endpoints, err := routing_table.EndpointsFromActual(actualLRPInfo)
	if err != nil {
		logger.Error("failed-to-extract-endpoint-from-actual", err)
		return
	}

	for _, endpoint := range endpoints {
		key := routing_table.RoutingKey{ProcessGuid: actualLRPInfo.ActualLRP.ProcessGuid, ContainerPort: uint32(endpoint.ContainerPort)}
		messagesToEmit := watcher.table.AddEndpoint(key, endpoint)
		watcher.emitMessages(logger, messagesToEmit)
	}
}

func (watcher *Watcher) removeAndEmit(logger lager.Logger, actualLRPInfo *routing_table.ActualLRPRoutingInfo) {
	logger.Info("watcher-remove-and-emit", lager.Data{"net_info": actualLRPInfo.ActualLRP.ActualLRPNetInfo})
	endpoints, err := routing_table.EndpointsFromActual(actualLRPInfo)
	if err != nil {
		logger.Error("failed-to-extract-endpoint-from-actual", err)
		return
	}

	for _, key := range routing_table.RoutingKeysFromActual(actualLRPInfo.ActualLRP) {
		for _, endpoint := range endpoints {
			if key.ContainerPort == endpoint.ContainerPort {
				messagesToEmit := watcher.table.RemoveEndpoint(key, endpoint)
				watcher.emitMessages(logger, messagesToEmit)
			}
		}
	}
}

func (watcher *Watcher) emitMessages(logger lager.Logger, messagesToEmit routing_table.MessagesToEmit) {
	if watcher.emitter != nil {
		logger.Debug("emit-messages", lager.Data{"messages": messagesToEmit})
		watcher.emitter.Emit(messagesToEmit)
		routesRegistered.Add(messagesToEmit.RouteRegistrationCount())
		routesUnregistered.Add(messagesToEmit.RouteUnregistrationCount())
	}
}

func desiredLRPData(schedulingInfo *models.DesiredLRPSchedulingInfo) lager.Data {
	logRoutes := make(models.Routes)
	logRoutes[cfroutes.CF_ROUTER] = schedulingInfo.Routes[cfroutes.CF_ROUTER]

	return lager.Data{
		"process-guid": schedulingInfo.ProcessGuid,
		"routes":       logRoutes,
		"instances":    schedulingInfo.GetInstances(),
	}
}

func logSkippedEvent(logger lager.Logger, event models.Event) {
	data := lager.Data{"event-type": event.EventType()}
	switch e := event.(type) {
	case *models.ActualLRPCreatedEvent:
		data["lrp"] = actualLRPData(routing_table.NewActualLRPRoutingInfo(e.ActualLrpGroup))
	case *models.ActualLRPRemovedEvent:
		data["lrp"] = actualLRPData(routing_table.NewActualLRPRoutingInfo(e.ActualLrpGroup))
	case *models.ActualLRPChangedEvent:
		data["before"] = actualLRPData(routing_table.NewActualLRPRoutingInfo(e.Before))
		data["after"] = actualLRPData(routing_table.NewActualLRPRoutingInfo(e.After))
	}
	logger.Info("skipping-event", data)
}

func actualLRPData(lrpRoutingInfo *routing_table.ActualLRPRoutingInfo) lager.Data {
	lrp := lrpRoutingInfo.ActualLRP
	return lager.Data{
		"process-guid":  lrp.ProcessGuid,
		"index":         lrp.Index,
		"domain":        lrp.Domain,
		"instance-guid": lrp.InstanceGuid,
		"cell-id":       lrp.ActualLRPInstanceKey.CellId,
		"address":       lrp.Address,
		"evacuating":    lrpRoutingInfo.Evacuating,
	}
}
