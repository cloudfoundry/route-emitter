package routehandlers

import (
	"errors"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/emitter"
	"code.cloudfoundry.org/route-emitter/routingtable"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/event"
	"code.cloudfoundry.org/route-emitter/routingtable/util"
	"code.cloudfoundry.org/route-emitter/watcher"
	"code.cloudfoundry.org/runtimeschema/metric"
)

var (
	tcpRouteCount = metric.Metric("TCPRouteCount")
)

type RoutingAPIHandler struct {
	routingTable routingtable.TCPRoutingTable
	emitter      emitter.RoutingAPIEmitter
	localMode    bool
}

var _ watcher.RouteHandler = new(RoutingAPIHandler)

func NewRoutingAPIHandler(routingTable routingtable.TCPRoutingTable, emitter emitter.RoutingAPIEmitter, localMode bool) *RoutingAPIHandler {
	return &RoutingAPIHandler{
		routingTable: routingTable,
		emitter:      emitter,
		localMode:    localMode,
	}
}

func (handler *RoutingAPIHandler) HandleEvent(logger lager.Logger, event models.Event) {
	logger = logger.Session("routing-api-handle-event")
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
	cachedEvents map[string]models.Event,
) {
	logger = logger.Session("routing-api-sync")
	logger.Debug("starting")
	defer logger.Debug("completed")

	var tempRoutingTable routingtable.TCPRoutingTable

	tempRoutingTable = routingtable.NewTCPTable(logger, nil)
	logger.Debug("construct-routing-table")
	for _, desireLrp := range desired {
		tempRoutingTable.AddRoutes(desireLrp)
	}

	for _, actualLrp := range actuals {
		tempRoutingTable.AddEndpoint(actualLrp)
	}

	numRoutes := 0
	if tempRoutingTable.RouteCount() != 0 {
		routingEvents := handler.routingTable.Swap(tempRoutingTable)
		logger.Debug("swap-complete", lager.Data{"events": len(routingEvents)})
		numRoutes = handler.emit(routingEvents)
	}

	if handler.localMode {
		err := tcpRouteCount.Send(numRoutes)
		if err != nil {
			logger.Error("failed-to-send-tcp-route-count-metric", err)
		}
	}
}

func (handler *RoutingAPIHandler) Emit(logger lager.Logger) {
	logger = logger.Session("routing-api-emit")
	events := handler.routingTable.GetRoutingEvents()
	logger.Debug("emitting-messages", lager.Data{"messages": events})
	_, _, err := handler.emitter.Emit(events)
	if err != nil {
		logger.Error("failed-emitting-messages", err, lager.Data{"messages": events})
	}
}

func (handler *RoutingAPIHandler) RefreshDesired(logger lager.Logger, desiredInfo []*models.DesiredLRPSchedulingInfo) {
	var routingEvents event.RoutingEvents
	for _, desiredLRP := range desiredInfo {
		routingEvents = append(routingEvents, handler.routingTable.AddRoutes(desiredLRP)...)
	}
	handler.emit(routingEvents)
}

func (handler *RoutingAPIHandler) ShouldRefreshDesired(actual *endpoint.ActualLRPRoutingInfo) bool {
	for _, key := range endpoint.NewRoutingKeysFromActual(actual) {
		if len(handler.routingTable.GetRoutes(key)) > 0 {
			return false
		}
	}

	return true
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

func (handler *RoutingAPIHandler) emit(routingEvents event.RoutingEvents) int {
	numRegistrations := 0
	if handler.emitter != nil && len(routingEvents) > 0 {
		numRegistrations, _, _ = handler.emitter.Emit(routingEvents)
	}
	return numRegistrations
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
