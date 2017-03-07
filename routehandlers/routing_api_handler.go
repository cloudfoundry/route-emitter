package routehandlers

import (
	"errors"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/emitter"
	"code.cloudfoundry.org/route-emitter/routing_table"
	"code.cloudfoundry.org/route-emitter/routing_table/schema/endpoint"
	"code.cloudfoundry.org/route-emitter/routing_table/schema/event"
	"code.cloudfoundry.org/route-emitter/routing_table/util"
	"code.cloudfoundry.org/route-emitter/watcher"
)

type RoutingAPIHandler struct {
	routingTable routing_table.TCPRoutingTable
	emitter      emitter.RoutingAPIEmitter
}

var _ watcher.RouteHandler = new(RoutingAPIHandler)

func NewRoutingAPIHandler(routingTable routing_table.TCPRoutingTable, emitter emitter.RoutingAPIEmitter) *RoutingAPIHandler {
	return &RoutingAPIHandler{
		routingTable: routingTable,
		emitter:      emitter,
	}
}

func (handler *RoutingAPIHandler) HandleEvent(logger lager.Logger, event models.Event) {
	switch event := event.(type) {
	case *models.DesiredLRPCreatedEvent:
		desiredInfo := event.DesiredLrp.DesiredLRPSchedulingInfo()
		handler.handleDesiredCreate(logger, &desiredInfo)
	case *models.DesiredLRPChangedEvent:
		before := event.Before.DesiredLRPSchedulingInfo()
		after := event.After.DesiredLRPSchedulingInfo()
		handler.handleDesiredUpdate(logger, &before, &after)
	case *models.DesiredLRPRemovedEvent:
		desiredInfo := event.DesiredLrp.DesiredLRPSchedulingInfo()
		handler.handleDesiredDelete(logger, &desiredInfo)
	case *models.ActualLRPCreatedEvent:
		routingInfo := endpoint.NewActualLRPRoutingInfo(event.ActualLrpGroup)
		handler.handleActualCreate(logger, routingInfo)
	case *models.ActualLRPChangedEvent:
		before := endpoint.NewActualLRPRoutingInfo(event.Before)
		after := endpoint.NewActualLRPRoutingInfo(event.After)
		handler.handleActualUpdate(logger, before, after)
	case *models.ActualLRPRemovedEvent:
		routingInfo := endpoint.NewActualLRPRoutingInfo(event.ActualLrpGroup)
		handler.handleActualDelete(logger, routingInfo)
	default:
		logger.Error("did-not-handle-unrecognizable-event", errors.New("unrecognizable-event"), lager.Data{"event-type": event.EventType()})
	}
}

func (handler *RoutingAPIHandler) Sync(
	logger lager.Logger,
	desired []*models.DesiredLRPSchedulingInfo,
	actuals []*endpoint.ActualLRPRoutingInfo,
	domains models.DomainSet,
) {
	logger.Debug("starting")
	defer logger.Debug("completed")

	var tempRoutingTable routing_table.TCPRoutingTable

	tempRoutingTable = routing_table.NewTCPTable(logger, nil)
	logger.Debug("construct-routing-table")
	for _, desireLrp := range desired {
		tempRoutingTable.AddRoutes(desireLrp)
	}

	for _, actualLrp := range actuals {
		tempRoutingTable.AddEndpoint(actualLrp)
	}

	if tempRoutingTable.RouteCount() == 0 {
		return
	}

	routingEvents := handler.routingTable.Swap(tempRoutingTable)
	logger.Debug("swap-complete", lager.Data{"events": len(routingEvents)})
	handler.emit(routingEvents)
}

func (handler *RoutingAPIHandler) RefreshDesired(desiredInfo []*models.DesiredLRPSchedulingInfo) {
	var routingEvents event.RoutingEvents
	for _, desiredLRP := range desiredInfo {
		routingEvents = append(routingEvents, handler.routingTable.AddRoutes(desiredLRP)...)
	}
	handler.emit(routingEvents)
}

