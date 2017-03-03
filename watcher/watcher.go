package watcher

import (
	"os"
	"sync/atomic"

	"code.cloudfoundry.org/bbs"
	"code.cloudfoundry.org/bbs/events"
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/routing_table"
	"code.cloudfoundry.org/runtimeschema/metric"
)

var (
	routeSyncDuration = metric.Duration("RouteEmitterSyncDuration")
)

//go:generate counterfeiter -o fakes/fake_routehandler.go . RouteHandler
type RouteHandler interface {
	HandleEvent(event models.Event)
	Sync(
		desired []*models.DesiredLRPSchedulingInfo,
		runningActual []*routing_table.ActualLRPRoutingInfo,
		domains models.DomainSet,
	)
}

type Watcher struct {
	cellID       string
	bbsClient    bbs.Client
	clock        clock.Clock
	routeHandler RouteHandler
	syncChannel  chan struct{}
	logger       lager.Logger
}

func NewWatcher(
	cellID string,
	bbsClient bbs.Client,
	clock clock.Clock,
	routeHandler RouteHandler,
	syncChannel chan struct{},
	logger lager.Logger,
) *Watcher {
	return &Watcher{
		cellID:       cellID,
		bbsClient:    bbsClient,
		clock:        clock,
		routeHandler: routeHandler,
		syncChannel:  syncChannel,
		logger:       logger.Session("watcher"),
	}
}

func (watcher *Watcher) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	watcher.logger.Debug("starting")
	defer watcher.logger.Debug("finished")

	eventChan := make(chan models.Event)
	resubscribeChannel := make(chan error)

	eventSource := &atomic.Value{}
	var stopEventSource int32

	go checkForEvents(watcher.bbsClient, resubscribeChannel,
		eventChan, eventSource, watcher.logger)
	watcher.logger.Debug("listening-on-channels")
	close(ready)
	watcher.logger.Debug("started")

	for {
		select {
		case event := <-eventChan:
			watcher.routeHandler.HandleEvent(event)

		case <-watcher.syncChannel:
			before := watcher.clock.Now()

			_, err := watcher.bbsClient.DesiredLRPSchedulingInfos(watcher.logger, models.DesiredLRPFilter{
				ProcessGuids: nil,
			})
			if err != nil {
				continue
			}
			_, err = watcher.bbsClient.ActualLRPGroups(watcher.logger, models.ActualLRPFilter{CellID: watcher.cellID})
			if err != nil {
				continue
			}
			_, err = watcher.bbsClient.Domains(watcher.logger)
			if err != nil {
				continue
			}
			watcher.routeHandler.Sync(nil, nil, nil)

			after := watcher.clock.Now()
			err = routeSyncDuration.Send(after.Sub(before))
			if err != nil {
				watcher.logger.Error("failed-to-send-route-sync-duration-metric", err)
			}

		case err := <-resubscribeChannel:
			watcher.logger.Error("event-source-error", err)
			if es := eventSource.Load(); es != nil {
				err := es.(events.EventSource).Close()
				if err != nil {
					watcher.logger.Error("failed-closing-event-source", err)
				}
			}
			go checkForEvents(watcher.bbsClient, resubscribeChannel,
				eventChan, eventSource, watcher.logger)

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
func checkForEvents(bbsClient bbs.Client, resubscribeChannel chan error,
	eventChan chan models.Event, eventSource *atomic.Value, logger lager.Logger) {
	var err error
	var es events.EventSource

	logger.Info("subscribing-to-bbs-events")
	es, err = bbsClient.SubscribeToEvents(logger)
	if err != nil {
		resubscribeChannel <- err
		return
	}
	logger.Info("subscribed-to-bbs-events")

	eventSource.Store(es)

	var event models.Event
	for {
		event, err = es.Next()
		if err != nil {
			switch err {
			case events.ErrUnrecognizedEventType:
				logger.Error("failed-getting-next-event", err)
			default:
				resubscribeChannel <- err
				return
			}
		}

		if event != nil {
			eventChan <- event
		}
	}
}
