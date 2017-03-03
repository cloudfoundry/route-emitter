package routehandlers

import (
	"errors"
	"sync"

	"code.cloudfoundry.org/bbs"
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/emitter"
	"code.cloudfoundry.org/route-emitter/routing_table"
	"code.cloudfoundry.org/route-emitter/routing_table/schema/event"
	"code.cloudfoundry.org/route-emitter/routing_table/util"
)

type routingAPIHandler struct {
	logger       lager.Logger
	routingTable routing_table.TCPRoutingTable
	emitter      emitter.RoutingAPIEmitter
	bbsClient    bbs.Client
	syncing      bool
	cachedEvents []models.Event
	sync.Locker
}

func NewRouteHandler(logger lager.Logger, routingTable routing_table.TCPRoutingTable, emitter emitter.RoutingAPIEmitter, bbsClient bbs.Client) RouteHandler {
	return &routingAPIHandler{
		logger:       logger,
		routingTable: routingTable,
		emitter:      emitter,
		bbsClient:    bbsClient,
		syncing:      false,
		cachedEvents: nil,
		Locker:       &sync.Mutex{},
	}
}

func (handler *routingAPIHandler) Syncing() bool {
	handler.Lock()
	defer handler.Unlock()
	return handler.syncing
}

func (handler *routingAPIHandler) HandleEvent(event models.Event) {
	handler.Lock()
	defer handler.Unlock()
	if handler.syncing {
		handler.logger.Debug("caching-events")
		handler.cachedEvents = append(handler.cachedEvents, event)
	} else {
		handler.handleEvent(event)
	}
}

