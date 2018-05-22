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
	loggingclient "code.cloudfoundry.org/diego-logging-client"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/routingtable"
)

const (
	routeSyncDuration = "RouteEmitterSyncDuration"
)

//go:generate counterfeiter -o fakes/fake_routehandler.go . RouteHandler
type RouteHandler interface {
	HandleEvent(logger lager.Logger, event models.Event)
	Sync(
		logger lager.Logger,
		desired []*models.DesiredLRPSchedulingInfo,
		runningActual []*routingtable.ActualLRPRoutingInfo,
		domains models.DomainSet,
		cachedEvents map[string]models.Event,
	)
	EmitExternal(logger lager.Logger)
	EmitInternal(logger lager.Logger)
	ShouldRefreshDesired(*routingtable.ActualLRPRoutingInfo) bool
	RefreshDesired(lager.Logger, []*models.DesiredLRPSchedulingInfo)
}

type Watcher struct {
	cellID         string
	bbsClient      bbs.Client
	clock          clock.Clock
	routeHandler   RouteHandler
	syncCh         chan struct{}
	emitExternalCh chan struct{}
	emitInternalCh chan struct{}
	logger         lager.Logger
	metronClient   loggingclient.IngressClient
}

func NewWatcher(
	cellID string,
	bbsClient bbs.Client,
	clock clock.Clock,
	routeHandler RouteHandler,
	syncCh chan struct{},
	emitExternalCh chan struct{},
	emitInternalCh chan struct{},
	logger lager.Logger,
	metronClient loggingclient.IngressClient,
) *Watcher {
	return &Watcher{
		cellID:         cellID,
		bbsClient:      bbsClient,
		clock:          clock,
		routeHandler:   routeHandler,
		syncCh:         syncCh,
		emitExternalCh: emitExternalCh,
		emitInternalCh: emitInternalCh,
		logger:         logger.Session("watcher"),
		metronClient:   metronClient,
	}
}

type syncEventResult struct {
	startTime     time.Time
	desired       []*models.DesiredLRPSchedulingInfo
	runningActual []*routingtable.ActualLRPRoutingInfo
	domains       models.DomainSet
	err           error
}

