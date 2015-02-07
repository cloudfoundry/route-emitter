package watcher

import (
	"os"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/runtime-schema/metric"
	"github.com/pivotal-golang/lager"
)

var (
	routesRegistered   = metric.Counter("RoutesRegistered")
	routesUnregistered = metric.Counter("RoutesUnregistered")
)

type Watcher struct {
	receptorClient receptor.Client
	table          routing_table.RoutingTable
	emitter        nats_emitter.NATSEmitter
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
	logger lager.Logger,
) *Watcher {
	return &Watcher{
		receptorClient: receptorClient,
		table:          table,
		emitter:        emitter,
		logger:         logger.Session("watcher"),
	}
}

func (watcher *Watcher) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	watcher.logger.Info("starting")

	eventSource, err := watcher.receptorClient.SubscribeToEvents()
	if err != nil {
		watcher.logger.Error("failed-subscribing-to-events", err)
		return err
	}

	close(ready)
	watcher.logger.Info("started")
	defer watcher.logger.Info("finished")

	eventChan := make(chan receptor.Event)
	errChan := make(chan error)
	resubscribeErrChan := make(chan error)

	for {
		go func() {
			if eventSource == nil {
				var resubscribeErr error
				eventSource, resubscribeErr = watcher.receptorClient.SubscribeToEvents()
				if resubscribeErr != nil {
					resubscribeErrChan <- resubscribeErr
					return
				}
			}

			event, err := eventSource.Next()

			if err != nil {
				errChan <- err
			} else if event != nil {
				eventChan <- event
			}
		}()

		select {
		case resubscribeErr := <-resubscribeErrChan:
			watcher.logger.Error("failed-resubscribing-to-events", resubscribeErr)
			if eventSource != nil {
				err := eventSource.Close()
				if err != nil {
					watcher.logger.Error("failed-closing-event-source", err)
				}
			}
			return resubscribeErr

		case event := <-eventChan:
			watcher.logger.Info("handling-event", lager.Data{
				"type": event.EventType(),
			})

			watcher.handleEvent(event)

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
			messagesToEmit := watcher.table.RemoveRoutes(key)
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
					URIs:    route.Hostnames,
					LogGuid: desiredLRP.LogGuid,
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
		messagesToEmit := watcher.table.RemoveRoutes(key)

		watcher.emitMessages(messagesToEmit)
	}
}

func (watcher *Watcher) handleActualCreate(actualLRP receptor.ActualLRPResponse) {
	watcher.logger.Debug("handling-actual-create", actualLRPData(actualLRP))
	defer watcher.logger.Debug("done-handling-actual-create")

	if actualLRP.State == receptor.ActualLRPStateRunning {
		watcher.addOrUpdateAndEmit(actualLRP)
	}
}

func (watcher *Watcher) handleActualUpdate(before, after receptor.ActualLRPResponse) {
	watcher.logger.Debug("handling-actual-update", lager.Data{"before": actualLRPData(before), "after": actualLRPData(after)})
	defer watcher.logger.Debug("done-handling-actual-update")

	switch {
	case after.State == receptor.ActualLRPStateRunning:
		watcher.addOrUpdateAndEmit(after)
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

func (watcher *Watcher) addOrUpdateAndEmit(actualLRP receptor.ActualLRPResponse) {
	endpoints, err := routing_table.EndpointsFromActual(actualLRP)
	if err != nil {
		watcher.logger.Error("failed-to-extract-endpoint-from-actual", err)
		return
	}

	for _, key := range routing_table.RoutingKeysFromActual(actualLRP) {
		for _, endpoint := range endpoints {
			if key.ContainerPort == endpoint.ContainerPort {
				messagesToEmit := watcher.table.AddOrUpdateEndpoint(key, endpoint)
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
	watcher.emitter.Emit(messagesToEmit)
	routesRegistered.Add(messagesToEmit.RouteRegistrationCount())
	routesUnregistered.Add(messagesToEmit.RouteUnregistrationCount())
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
	}
}
