package watcher

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"code.cloudfoundry.org/bbs"
	"code.cloudfoundry.org/bbs/events"
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/route-emitter/syncer"
	"code.cloudfoundry.org/runtimeschema/metric"
)

var (
	routeSyncDuration = metric.Duration("RouteEmitterSyncDuration")
)

//go:generate counterfeiter -o fakes/fake_routehandler.go . RouteHandler
type RouteHandler interface {
	HandleEvent(logger lager.Logger, event models.Event)
	Sync(
		logger lager.Logger,
		desired []*models.DesiredLRPSchedulingInfo,
		runningActual []*endpoint.ActualLRPRoutingInfo,
		domains models.DomainSet,
		cachedEvents map[string]models.Event,
	)
	Emit(logger lager.Logger)
	ShouldRefreshDesired(*endpoint.ActualLRPRoutingInfo) bool
	RefreshDesired(lager.Logger, []*models.DesiredLRPSchedulingInfo)
}

type Watcher struct {
	cellID       string
	bbsClient    bbs.Client
	clock        clock.Clock
	routeHandler RouteHandler
	syncEvents   syncer.Events
	logger       lager.Logger
}

func NewWatcher(
	cellID string,
	bbsClient bbs.Client,
	clock clock.Clock,
	routeHandler RouteHandler,
	syncEvents syncer.Events,
	logger lager.Logger,
) *Watcher {
	return &Watcher{
		cellID:       cellID,
		bbsClient:    bbsClient,
		clock:        clock,
		routeHandler: routeHandler,
		syncEvents:   syncEvents,
		logger:       logger.Session("watcher"),
	}
}

type syncEventResult struct {
	startTime     time.Time
	desired       []*models.DesiredLRPSchedulingInfo
	runningActual []*endpoint.ActualLRPRoutingInfo
	domains       models.DomainSet
}

func (watcher *Watcher) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	watcher.logger.Debug("starting")
	defer watcher.logger.Debug("finished")

	eventChan := make(chan models.Event)
	cachedEventsChan := make(chan map[string]models.Event)
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
			logger := watcher.logger.Session("handling-event")
			watcher.handleEvent(logger, event)
		case <-watcher.syncEvents.Emit:
			logger := watcher.logger.Session("emit")
			watcher.routeHandler.Emit(logger)
		case <-watcher.syncEvents.Sync:
			logger := watcher.logger.Session("sync")
			logger.Debug("starting")

			done := make(chan struct{})
			go watcher.cacheIncomingEvents(eventChan, cachedEventsChan, done)
			syncEvent, err := watcher.sync(logger)

			close(done)

			// always pull the cachedEvents map off the channel
			cachedEvents := <-cachedEventsChan
			for _, e := range cachedEvents {
				watcher.refreshDesired(logger, e)
			}

			if err != nil {
				logger.Error("failed-to-sync-events", err)
				continue
			}

			watcher.routeHandler.Sync(logger,
				syncEvent.desired,
				syncEvent.runningActual,
				syncEvent.domains,
				cachedEvents,
			)

			after := watcher.clock.Now()
			err = routeSyncDuration.Send(after.Sub(syncEvent.startTime))

			if err != nil {
				watcher.logger.Error("failed-to-send-route-sync-duration-metric", err)
			}

			logger.Debug("complete")
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

func (w *Watcher) cacheIncomingEvents(
	eventChan chan models.Event,
	cachedEventsChan chan map[string]models.Event,
	done chan struct{},
) {
	cachedEvents := make(map[string]models.Event)
	for {
		select {
		case event := <-eventChan:
			w.logger.Info("caching-event", lager.Data{
				"type": event.EventType(),
			})
			cachedEvents[event.Key()] = event
		case <-done:
			cachedEventsChan <- cachedEvents
			return
		}
	}
}

