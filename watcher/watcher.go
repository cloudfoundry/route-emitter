package watcher

import (
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudfoundry-incubator/bbs"
	"github.com/cloudfoundry-incubator/bbs/events"
	"github.com/cloudfoundry-incubator/bbs/models"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/route-emitter/syncer"
	"github.com/cloudfoundry-incubator/runtime-schema/metric"
	"github.com/pivotal-golang/clock"
	"github.com/pivotal-golang/lager"
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
	logger     lager.Logger
}

type syncEndEvent struct {
	table    routing_table.RoutingTable
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
					return
				}

				es, err = watcher.bbsClient.SubscribeToEvents()
				if err != nil {
					watcher.logger.Error("failed-subscribing-to-events", err)
					continue
				}

				eventSource.Store(es)

				var event models.Event
				for {
					event, err = es.Next()
					if err != nil {
						watcher.logger.Error("failed-getting-next-event", err)
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
				syncing = true

				if !startedEventSource {
					startedEventSource = true
					startEventSource()
				}

				cachedEvents = make(map[string]models.Event)
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
	routesTotal.Send(watcher.table.RouteCount())
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

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()

		logger.Debug("getting-actual-lrps")
		actualLRPGroups, err := watcher.bbsClient.ActualLRPGroups(models.ActualLRPFilter{})
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
	}()

	wg.Add(1)
	go func() {
		var err error
		defer wg.Done()

		logger.Debug("getting-scheduling-infos")
		schedulingInfos, err = watcher.bbsClient.DesiredLRPSchedulingInfos(models.DesiredLRPFilter{})
		if err != nil {
			logger.Error("failed-getting-desired-lrps", err)
			getSchedulingInfosErr = err
			return
		}
		logger.Debug("succeeded-getting-scheduling-infos", lager.Data{"num-desired-responses": len(schedulingInfos)})
	}()

	wg.Wait()

	if getActualLRPsErr != nil || getSchedulingInfosErr != nil {
		return
	}

	newTable := routing_table.NewTempTable(
		routing_table.RoutesByRoutingKeyFromSchedulingInfos(schedulingInfos),
		routing_table.EndpointsByRoutingKeyFromActuals(runningActualLRPs),
	)

	endEvent.table = newTable
	endEvent.callback = func(table routing_table.RoutingTable) {
		after := watcher.clock.Now()
		routeSyncDuration.Send(after.Sub(before))
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

	messages := watcher.table.Swap(syncEnd.table)
	logger.Debug("emitting-messages", lager.Data{
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

func (watcher *Watcher) handleEvent(logger lager.Logger, event models.Event) {
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

	for _, key := range beforeRoutingKeys {
		if !afterKeysSet.contains(key) || !afterContainerPorts.contains(key.ContainerPort) {
			messagesToEmit := watcher.table.RemoveRoutes(key, &after.ModificationTag)
			watcher.emitMessages(logger, messagesToEmit)
		}
	}
}

func (watcher *Watcher) setRoutesForDesired(logger lager.Logger, schedulingInfo *models.DesiredLRPSchedulingInfo) set {
	routingKeys := routing_table.RoutingKeysFromSchedulingInfo(schedulingInfo)
	routes, _ := cfroutes.CFRoutesFromRoutingInfo(schedulingInfo.Routes)
	routingKeySet := set{}

	for _, key := range routingKeys {
		routingKeySet.add(key)
		for _, route := range routes {
			if key.ContainerPort == route.Port {
				messagesToEmit := watcher.table.SetRoutes(key, routing_table.Routes{
					Hostnames:       route.Hostnames,
					LogGuid:         schedulingInfo.LogGuid,
					RouteServiceUrl: route.RouteServiceUrl,
				})
				watcher.emitMessages(logger, messagesToEmit)
			}
		}
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

func (watcher *Watcher) addAndEmit(logger lager.Logger, actualLRPInfo *routing_table.ActualLRPRoutingInfo) {
	endpoints, err := routing_table.EndpointsFromActual(actualLRPInfo)
	if err != nil {
		logger.Error("failed-to-extract-endpoint-from-actual", err)
		return
	}

	for _, key := range routing_table.RoutingKeysFromActual(actualLRPInfo.ActualLRP) {
		for _, endpoint := range endpoints {
			if key.ContainerPort == endpoint.ContainerPort {
				messagesToEmit := watcher.table.AddEndpoint(key, endpoint)
				watcher.emitMessages(logger, messagesToEmit)
			}
		}
	}
}

func (watcher *Watcher) removeAndEmit(logger lager.Logger, actualLRPInfo *routing_table.ActualLRPRoutingInfo) {
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
		logger.Debug("emitting-messages", lager.Data{"messages": messagesToEmit})
		watcher.emitter.Emit(messagesToEmit)
		routesRegistered.Add(messagesToEmit.RouteRegistrationCount())
		routesUnregistered.Add(messagesToEmit.RouteUnregistrationCount())
	}
}

func desiredLRPData(schedulingInfo *models.DesiredLRPSchedulingInfo) lager.Data {
	return lager.Data{
		"process-guid": schedulingInfo.ProcessGuid,
		"routes":       schedulingInfo.Routes,
	}
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
