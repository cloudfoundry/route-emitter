package watcher

import (
	"os"

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
	Table    routing_table.RoutingTable
	Callback func(routing_table.RoutingTable)
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

	var eventSource receptor.EventSource
	var cachedEvents map[string]receptor.Event

	eventChan := make(chan receptor.Event)
	errChan := make(chan error, 1)
	syncEndChan := make(chan syncEndEvent)

	syncing := false
	checkEventSource := func() error {
		if eventSource == nil {
			var resubscribeErr error
			eventSource, resubscribeErr = watcher.receptorClient.SubscribeToEvents()
			if resubscribeErr != nil {
				watcher.logger.Error("failed-resubscribing-to-events", resubscribeErr)
				return resubscribeErr
			}

			go func() {
				for {
					event, err := eventSource.Next()
					if err != nil {
						errChan <- err
						return
					}

					if event != nil {
						eventChan <- event
					}
				}
			}()
		}

		return nil
	}

	for {
		select {
		case <-watcher.syncEvents.Sync:
			if syncing == false {
				watcher.logger.Info("sync-begin")
				syncing = true
				if err := checkEventSource(); err != nil {
					return err
				}

				cachedEvents = make(map[string]receptor.Event)
				go watcher.sync(syncEndChan)
			}

		case syncEnd := <-syncEndChan:
			watcher.logger.Info("sync-end")
			watcher.completeSync(syncEnd, cachedEvents)
			cachedEvents = nil
			syncing = false

		case <-watcher.syncEvents.Emit:
			watcher.logger.Info("sync-emit")
			watcher.emit()

		case event := <-eventChan:
			watcher.logger.Info("handling-event", lager.Data{
				"type": event.EventType(),
			})

			if syncing {
				cachedEvents[event.Key()] = event
			} else {
				watcher.handleEvent(event)
			}

		case err := <-errChan:
			watcher.logger.Error("failed-getting-next-event", err)
			eventSource = nil

			if err := checkEventSource(); err != nil {
				return err
			}

		case <-signals:
			watcher.logger.Info("stopping")
			if eventSource != nil {
				err := eventSource.Close()
				if err != nil {
					watcher.logger.Error("failed-closing-event-source", err)
				}
			}
			return nil
		}
	}
}

func (watcher *Watcher) emit() {
	messagesToEmit := watcher.table.MessagesToEmit()

	watcher.logger.Info("emitting-messages", lager.Data{"messages": messagesToEmit})
	err := watcher.emitter.Emit(messagesToEmit)
	if err != nil {
		watcher.logger.Error("failed-to-emit-routes", err)
	}

	routesSynced.Add(messagesToEmit.RouteRegistrationCount())
	routesTotal.Send(watcher.table.RouteCount())
}

func (watcher *Watcher) sync(syncEndChan chan syncEndEvent) {
	endEvent := syncEndEvent{}
	defer func() {
		syncEndChan <- endEvent
	}()

	before := watcher.clock.Now()

	actualLRPResponses, err := watcher.receptorClient.ActualLRPs()
	if err != nil {
		watcher.logger.Error("failed-to-get-actual", err)
		return
	}

	desiredLRPResponses, err := watcher.receptorClient.DesiredLRPs()
	if err != nil {
		watcher.logger.Error("failed-to-get-desired", err)
		return
	}

	runningActualLRPs := make([]receptor.ActualLRPResponse, 0, len(actualLRPResponses))
	for _, actualLRPResponse := range actualLRPResponses {
		if actualLRPResponse.State == receptor.ActualLRPStateRunning {
			runningActualLRPs = append(runningActualLRPs, actualLRPResponse)
		}
	}

	desiredLRPs := make([]receptor.DesiredLRPResponse, 0, len(desiredLRPResponses))
	for _, desiredLRPResponse := range desiredLRPResponses {
		desiredLRPs = append(desiredLRPs, desiredLRPResponse)
	}

	newTable := routing_table.NewTempTable(
		routing_table.RoutesByRoutingKeyFromDesireds(desiredLRPs),
		routing_table.EndpointsByRoutingKeyFromActuals(runningActualLRPs),
	)

	endEvent.Table = newTable
	endEvent.Callback = func(table routing_table.RoutingTable) {
		after := watcher.clock.Now()
		routeSyncDuration.Send(after.Sub(before))
	}
}

func (watcher *Watcher) completeSync(syncEnd syncEndEvent, cachedEvents map[string]receptor.Event) {
	if syncEnd.Table == nil {
		// sync failed, process the events on the current table
		for _, e := range cachedEvents {
			watcher.handleEvent(e)
		}

		return
	}

	emitter := watcher.emitter
	watcher.emitter = nil

	table := watcher.table
	watcher.table = syncEnd.Table

	for _, e := range cachedEvents {
		watcher.handleEvent(e)
	}

	watcher.table = table
	watcher.emitter = emitter

	messages := watcher.table.Swap(syncEnd.Table)
	watcher.emitMessages(messages)

	if syncEnd.Callback != nil {
		syncEnd.Callback(watcher.table)
	}
}

