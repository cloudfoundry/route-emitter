package watcher

import (
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudfoundry-incubator/receptor"
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
	receptorClient receptor.Client
	clock          clock.Clock
	table          routing_table.RoutingTable
	emitter        nats_emitter.NATSEmitter
	syncEvents     syncer.Events
	logger         lager.Logger
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
	receptorClient receptor.Client,
	clock clock.Clock,
	table routing_table.RoutingTable,
	emitter nats_emitter.NATSEmitter,
	syncEvents syncer.Events,
	logger lager.Logger,
) *Watcher {
	return &Watcher{
		receptorClient: receptorClient,
		clock:          clock,
		table:          table,
		emitter:        emitter,
		syncEvents:     syncEvents,
		logger:         logger.Session("watcher"),
	}
}

func (watcher *Watcher) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	watcher.logger.Info("starting")

	close(ready)
	watcher.logger.Info("started")
	defer watcher.logger.Info("finished")

	var cachedEvents map[string]receptor.Event

	eventChan := make(chan receptor.Event)
	syncEndChan := make(chan syncEndEvent)

	syncing := false

	var eventSource atomic.Value
	var stopEventSource int32

	startEventSource := func() {
		go func() {
			var err error
			var es receptor.EventSource

			for {
				if atomic.LoadInt32(&stopEventSource) == 1 {
					return
				}

				es, err = watcher.receptorClient.SubscribeToEvents()
				if err != nil {
					watcher.logger.Error("failed-subscribing-to-events", err)
					continue
				}

				eventSource.Store(es)

				var event receptor.Event
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

				cachedEvents = make(map[string]receptor.Event)
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
				err := es.(receptor.EventSource).Close()
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

	var runningActualLRPs []receptor.ActualLRPResponse
	var getActualLRPsErr error
	var desiredLRPs []receptor.DesiredLRPResponse
	var getDesiredLRPsErr error

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()

		logger.Debug("getting-actual-lrps")
		actualLRPResponses, err := watcher.receptorClient.ActualLRPs()
		if err != nil {
			logger.Error("failed-getting-actual-lrps", err)
			getActualLRPsErr = err
			return
		}
		logger.Debug("succeeded-getting-actual-lrps", lager.Data{"num-actual-responses": len(actualLRPResponses)})

		runningActualLRPs = make([]receptor.ActualLRPResponse, 0, len(actualLRPResponses))
		for _, actualLRPResponse := range actualLRPResponses {
			if actualLRPResponse.State == receptor.ActualLRPStateRunning {
				runningActualLRPs = append(runningActualLRPs, actualLRPResponse)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		logger.Debug("getting-desired-lrps")
		desiredLRPResponses, err := watcher.receptorClient.DesiredLRPs()
		if err != nil {
			logger.Error("failed-getting-desired-lrps", err)
			getDesiredLRPsErr = err
			return
		}
		logger.Debug("succeeded-getting-desired-lrps", lager.Data{"num-desired-responses": len(desiredLRPResponses)})

		desiredLRPs = make([]receptor.DesiredLRPResponse, 0, len(desiredLRPResponses))
		for _, desiredLRPResponse := range desiredLRPResponses {
			desiredLRPs = append(desiredLRPs, desiredLRPResponse)
		}
	}()

	wg.Wait()

	if getActualLRPsErr != nil || getDesiredLRPsErr != nil {
		return
	}

	newTable := routing_table.NewTempTable(
		routing_table.RoutesByRoutingKeyFromDesireds(desiredLRPs),
		routing_table.EndpointsByRoutingKeyFromActuals(runningActualLRPs),
	)

	endEvent.table = newTable
	endEvent.callback = func(table routing_table.RoutingTable) {
		after := watcher.clock.Now()
		routeSyncDuration.Send(after.Sub(before))
	}
}

func (watcher *Watcher) completeSync(syncEnd syncEndEvent, cachedEvents map[string]receptor.Event) {
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

func (watcher *Watcher) handleEvent(logger lager.Logger, event receptor.Event) {
	switch event := event.(type) {
	case receptor.DesiredLRPCreatedEvent:
		watcher.handleDesiredCreate(logger, event.DesiredLRPResponse)
	case receptor.DesiredLRPChangedEvent:
		watcher.handleDesiredUpdate(logger, event.Before, event.After)
	case receptor.DesiredLRPRemovedEvent:
		watcher.handleDesiredDelete(logger, event.DesiredLRPResponse)
	case receptor.ActualLRPCreatedEvent:
		watcher.handleActualCreate(logger, event.ActualLRPResponse)
	case receptor.ActualLRPChangedEvent:
		watcher.handleActualUpdate(logger, event.Before, event.After)
	case receptor.ActualLRPRemovedEvent:
		watcher.handleActualDelete(logger, event.ActualLRPResponse)
	default:
		logger.Info("did-not-handle-unrecognizable-event", lager.Data{"event-type": event.EventType()})
	}
}

func (watcher *Watcher) handleDesiredCreate(logger lager.Logger, desiredLRP receptor.DesiredLRPResponse) {
	logger = logger.Session("handle-desired-create", desiredLRPData(desiredLRP))
	logger.Info("starting")
	defer logger.Info("complete")

	watcher.setRoutesForDesired(logger, desiredLRP)
}

func (watcher *Watcher) handleDesiredUpdate(logger lager.Logger, before, after receptor.DesiredLRPResponse) {
	logger = logger.Session("handling-desired-update", lager.Data{
		"before": desiredLRPData(before),
		"after":  desiredLRPData(after),
	})
	logger.Info("starting")
	defer logger.Info("complete")

	afterKeysSet := watcher.setRoutesForDesired(logger, after)

	beforeRoutingKeys := routing_table.RoutingKeysFromDesired(before)
	afterRoutes, _ := cfroutes.CFRoutesFromRoutingInfo(after.Routes)

	afterContainerPorts := set{}
	for _, route := range afterRoutes {
		afterContainerPorts.add(route.Port)
	}

	for _, key := range beforeRoutingKeys {
		if !afterKeysSet.contains(key) || !afterContainerPorts.contains(key.ContainerPort) {
			messagesToEmit := watcher.table.RemoveRoutes(key, after.ModificationTag)
			watcher.emitMessages(logger, messagesToEmit)
		}
	}
}

func (watcher *Watcher) setRoutesForDesired(logger lager.Logger, desiredLRP receptor.DesiredLRPResponse) set {
	routingKeys := routing_table.RoutingKeysFromDesired(desiredLRP)
	routes, _ := cfroutes.CFRoutesFromRoutingInfo(desiredLRP.Routes)
	routingKeySet := set{}

	for _, key := range routingKeys {
		routingKeySet.add(key)
		for _, route := range routes {
			if key.ContainerPort == route.Port {
				messagesToEmit := watcher.table.SetRoutes(key, routing_table.Routes{
					Hostnames: route.Hostnames,
					LogGuid:   desiredLRP.LogGuid,
				})
				watcher.emitMessages(logger, messagesToEmit)
			}
		}
	}

	return routingKeySet
}

func (watcher *Watcher) handleDesiredDelete(logger lager.Logger, desiredLRP receptor.DesiredLRPResponse) {
	logger = logger.Session("handling-desired-delete", desiredLRPData(desiredLRP))
	logger.Info("starting")
	defer logger.Info("complete")

	for _, key := range routing_table.RoutingKeysFromDesired(desiredLRP) {
		messagesToEmit := watcher.table.RemoveRoutes(key, desiredLRP.ModificationTag)

		watcher.emitMessages(logger, messagesToEmit)
	}
}

func (watcher *Watcher) handleActualCreate(logger lager.Logger, actualLRP receptor.ActualLRPResponse) {
	logger = logger.Session("handling-actual-create", actualLRPData(actualLRP))
	logger.Info("starting")
	defer logger.Info("complete")

	if actualLRP.State == receptor.ActualLRPStateRunning {
		watcher.addAndEmit(logger, actualLRP)
	}
}

func (watcher *Watcher) handleActualUpdate(logger lager.Logger, before, after receptor.ActualLRPResponse) {
	logger = logger.Session("handling-actual-update", lager.Data{
		"before": actualLRPData(before),
		"after":  actualLRPData(after),
	})
	logger.Info("starting")
	defer logger.Info("complete")

	switch {
	case after.State == receptor.ActualLRPStateRunning:
		watcher.addAndEmit(logger, after)
	case after.State != receptor.ActualLRPStateRunning && before.State == receptor.ActualLRPStateRunning:
		watcher.removeAndEmit(logger, before)
	}
}

func (watcher *Watcher) handleActualDelete(logger lager.Logger, actualLRP receptor.ActualLRPResponse) {
	logger = logger.Session("handling-actual-delete", actualLRPData(actualLRP))
	logger.Info("starting")
	defer logger.Info("complete")

	if actualLRP.State == receptor.ActualLRPStateRunning {
		watcher.removeAndEmit(logger, actualLRP)
	}
}

func (watcher *Watcher) addAndEmit(logger lager.Logger, actualLRP receptor.ActualLRPResponse) {
	endpoints, err := routing_table.EndpointsFromActual(actualLRP)
	if err != nil {
		logger.Error("failed-to-extract-endpoint-from-actual", err)
		return
	}

	for _, key := range routing_table.RoutingKeysFromActual(actualLRP) {
		for _, endpoint := range endpoints {
			if key.ContainerPort == endpoint.ContainerPort {
				messagesToEmit := watcher.table.AddEndpoint(key, endpoint)
				watcher.emitMessages(logger, messagesToEmit)
			}
		}
	}
}

func (watcher *Watcher) removeAndEmit(logger lager.Logger, actualLRP receptor.ActualLRPResponse) {
	endpoints, err := routing_table.EndpointsFromActual(actualLRP)
	if err != nil {
		logger.Error("failed-to-extract-endpoint-from-actual", err)
		return
	}

	for _, key := range routing_table.RoutingKeysFromActual(actualLRP) {
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

func desiredLRPData(lrp receptor.DesiredLRPResponse) lager.Data {
	return lager.Data{
		"process-guid": lrp.ProcessGuid,
		"routes":       lrp.Routes,
		"ports":        lrp.Ports,
	}
}

func actualLRPData(lrp receptor.ActualLRPResponse) lager.Data {
	return lager.Data{
		"process-guid":  lrp.ProcessGuid,
		"index":         lrp.Index,
		"domain":        lrp.Domain,
		"instance-guid": lrp.InstanceGuid,
		"cell-id":       lrp.CellID,
		"address":       lrp.Address,
		"ports":         lrp.Ports,
		"evacuating":    lrp.Evacuating,
	}
}