func (w *Watcher) refreshDesired(logger lager.Logger, event models.Event) {
	var routingInfo *endpoint.ActualLRPRoutingInfo
	switch event := event.(type) {
	case *models.ActualLRPCreatedEvent:
		routingInfo = endpoint.NewActualLRPRoutingInfo(event.ActualLrpGroup)
	case *models.ActualLRPChangedEvent:
		routingInfo = endpoint.NewActualLRPRoutingInfo(event.After)
	default:
	}

	if routingInfo != nil && routingInfo.ActualLRP.State == models.ActualLRPStateRunning {
		if w.routeHandler.ShouldRefreshDesired(routingInfo) {
			logger.Debug("refreshing-desired-lrp-info", lager.Data{"routing-info": routingInfo})
			desiredLRPs, err := w.bbsClient.DesiredLRPSchedulingInfos(logger, models.DesiredLRPFilter{
				ProcessGuids: []string{routingInfo.ActualLRP.ProcessGuid},
			})
			if err != nil {
				logger.Error("failed-getting-desired-lrps-for-missing-actual-lrp", err)
			} else {
				w.routeHandler.RefreshDesired(logger, desiredLRPs)
			}
		}
	}
}

func (w *Watcher) handleEvent(logger lager.Logger, event models.Event) {
	w.refreshDesired(logger, event)
	w.routeHandler.HandleEvent(logger, event)
}

func (w *Watcher) sync(logger lager.Logger) (*syncEventResult, error) {
	var desiredSchedulingInfo []*models.DesiredLRPSchedulingInfo
	var runningActualLRPs []*endpoint.ActualLRPRoutingInfo
	var domains models.DomainSet

	var actualErr, desiredErr, domainsErr error
	before := w.clock.Now()

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Debug("getting-actual-lrps")
		var actualLRPGroups []*models.ActualLRPGroup
		actualLRPGroups, actualErr = w.bbsClient.ActualLRPGroups(logger, models.ActualLRPFilter{CellID: w.cellID})
		if actualErr != nil {
			logger.Error("failed-getting-actual-lrps", actualErr)
			return
		}
		logger.Debug("succeeded-getting-actual-lrps", lager.Data{"num-actual-responses": len(actualLRPGroups)})

		runningActualLRPs = make([]*endpoint.ActualLRPRoutingInfo, 0, len(actualLRPGroups))
		for _, actualLRPGroup := range actualLRPGroups {
			actualLRP, evacuating := actualLRPGroup.Resolve()
			if actualLRP.State == models.ActualLRPStateRunning {
				runningActualLRPs = append(runningActualLRPs, &endpoint.ActualLRPRoutingInfo{
					ActualLRP:  actualLRP,
					Evacuating: evacuating,
				})
			}
		}

		if w.cellID != "" {
			guids := make([]string, 0, len(runningActualLRPs))
			// filter the desired lrp scheduling info by process guids
			for _, lrpInfo := range runningActualLRPs {
				guids = append(guids, lrpInfo.ActualLRP.ProcessGuid)
			}
			if len(guids) > 0 {
				desiredSchedulingInfo, desiredErr = getSchedulingInfos(logger, w.bbsClient, guids)
			}
		}
	}()

	if w.cellID == "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			desiredSchedulingInfo, desiredErr = getSchedulingInfos(logger, w.bbsClient, nil)
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		var domainArray []string
		logger.Debug("getting-domains")
		domainArray, domainsErr = w.bbsClient.Domains(logger)
		if domainsErr != nil {
			logger.Error("failed-getting-domains", domainsErr)
			return
		}

		domains = models.NewDomainSet(domainArray)
		logger.Debug("succeeded-getting-domains", lager.Data{"num-domains": len(domains)})
	}()

	wg.Wait()

	if actualErr != nil || desiredErr != nil || domainsErr != nil {
		return nil, fmt.Errorf("failed to sync: %s, %s, %s", actualErr, desiredErr, domainsErr)
	}

	return &syncEventResult{
		startTime:     before,
		desired:       desiredSchedulingInfo,
		runningActual: runningActualLRPs,
		domains:       domains,
	}, nil
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

func getSchedulingInfos(logger lager.Logger, bbsClient bbs.Client, guids []string) ([]*models.DesiredLRPSchedulingInfo, error) {
	logger.Debug("getting-scheduling-infos", lager.Data{"guids-length": len(guids)})
	schedulingInfos, err := bbsClient.DesiredLRPSchedulingInfos(logger, models.DesiredLRPFilter{
		ProcessGuids: guids,
	})
	if err != nil {
		logger.Error("failed-getting-scheduling-infos", err)
		return nil, err
	}

	logger.Debug("succeeded-getting-scheduling-infos", lager.Data{"num-desired-responses": len(schedulingInfos)})
	return schedulingInfos, nil
}
