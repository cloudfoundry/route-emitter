package watcher

import (
	"os"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/receptor/serialization"
	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/runtime-schema/metric"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/pivotal-golang/lager"
)

var (
	routesRegistered   = metric.Counter("RoutesRegistered")
	routesUnregistered = metric.Counter("RoutesUnregistered")
)

type Watcher struct {
	receptorClient receptor.Client
	table          routing_table.RoutingTable
	emitter        nats_emitter.NATSEmitterInterface
	logger         lager.Logger
}

func NewWatcher(
	receptorClient receptor.Client,
	table routing_table.RoutingTable,
	emitter nats_emitter.NATSEmitterInterface,
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

	return nil
}

func (watcher *Watcher) handleEvent(event receptor.Event) {
	switch event := event.(type) {
	case receptor.DesiredLRPCreatedEvent:
		watcher.handleDesiredCreateOrUpdate(serialization.DesiredLRPFromResponse(event.DesiredLRPResponse))
	case receptor.DesiredLRPChangedEvent:
		watcher.handleDesiredCreateOrUpdate(serialization.DesiredLRPFromResponse(event.After))
	case receptor.DesiredLRPRemovedEvent:
		watcher.handleDesiredDelete(serialization.DesiredLRPFromResponse(event.DesiredLRPResponse))
	case receptor.ActualLRPCreatedEvent:
		watcher.handleActualCreate(serialization.ActualLRPFromResponse(event.ActualLRPResponse))
	case receptor.ActualLRPChangedEvent:
		watcher.handleActualUpdate(serialization.ActualLRPFromResponse(event.Before), serialization.ActualLRPFromResponse(event.After))
	case receptor.ActualLRPRemovedEvent:
		watcher.handleActualDelete(serialization.ActualLRPFromResponse(event.ActualLRPResponse))
	default:
		watcher.logger.Info("did-not-handle-unrecognizable-event", lager.Data{"event-type": event.EventType()})
	}
}

func (watcher *Watcher) handleDesiredCreateOrUpdate(desiredLRP models.DesiredLRP) {
	watcher.logger.Debug("handling-desired-create-or-update", desiredLRPData(desiredLRP))
	defer watcher.logger.Debug("done-handling-desired-create-or-update")

	messagesToEmit := watcher.table.SetRoutes(desiredLRP.ProcessGuid, routing_table.Routes{
		URIs:    desiredLRP.Routes,
		LogGuid: desiredLRP.LogGuid,
	})

	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}

func (watcher *Watcher) handleDesiredDelete(desiredLRP models.DesiredLRP) {
	watcher.logger.Debug("handling-desired-delete", desiredLRPData(desiredLRP))
	defer watcher.logger.Debug("done-handling-desired-delete")

	messagesToEmit := watcher.table.RemoveRoutes(desiredLRP.ProcessGuid)

	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}

func (watcher *Watcher) handleActualCreate(actualLRP models.ActualLRP) {
	watcher.logger.Debug("handling-actual-create", actualLRPData(actualLRP))
	defer watcher.logger.Debug("done-handling-actual-create")

	if actualLRP.State == models.ActualLRPStateRunning {
		watcher.addOrUpdateAndEmit(actualLRP)
	} else {
		watcher.removeAndEmit(actualLRP)
	}
}

func (watcher *Watcher) handleActualUpdate(before, after models.ActualLRP) {
	watcher.logger.Debug("handling-actual-update", lager.Data{"before": actualLRPData(before), "after": actualLRPData(after)})
	defer watcher.logger.Debug("done-handling-actual-update")

	switch {
	case after.State == models.ActualLRPStateRunning:
		watcher.addOrUpdateAndEmit(after)
	case after.State != models.ActualLRPStateRunning && before.State == models.ActualLRPStateRunning:
		watcher.removeAndEmit(before)
	}
}

func (watcher *Watcher) handleActualDelete(actualLRP models.ActualLRP) {
	watcher.logger.Debug("handling-actual-delete", actualLRPData(actualLRP))
	defer watcher.logger.Debug("done-handling-actual-delete")

	watcher.removeAndEmit(actualLRP)
}

func (watcher *Watcher) addOrUpdateAndEmit(actualLRP models.ActualLRP) {
	container, err := routing_table.ContainerFromActual(actualLRP)
	if err != nil {
		watcher.logger.Error("failed-to-extract-container-from-actual", err)
		return
	}

	messagesToEmit := watcher.table.AddOrUpdateContainer(actualLRP.ProcessGuid, container)
	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}

func (watcher *Watcher) removeAndEmit(actualLRP models.ActualLRP) {
	container, err := routing_table.ContainerFromActual(actualLRP)
	if err != nil {
		watcher.logger.Error("failed-to-extract-container-from-actual", err)
		return
	}

	messagesToEmit := watcher.table.RemoveContainer(actualLRP.ProcessGuid, container)
	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}

func desiredLRPData(lrp models.DesiredLRP) lager.Data {
	return lager.Data{
		"process-guid": lrp.ProcessGuid,
		"routes":       lrp.Routes,
		"ports":        lrp.Ports,
	}
}

func actualLRPData(lrp models.ActualLRP) lager.Data {
	return lager.Data{
		"process-guid":  lrp.ActualLRPKey.ProcessGuid,
		"index":         lrp.ActualLRPKey.Index,
		"container-key": lrp.ActualLRPContainerKey,
		"net-info":      lrp.ActualLRPNetInfo,
	}
}
