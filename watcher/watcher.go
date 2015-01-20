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
					watcher.logger.Debug("failed-closing-event-source", lager.Data{"error-msg": err.Error()})
				}
			}
			return resubscribeErr

		case event := <-eventChan:
			watcher.logger.Info("handling-event", lager.Data{"event": event})
			watcher.handleEvent(event)

		case err := <-errChan:
			watcher.logger.Error("failed-getting-next-event", err)
			eventSource = nil

		case <-signals:
			watcher.logger.Info("stopping")
			if eventSource != nil {
				err := eventSource.Close()
				if err != nil {
					watcher.logger.Debug("failed-closing-event-source", lager.Data{"error-msg": err.Error()})
				}
			}
			return nil
		}
	}

	return nil
}

func (watcher *Watcher) handleEvent(event receptor.Event) {
	switch event := event.(type) {
	case receptor.DesiredLRPChangedEvent:
		watcher.handleDesiredCreateOrUpdate(serialization.DesiredLRPFromResponse(event.DesiredLRPResponse))
	case receptor.DesiredLRPRemovedEvent:
		watcher.handleDesiredDelete(serialization.DesiredLRPFromResponse(event.DesiredLRPResponse))
	case receptor.ActualLRPChangedEvent:
		watcher.handleActualCreateOrUpdate(serialization.ActualLRPFromResponse(event.ActualLRPResponse))
	case receptor.ActualLRPRemovedEvent:
		watcher.handleActualDelete(serialization.ActualLRPFromResponse(event.ActualLRPResponse))
	default:
		watcher.logger.Info("did-not-handle-unrecognizable-event", lager.Data{"event-type": event.EventType()})
	}
}

func (watcher *Watcher) handleDesiredCreateOrUpdate(desiredLRP models.DesiredLRP) {
	watcher.logger.Debug("handling-desired-create-or-update", lager.Data{"desired-lrp": desiredLRP})
	defer watcher.logger.Debug("done-handling-desired-create-or-update")

	messagesToEmit := watcher.table.SetRoutes(desiredLRP.ProcessGuid, routing_table.Routes{
		URIs:    desiredLRP.Routes,
		LogGuid: desiredLRP.LogGuid,
	})

	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}

func (watcher *Watcher) handleDesiredDelete(desiredLRP models.DesiredLRP) {
	watcher.logger.Debug("handling-desired-delete", lager.Data{"desired-lrp": desiredLRP})
	defer watcher.logger.Debug("done-handling-desired-delete")

	messagesToEmit := watcher.table.RemoveRoutes(desiredLRP.ProcessGuid)

	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}

func (watcher *Watcher) handleActualCreateOrUpdate(actualLRP models.ActualLRP) {
	watcher.logger.Debug("handling-actual-create-or-update", lager.Data{"actual-lrp": actualLRP})
	defer watcher.logger.Debug("done-handling-actual-create-or-update")

	container, err := routing_table.ContainerFromActual(actualLRP)
	if err != nil {
		watcher.logger.Error("failed-to-extract-container-from-actual", err)
		return
	}

	messagesToEmit := watcher.table.AddOrUpdateContainer(actualLRP.ProcessGuid, container)
	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}

func (watcher *Watcher) handleActualDelete(actualLRP models.ActualLRP) {
	watcher.logger.Debug("handling-actual-delete", lager.Data{"actual-lrp": actualLRP})
	defer watcher.logger.Debug("done-handling-actual-delete")

	container, err := routing_table.ContainerFromActual(actualLRP)
	if err != nil {
		return
	}

	messagesToEmit := watcher.table.RemoveContainer(actualLRP.ProcessGuid, container)
	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}
