package routehandlers

import (
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/route-emitter/watcher"
)

type MultiHandler struct {
	handlers []watcher.RouteHandler
}

var _ watcher.RouteHandler = new(MultiHandler)

func NewMultiHandler(handlers ...watcher.RouteHandler) *MultiHandler {
	return &MultiHandler{
		handlers: handlers,
	}
}

func (h *MultiHandler) HandleEvent(logger lager.Logger, event models.Event) {
	for _, rh := range h.handlers {
		rh.HandleEvent(logger, event)
	}
}

func (h *MultiHandler) Sync(
	logger lager.Logger,
	desired []*models.DesiredLRPSchedulingInfo,
	runningActual []*endpoint.ActualLRPRoutingInfo,
	domains models.DomainSet,
	cachedEvents map[string]models.Event,
) {
	for _, rh := range h.handlers {
		rh.Sync(logger, desired, runningActual, domains, cachedEvents)
	}
}

func (h *MultiHandler) Emit(logger lager.Logger) {
	for _, rh := range h.handlers {
		rh.Emit(logger)
	}
}

func (h *MultiHandler) ShouldRefreshDesired(a *endpoint.ActualLRPRoutingInfo) bool {
	var refresh bool
	for _, rh := range h.handlers {
		refresh = refresh || rh.ShouldRefreshDesired(a)
	}

	return refresh
}

func (h *MultiHandler) RefreshDesired(logger lager.Logger, d []*models.DesiredLRPSchedulingInfo) {
	for _, rh := range h.handlers {
		rh.RefreshDesired(logger, d)
	}
}