func (watcher *Watcher) handleEvent(event receptor.Event) {
	switch event := event.(type) {
	case receptor.DesiredLRPCreatedEvent:
		watcher.handleDesiredCreate(event.DesiredLRPResponse)
	case receptor.DesiredLRPChangedEvent:
		watcher.handleDesiredUpdate(event.Before, event.After)
	case receptor.DesiredLRPRemovedEvent:
		watcher.handleDesiredDelete(event.DesiredLRPResponse)
	case receptor.ActualLRPCreatedEvent:
		watcher.handleActualCreate(event.ActualLRPResponse)
	case receptor.ActualLRPChangedEvent:
		watcher.handleActualUpdate(event.Before, event.After)
	case receptor.ActualLRPRemovedEvent:
		watcher.handleActualDelete(event.ActualLRPResponse)
	default:
		watcher.logger.Info("did-not-handle-unrecognizable-event", lager.Data{"event-type": event.EventType()})
	}
}

func (watcher *Watcher) handleDesiredCreate(desiredLRP receptor.DesiredLRPResponse) {
	watcher.logger.Info("handling-desired-create", desiredLRPData(desiredLRP))
	defer watcher.logger.Info("done-handling-desired-create")

	watcher.setRoutesForDesired(desiredLRP)
}

func (watcher *Watcher) handleDesiredUpdate(before, after receptor.DesiredLRPResponse) {
	watcher.logger.Info("handling-desired-update", lager.Data{
		"before": desiredLRPData(before),
		"after":  desiredLRPData(after),
	})
	defer watcher.logger.Info("done-handling-desired-update")

	afterKeysSet := watcher.setRoutesForDesired(after)

	beforeRoutingKeys := routing_table.RoutingKeysFromDesired(before)
	afterRoutes, _ := cfroutes.CFRoutesFromRoutingInfo(after.Routes)

	afterContainerPorts := set{}
	for _, route := range afterRoutes {
		afterContainerPorts.add(route.Port)
	}

	for _, key := range beforeRoutingKeys {
		if !afterKeysSet.contains(key) || !afterContainerPorts.contains(key.ContainerPort) {
			messagesToEmit := watcher.table.RemoveRoutes(key, after.ModificationTag)
			watcher.emitMessages(messagesToEmit)
		}
	}
}

func (watcher *Watcher) setRoutesForDesired(desiredLRP receptor.DesiredLRPResponse) set {
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
				watcher.emitMessages(messagesToEmit)
			}
		}
	}

	return routingKeySet
}

func (watcher *Watcher) handleDesiredDelete(desiredLRP receptor.DesiredLRPResponse) {
	watcher.logger.Debug("handling-desired-delete", desiredLRPData(desiredLRP))
	defer watcher.logger.Debug("done-handling-desired-delete")

	for _, key := range routing_table.RoutingKeysFromDesired(desiredLRP) {
		messagesToEmit := watcher.table.RemoveRoutes(key, desiredLRP.ModificationTag)

		watcher.emitMessages(messagesToEmit)
	}
}

func (watcher *Watcher) handleActualCreate(actualLRP receptor.ActualLRPResponse) {
	watcher.logger.Debug("handling-actual-create", actualLRPData(actualLRP))
	defer watcher.logger.Debug("done-handling-actual-create")

	if actualLRP.State == receptor.ActualLRPStateRunning {
		watcher.addAndEmit(actualLRP)
	}
}

func (watcher *Watcher) handleActualUpdate(before, after receptor.ActualLRPResponse) {
	watcher.logger.Debug("handling-actual-update", lager.Data{
		"before": actualLRPData(before),
		"after":  actualLRPData(after),
	})
	defer watcher.logger.Debug("done-handling-actual-update")

	switch {
	case after.State == receptor.ActualLRPStateRunning:
		watcher.addAndEmit(after)
	case after.State != receptor.ActualLRPStateRunning && before.State == receptor.ActualLRPStateRunning:
		watcher.removeAndEmit(before)
	}
}

func (watcher *Watcher) handleActualDelete(actualLRP receptor.ActualLRPResponse) {
	watcher.logger.Debug("handling-actual-delete", actualLRPData(actualLRP))
	defer watcher.logger.Debug("done-handling-actual-delete")

	if actualLRP.State == receptor.ActualLRPStateRunning {
		watcher.removeAndEmit(actualLRP)
	}
}

func (watcher *Watcher) addAndEmit(actualLRP receptor.ActualLRPResponse) {
	endpoints, err := routing_table.EndpointsFromActual(actualLRP)
	if err != nil {
		watcher.logger.Error("failed-to-extract-endpoint-from-actual", err)
		return
	}

	for _, key := range routing_table.RoutingKeysFromActual(actualLRP) {
		for _, endpoint := range endpoints {
			if key.ContainerPort == endpoint.ContainerPort {
				messagesToEmit := watcher.table.AddEndpoint(key, endpoint)
				watcher.emitMessages(messagesToEmit)
			}
		}
	}
}

func (watcher *Watcher) removeAndEmit(actualLRP receptor.ActualLRPResponse) {
	endpoints, err := routing_table.EndpointsFromActual(actualLRP)
	if err != nil {
		watcher.logger.Error("failed-to-extract-endpoint-from-actual", err)
		return
	}

	for _, key := range routing_table.RoutingKeysFromActual(actualLRP) {
		for _, endpoint := range endpoints {
			if key.ContainerPort == endpoint.ContainerPort {
				messagesToEmit := watcher.table.RemoveEndpoint(key, endpoint)
				watcher.emitMessages(messagesToEmit)
			}
		}
	}
}

func (watcher *Watcher) emitMessages(messagesToEmit routing_table.MessagesToEmit) {
	if watcher.emitter != nil {
		watcher.logger.Info("emitting-messages", lager.Data{"messages": messagesToEmit})
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