func (handler *routingAPIHandler) Sync() {
	logger := handler.logger.Session("bulk-sync")
	logger.Debug("starting")

	var tempRoutingTable routing_table.TCPRoutingTable

	defer func() {
		handler.Lock()
		handler.applyCachedEvents(logger, tempRoutingTable)
		handler.syncing = false
		handler.cachedEvents = nil
		handler.Unlock()
		logger.Debug("completed")
	}()

	handler.Lock()
	handler.cachedEvents = []models.Event{}
	handler.syncing = true
	handler.Unlock()

	var runningActualLRPs []*models.ActualLRPGroup
	var getActualLRPsErr error
	var desiredLRPs []*models.DesiredLRP
	var getDesiredLRPsErr error

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()

		logger.Debug("getting-actual-lrps")
		actualLRPResponses, err := handler.bbsClient.ActualLRPGroups(logger, models.ActualLRPFilter{})
		if err != nil {
			logger.Error("failed-getting-actual-lrps", err)
			getActualLRPsErr = err
			return
		}
		logger.Debug("succeeded-getting-actual-lrps", lager.Data{"num-actual-responses": len(actualLRPResponses)})

		runningActualLRPs = make([]*models.ActualLRPGroup, 0, len(actualLRPResponses))
		for _, actualLRPResponse := range actualLRPResponses {
			actual, _ := actualLRPResponse.Resolve()
			if actual.State == models.ActualLRPStateRunning {
				runningActualLRPs = append(runningActualLRPs, actualLRPResponse)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		logger.Debug("getting-desired-lrps")
		desiredLRPResponses, err := handler.bbsClient.DesiredLRPs(logger, models.DesiredLRPFilter{})
		if err != nil {
			logger.Error("failed-getting-desired-lrps", err)
			getDesiredLRPsErr = err
			return
		}
		logger.Debug("succeeded-getting-desired-lrps", lager.Data{"num-desired-responses": len(desiredLRPResponses)})

		desiredLRPs = make([]*models.DesiredLRP, 0, len(desiredLRPResponses))
		for _, desiredLRPResponse := range desiredLRPResponses {
			desiredLRPs = append(desiredLRPs, desiredLRPResponse)
		}
	}()

	wg.Wait()

	if getActualLRPsErr == nil && getDesiredLRPsErr == nil {
		tempRoutingTable = routing_table.NewTCPTable(handler.logger, nil)
		handler.logger.Debug("construct-routing-table")
		for _, desireLrp := range desiredLRPs {
			tempRoutingTable.AddRoutes(desireLrp)
		}

		for _, actualLrp := range runningActualLRPs {
			tempRoutingTable.AddEndpoint(actualLrp)
		}
	} else {
		logger.Info("sync-failed")
	}
}

func (handler *routingAPIHandler) applyCachedEvents(logger lager.Logger, tempRoutingTable routing_table.TCPRoutingTable) {
	logger.Debug("apply-cached-events")
	if tempRoutingTable == nil || tempRoutingTable.RouteCount() == 0 {
		// sync failed, process the events on the current table
		handler.logger.Debug("handling-events-from-failed-sync")
		for _, e := range handler.cachedEvents {
			handler.handleEvent(e)
		}
		logger.Debug("done-handling-events-from-failed-sync")
		return
	}

	logger.Debug("tempRoutingTable", lager.Data{"route-count": tempRoutingTable.RouteCount()})

	table := handler.routingTable
	emitter := handler.emitter

	handler.routingTable = tempRoutingTable
	handler.emitter = nil
	for _, e := range handler.cachedEvents {
		handler.handleEvent(e)
	}

	handler.routingTable = table
	handler.emitter = emitter

	logger.Debug("applied-cached-events")
	routingEvents := handler.routingTable.Swap(tempRoutingTable)
	logger.Debug("swap-complete", lager.Data{"events": len(routingEvents)})
	handler.emit(routingEvents)
}

func (handler *routingAPIHandler) handleEvent(event models.Event) {
	switch event := event.(type) {
	case *models.DesiredLRPCreatedEvent:
		handler.handleDesiredCreate(event.DesiredLrp)
	case *models.DesiredLRPChangedEvent:
		handler.handleDesiredUpdate(event.Before, event.After)
	case *models.DesiredLRPRemovedEvent:
		handler.handleDesiredDelete(event.DesiredLrp)
	case *models.ActualLRPCreatedEvent:
		handler.handleActualCreate(event.ActualLrpGroup)
	case *models.ActualLRPChangedEvent:
		handler.handleActualUpdate(event.Before, event.After)
	case *models.ActualLRPRemovedEvent:
		handler.handleActualDelete(event.ActualLrpGroup)
	default:
		handler.logger.Error("did-not-handle-unrecognizable-event", errors.New("unrecognizable-event"), lager.Data{"event-type": event.EventType()})
	}
}

func (handler *routingAPIHandler) handleDesiredCreate(desiredLRP *models.DesiredLRP) {
	logger := handler.logger.Session("handle-desired-create", util.DesiredLRPData(desiredLRP))
	logger.Debug("starting")
	defer logger.Debug("complete")
	routingEvents := handler.routingTable.AddRoutes(desiredLRP)
	handler.emit(routingEvents)
}

func (handler *routingAPIHandler) handleDesiredUpdate(before, after *models.DesiredLRP) {
	logger := handler.logger.Session("handling-desired-update", lager.Data{
		"before": util.DesiredLRPData(before),
		"after":  util.DesiredLRPData(after),
	})
	logger.Debug("starting")
	defer logger.Debug("complete")

	routingEvents := handler.routingTable.UpdateRoutes(before, after)
	handler.emit(routingEvents)
}

func (handler *routingAPIHandler) handleDesiredDelete(desiredLRP *models.DesiredLRP) {
	logger := handler.logger.Session("handling-desired-delete", util.DesiredLRPData(desiredLRP))
	logger.Debug("starting")
	defer logger.Debug("complete")
	routingEvents := handler.routingTable.RemoveRoutes(desiredLRP)
	handler.emit(routingEvents)
}

func (handler *routingAPIHandler) handleActualCreate(actualLRPGrp *models.ActualLRPGroup) {
	actualLRP, evacuating := actualLRPGrp.Resolve()
	logger := handler.logger.Session("handling-actual-create", util.ActualLRPData(actualLRP, evacuating))
	logger.Debug("starting")
	defer logger.Debug("complete")
	if actualLRP.State == models.ActualLRPStateRunning {
		handler.addAndEmit(actualLRPGrp)
	}
}

func (handler *routingAPIHandler) addAndEmit(actualLRPGrp *models.ActualLRPGroup) {
	routingEvents := handler.routingTable.AddEndpoint(actualLRPGrp)
	handler.emit(routingEvents)
}

func (handler *routingAPIHandler) removeAndEmit(actualLRPGrp *models.ActualLRPGroup) {
	routingEvents := handler.routingTable.RemoveEndpoint(actualLRPGrp)
	handler.emit(routingEvents)
}

func (handler *routingAPIHandler) emit(routingEvents event.RoutingEvents) {
	if handler.emitter != nil && len(routingEvents) > 0 {
		handler.emitter.Emit(routingEvents)
	}
}

func (handler *routingAPIHandler) handleActualUpdate(beforeGrp, afterGrp *models.ActualLRPGroup) {
	before, beforeEvacuating := beforeGrp.Resolve()
	after, afterEvacuating := afterGrp.Resolve()
	logger := handler.logger.Session("handling-actual-update", lager.Data{
		"before": util.ActualLRPData(before, beforeEvacuating),
		"after":  util.ActualLRPData(after, afterEvacuating),
	})
	logger.Debug("starting")
	defer logger.Debug("complete")

	switch {
	case after.State == models.ActualLRPStateRunning:
		handler.addAndEmit(afterGrp)
	case after.State != models.ActualLRPStateRunning && before.State == models.ActualLRPStateRunning:
		handler.removeAndEmit(beforeGrp)
	}
}

func (handler *routingAPIHandler) handleActualDelete(actualLRPGrp *models.ActualLRPGroup) {
	actualLRP, evacuating := actualLRPGrp.Resolve()
	logger := handler.logger.Session("handling-actual-delete", util.ActualLRPData(actualLRP, evacuating))
	logger.Debug("starting")
	defer logger.Debug("complete")
	if actualLRP.State == models.ActualLRPStateRunning {
		handler.removeAndEmit(actualLRPGrp)
	}
}