func (handler *RoutingAPIHandler) ShouldRefreshDesired(actual *endpoint.ActualLRPRoutingInfo) bool {
	for _, key := range endpoint.NewRoutingKeysFromActual(actual) {
		if len(handler.routingTable.GetRoutes(key)) == 0 {
			return true
		}
	}

	return false
}

func (handler *RoutingAPIHandler) handleDesiredCreate(logger lager.Logger, desiredLRP *models.DesiredLRPSchedulingInfo) {
	logger = logger.Session("handle-desired-create", util.DesiredLRPData(desiredLRP))
	logger.Debug("starting")
	defer logger.Debug("complete")
	routingEvents := handler.routingTable.AddRoutes(desiredLRP)
	handler.emit(routingEvents)
}

func (handler *RoutingAPIHandler) handleDesiredUpdate(logger lager.Logger, before, after *models.DesiredLRPSchedulingInfo) {
	logger = logger.Session("handling-desired-update", lager.Data{
		"before": util.DesiredLRPData(before),
		"after":  util.DesiredLRPData(after),
	})
	logger.Debug("starting")
	defer logger.Debug("complete")

	routingEvents := handler.routingTable.UpdateRoutes(before, after)
	handler.emit(routingEvents)
}

func (handler *RoutingAPIHandler) handleDesiredDelete(logger lager.Logger, desiredLRP *models.DesiredLRPSchedulingInfo) {
	logger = logger.Session("handling-desired-delete", util.DesiredLRPData(desiredLRP))
	logger.Debug("starting")
	defer logger.Debug("complete")
	routingEvents := handler.routingTable.RemoveRoutes(desiredLRP)
	handler.emit(routingEvents)
}

func (handler *RoutingAPIHandler) handleActualCreate(logger lager.Logger, actualInfo *endpoint.ActualLRPRoutingInfo) {
	logger = logger.Session("handling-actual-create", util.ActualLRPData(actualInfo))
	logger.Debug("starting")
	defer logger.Debug("complete")
	if actualInfo.ActualLRP.State == models.ActualLRPStateRunning {
		handler.addAndEmit(actualInfo)
	}
}

func (handler *RoutingAPIHandler) addAndEmit(actualInfo *endpoint.ActualLRPRoutingInfo) {
	routingEvents := handler.routingTable.AddEndpoint(actualInfo)
	handler.emit(routingEvents)
}

func (handler *RoutingAPIHandler) removeAndEmit(actualInfo *endpoint.ActualLRPRoutingInfo) {
	routingEvents := handler.routingTable.RemoveEndpoint(actualInfo)
	handler.emit(routingEvents)
}

func (handler *RoutingAPIHandler) emit(routingEvents event.RoutingEvents) {
	if handler.emitter != nil && len(routingEvents) > 0 {
		handler.emitter.Emit(routingEvents)
	}
}

func (handler *RoutingAPIHandler) handleActualUpdate(logger lager.Logger, before, after *endpoint.ActualLRPRoutingInfo) {
	logger = logger.Session("handling-actual-update", lager.Data{
		"before": util.ActualLRPData(before),
		"after":  util.ActualLRPData(after),
	})
	logger.Debug("starting")
	defer logger.Debug("complete")

	switch {
	case after.ActualLRP.State == models.ActualLRPStateRunning:
		handler.addAndEmit(after)
	case after.ActualLRP.State != models.ActualLRPStateRunning && before.ActualLRP.State == models.ActualLRPStateRunning:
		handler.removeAndEmit(before)
	}
}

func (handler *RoutingAPIHandler) handleActualDelete(logger lager.Logger, actualInfo *endpoint.ActualLRPRoutingInfo) {
	logger = logger.Session("handling-actual-delete", util.ActualLRPData(actualInfo))
	logger.Debug("starting")
	defer logger.Debug("complete")
	if actualInfo.ActualLRP.State == models.ActualLRPStateRunning {
		handler.removeAndEmit(actualInfo)
	}
}