func (watcher *Watcher) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	watcher.logger.Debug("starting", lager.Data{"cell-id": watcher.cellID})
	defer watcher.logger.Debug("finished")

	eventChan := make(chan models.Event)
	resubscribeChannel := make(chan error)

	eventSource := &atomic.Value{}
	var stopEventSource int32

	go watcher.checkForEvents(resubscribeChannel, eventChan, eventSource, watcher.logger)
	watcher.logger.Debug("listening-on-channels")
	close(ready)
	watcher.logger.Debug("started")

	cachedEvents := make(map[string]models.Event)
	syncEnd := make(chan *syncEventResult)
	syncing := false

	for {
		select {
		case event := <-eventChan:
			if syncing {
				watcher.logger.Info("caching-event", lager.Data{
					"type": event.EventType(),
				})
				cachedEvents[event.Key()] = event
				continue
			}
			logger := watcher.logger.Session("handling-event")
			watcher.handleEvent(logger, event)
		case <-watcher.emitExternalCh:
			logger := watcher.logger.Session("emit-external")
			watcher.routeHandler.EmitExternal(logger)
		case <-watcher.emitInternalCh:
			logger := watcher.logger.Session("emit-internal")
			watcher.routeHandler.EmitInternal(logger)
		case syncEvent := <-syncEnd:
			syncing = false
			logger := watcher.logger.Session("sync")
			if syncEvent.err != nil {
				logger.Error("failed-to-sync-events", syncEvent.err)
				continue
			}

			var cachedDesired []*models.DesiredLRPSchedulingInfo
			for _, e := range cachedEvents {
				desired := watcher.retrieveDesiredWhileSyncing(logger, e, syncEvent.desired)
				if len(desired) > 0 {
					cachedDesired = append(cachedDesired, desired...)
				}
			}

			if len(cachedDesired) > 0 {
				syncEvent.desired = append(syncEvent.desired, cachedDesired...)
			}

			logger.Debug("calling-handler-sync")
			watcher.routeHandler.Sync(logger,
				syncEvent.desired,
				syncEvent.runningActual,
				syncEvent.domains,
				cachedEvents,
			)

			after := watcher.clock.Now()
			if err := watcher.metronClient.SendDuration(routeSyncDuration, after.Sub(syncEvent.startTime)); err != nil {
				watcher.logger.Error("failed-to-send-route-sync-duration-metric", err)
			}

			cachedEvents = make(map[string]models.Event)
			logger.Info("complete")
		case <-watcher.syncCh:
			if syncing {
				watcher.logger.Debug("sync-already-in-progress")
				continue
			}
			logger := watcher.logger.Session("sync")
			logger.Info("starting")
			go watcher.sync(logger, syncEnd)
			syncing = true
		case err := <-resubscribeChannel:
			watcher.logger.Error("event-source-error", err)
			if es := eventSource.Load(); es != nil {
				err := es.(events.EventSource).Close()
				if err != nil {
					watcher.logger.Error("failed-closing-event-source", err)
				}
			}
			go watcher.checkForEvents(resubscribeChannel, eventChan, eventSource, watcher.logger)

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

func (w *Watcher) retrieveDesiredInternal(logger lager.Logger, event models.Event, currentDesireds []*models.DesiredLRPSchedulingInfo, syncing bool) []*models.DesiredLRPSchedulingInfo {
	var routingInfo *routingtable.ActualLRPRoutingInfo
	var err error
	switch event := event.(type) {
	case *models.ActualLRPCreatedEvent:
		routingInfo, err = routingtable.NewActualLRPRoutingInfo(event.ActualLrpGroup)
	case *models.ActualLRPChangedEvent:
		routingInfo, err = routingtable.NewActualLRPRoutingInfo(event.After)
	default:
		return nil
	}
	if err != nil {
		logger.Error("failed-to-resolve", err, lager.Data{"event-type": event.EventType()})
		return nil
	}
	var desiredLRPs []*models.DesiredLRPSchedulingInfo
	if routingInfo.ActualLRP.State != models.ActualLRPStateRunning {
		return nil
	}
	if w.routeHandler.ShouldRefreshDesired(routingInfo) || (syncing && !foundInCurrentDesireds(routingInfo.ActualLRP.ProcessGuid, currentDesireds)) {
		logger.Info("refreshing-desired-lrp-info", lager.Data{"process-guid": routingInfo.ActualLRP.ProcessGuid})
		desiredLRPs, err = w.bbsClient.DesiredLRPSchedulingInfos(logger, models.DesiredLRPFilter{
			ProcessGuids: []string{routingInfo.ActualLRP.ProcessGuid},
		})
		if err != nil {
			logger.Error("failed-getting-desired-lrps-for-missing-actual-lrp", err)
		}
	}

	return desiredLRPs
}

func (w *Watcher) retrieveDesired(logger lager.Logger, event models.Event) []*models.DesiredLRPSchedulingInfo {
	return w.retrieveDesiredInternal(logger, event, nil, false)
}

func (w *Watcher) retrieveDesiredWhileSyncing(logger lager.Logger, event models.Event, currentDesireds []*models.DesiredLRPSchedulingInfo) []*models.DesiredLRPSchedulingInfo {
	return w.retrieveDesiredInternal(logger, event, currentDesireds, true)
}

func foundInCurrentDesireds(guid string, currentDesireds []*models.DesiredLRPSchedulingInfo) bool {
	for _, d := range currentDesireds {
		if d.ProcessGuid == guid {
			return true
		}
	}

	return false
}

func (w *Watcher) handleEvent(logger lager.Logger, event models.Event) {
	desiredLRPs := w.retrieveDesired(logger, event)
	if len(desiredLRPs) > 0 {
		w.routeHandler.RefreshDesired(logger, desiredLRPs)
	}
	w.routeHandler.HandleEvent(logger, event)
}

func (w *Watcher) sync(logger lager.Logger, ch chan<- *syncEventResult) {
	var desiredSchedulingInfo []*models.DesiredLRPSchedulingInfo
	var runningActualLRPs []*routingtable.ActualLRPRoutingInfo
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

		runningActualLRPs = make([]*routingtable.ActualLRPRoutingInfo, 0, len(actualLRPGroups))
		for _, actualLRPGroup := range actualLRPGroups {
			actualLRP, evacuating, err := actualLRPGroup.Resolve()
			if err != nil {
				logger.Error("failed-resolving-actual-lrp", err, lager.Data{"actual-lrp-group": actualLRPGroup})
			} else if actualLRP.State == models.ActualLRPStateRunning {
				runningActualLRPs = append(runningActualLRPs, &routingtable.ActualLRPRoutingInfo{
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

	var err error
	if actualErr != nil || desiredErr != nil || domainsErr != nil {
		err = fmt.Errorf("failed to sync: %s, %s, %s", actualErr, desiredErr, domainsErr)
	}

	ch <- &syncEventResult{
		startTime:     before,
		desired:       desiredSchedulingInfo,
		runningActual: runningActualLRPs,
		domains:       domains,
		err:           err,
	}
}

func (w *Watcher) checkForEvents(resubscribeChannel chan error, eventChan chan models.Event, eventSource *atomic.Value, logger lager.Logger) {
	var err error
	var es events.EventSource

	logger.Info("subscribing-to-bbs-events")
	es, err = w.bbsClient.SubscribeToEventsByCellID(logger, w.cellID)
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
