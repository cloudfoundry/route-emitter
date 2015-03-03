package watcher

import (
	"os"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/route-emitter/syncer"
	"github.com/cloudfoundry-incubator/runtime-schema/metric"
	"github.com/pivotal-golang/lager"
)

var (
	routesTotal  = metric.Metric("RoutesTotal")
	routesSynced = metric.Counter("RoutesSynced")

	routesRegistered   = metric.Counter("RoutesRegistered")
	routesUnregistered = metric.Counter("RoutesUnregistered")
)

type Watcher struct {
	receptorClient receptor.Client
	table          routing_table.RoutingTable
	emitter        nats_emitter.NATSEmitter
	syncEvents     syncer.SyncEvents
	logger         lager.Logger
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
	table routing_table.RoutingTable,
	emitter nats_emitter.NATSEmitter,
	syncEvents syncer.SyncEvents,
	logger lager.Logger,
) *Watcher {
	return &Watcher{
		receptorClient: receptorClient,
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
	var syncing bool
	eventChan := make(chan receptor.Event)
	errChan := make(chan error, 1)

	for {
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

		select {
		case syncBegin := <-watcher.syncEvents.Begin:
			syncing = true
			cachedEvents = make(map[string]receptor.Event)
			close(syncBegin.Ack)

		case syncEnd := <-watcher.syncEvents.End:
			watcher.completeSync(syncEnd, cachedEvents)
			cachedEvents = nil
			syncing = false

		case <-watcher.syncEvents.Emit:
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

func (watcher *Watcher) completeSync(syncEnd syncer.SyncEnd, cachedEvents map[string]receptor.Event) {
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
