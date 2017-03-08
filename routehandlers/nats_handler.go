package routehandlers

import (
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/emitter"
	"code.cloudfoundry.org/route-emitter/routing_table"
	"code.cloudfoundry.org/route-emitter/routing_table/schema/endpoint"
	"code.cloudfoundry.org/route-emitter/routing_table/util"
	"code.cloudfoundry.org/route-emitter/watcher"
	"code.cloudfoundry.org/routing-info/cfroutes"
)

type NATSHandler struct {
	routingTable routing_table.NATSRoutingTable
	emitter      emitter.NATSEmitter
}

var _ watcher.RouteHandler = new(NATSHandler)

func NewNATSHandler(routingTable routing_table.NATSRoutingTable, natsEmitter emitter.NATSEmitter) *NATSHandler {
	return &NATSHandler{
		routingTable: routingTable,
		emitter:      natsEmitter,
	}
}

func (handler *NATSHandler) HandleEvent(logger lager.Logger, event models.Event) {
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
		// default:
		// 	logger.Error("did-not-handle-unrecognizable-event", errors.New("unrecognizable-event"), lager.Data{"event-type": event.EventType()})
	}
}

func (handler *NATSHandler) Sync(
	logger lager.Logger,
	desired []*models.DesiredLRPSchedulingInfo,
	actuals []*endpoint.ActualLRPRoutingInfo,
	domains models.DomainSet,
) {
	schedInfoMap := make(map[string]*models.DesiredLRPSchedulingInfo)
	for _, schedInfo := range desired {
		schedInfoMap[schedInfo.ProcessGuid] = schedInfo
	}

	newTable := routing_table.NewTempTable(
		routing_table.RoutesByRoutingKeyFromSchedulingInfos(desired),
		routing_table.EndpointsByRoutingKeyFromActuals(actuals, schedInfoMap),
	)

	if newTable.RouteCount() == 0 {
		return
	}

	messages := handler.routingTable.Swap(newTable, domains)
	logger.Debug("start-emitting-messages", lager.Data{
		"num-registration-messages":   len(messages.RegistrationMessages),
		"num-unregistration-messages": len(messages.UnregistrationMessages),
	})
	handler.emitMessages(logger, messages)
	logger.Debug("done-emitting-messages", lager.Data{
		"num-registration-messages":   len(messages.RegistrationMessages),
		"num-unregistration-messages": len(messages.UnregistrationMessages),
	})
}

func (handler *NATSHandler) RefreshDesired(logger lager.Logger, desiredInfo []*models.DesiredLRPSchedulingInfo) {
	for _, desiredLRP := range desiredInfo {
		handler.setRoutesForDesired(logger, desiredLRP)
	}
}

func (handler *NATSHandler) ShouldRefreshDesired(actual *endpoint.ActualLRPRoutingInfo) bool {
	for _, key := range endpoint.NewRoutingKeysFromActual(actual) {
		if len(handler.routingTable.GetRoutes(key)) == 0 {
			return true
		}
	}

	return false
}

func (handler *NATSHandler) handleDesiredCreate(logger lager.Logger, desiredLRP *models.DesiredLRPSchedulingInfo) {
	logger = logger.Session("handle-desired-create", util.DesiredLRPData(desiredLRP))
	logger.Debug("starting")
	defer logger.Debug("complete")
	// routingEvents := handler.routingTable.AddRoutes(desiredLRP)
	handler.setRoutesForDesired(logger, desiredLRP)
	// handler.emit(routingEvents)
}

func (handler *NATSHandler) handleDesiredUpdate(logger lager.Logger, before, after *models.DesiredLRPSchedulingInfo) {
	logger = logger.Session("handling-desired-update", lager.Data{
		"before": util.DesiredLRPData(before),
		"after":  util.DesiredLRPData(after),
	})
	logger.Info("starting")
	defer logger.Info("complete")

	afterKeysSet := handler.setRoutesForDesired(logger, after)

	beforeRoutingKeys := routing_table.RoutingKeysFromSchedulingInfo(before)
	afterRoutes, _ := cfroutes.CFRoutesFromRoutingInfo(after.Routes)

	afterContainerPorts := set{}
	for _, route := range afterRoutes {
		afterContainerPorts.add(route.Port)
	}

	requestedInstances := after.Instances - before.Instances

	for _, key := range beforeRoutingKeys {
		if !afterKeysSet.contains(key) || !afterContainerPorts.contains(key.ContainerPort) {
			messagesToEmit := handler.routingTable.RemoveRoutes(key, &after.ModificationTag)
			handler.emitMessages(logger, messagesToEmit)
		}

		// in case of scale down, remove endpoints
		if requestedInstances < 0 {
			logger.Info("removing-endpoints", lager.Data{"removal_count": -1 * requestedInstances, "routing_key": key})

			for index := before.Instances - 1; index >= after.Instances; index-- {
				endpoints := handler.routingTable.EndpointsForIndex(key, index)

				for i := range endpoints {
					messagesToEmit := handler.routingTable.RemoveEndpoint(key, endpoints[i])
					handler.emitMessages(logger, messagesToEmit)
				}
			}
		}
	}
}

func (handler *NATSHandler) handleDesiredDelete(logger lager.Logger, schedulingInfo *models.DesiredLRPSchedulingInfo) {
	logger = logger.Session("handling-desired-delete", util.DesiredLRPData(schedulingInfo))
	logger.Info("starting")
	defer logger.Info("complete")
	for _, key := range routing_table.RoutingKeysFromSchedulingInfo(schedulingInfo) {
		messagesToEmit := handler.routingTable.RemoveRoutes(key, &schedulingInfo.ModificationTag)
		handler.emitMessages(logger, messagesToEmit)
	}
}

func (handler *NATSHandler) handleActualCreate(logger lager.Logger, actualLRPInfo *endpoint.ActualLRPRoutingInfo) {
	logger = logger.Session("handling-actual-create", util.ActualLRPData(actualLRPInfo))
	logger.Info("starting")
	defer logger.Info("complete")
	if actualLRPInfo.ActualLRP.State == models.ActualLRPStateRunning {
		handler.addAndEmit(logger, actualLRPInfo)
	}
}

func (handler *NATSHandler) handleActualUpdate(logger lager.Logger, before, after *endpoint.ActualLRPRoutingInfo) {
	logger = logger.Session("handling-actual-update", lager.Data{
		"before": util.ActualLRPData(before),
		"after":  util.ActualLRPData(after),
	})
	logger.Info("starting")
	defer logger.Info("complete")

	switch {
	case after.ActualLRP.State == models.ActualLRPStateRunning:
		handler.addAndEmit(logger, after)
	case after.ActualLRP.State != models.ActualLRPStateRunning && before.ActualLRP.State == models.ActualLRPStateRunning:
		handler.removeAndEmit(logger, before)
	}
}

func (handler *NATSHandler) handleActualDelete(logger lager.Logger, actualLRPInfo *endpoint.ActualLRPRoutingInfo) {
	logger = logger.Session("handling-actual-delete", util.ActualLRPData(actualLRPInfo))
	logger.Info("starting")
	defer logger.Info("complete")
	if actualLRPInfo.ActualLRP.State == models.ActualLRPStateRunning {
		handler.removeAndEmit(logger, actualLRPInfo)
	}
}

type set map[interface{}]struct{}

func (set set) contains(value interface{}) bool {
	_, found := set[value]
	return found
}

func (set set) add(value interface{}) {
	set[value] = struct{}{}
}

func (handler *NATSHandler) setRoutesForDesired(logger lager.Logger, schedulingInfo *models.DesiredLRPSchedulingInfo) set {
	routes, _ := cfroutes.CFRoutesFromRoutingInfo(schedulingInfo.Routes)
	routingKeySet := set{}

	routeEntries := make(map[endpoint.RoutingKey][]routing_table.Route)
	for _, route := range routes {
		key := endpoint.RoutingKey{ProcessGUID: schedulingInfo.ProcessGuid, ContainerPort: route.Port}
		routingKeySet.add(key)

		routes := []routing_table.Route{}
		for _, hostname := range route.Hostnames {
			routes = append(routes, routing_table.Route{
				Hostname:        hostname,
				LogGuid:         schedulingInfo.LogGuid,
				RouteServiceUrl: route.RouteServiceUrl,
			})
		}
		routeEntries[key] = append(routeEntries[key], routes...)
	}
	for key := range routeEntries {
		messagesToEmit := handler.routingTable.SetRoutes(key, routeEntries[key], nil)
		handler.emitMessages(logger, messagesToEmit)
	}

	return routingKeySet
}

func (handler *NATSHandler) emitMessages(logger lager.Logger, messagesToEmit routing_table.MessagesToEmit) {
	if handler.emitter != nil {
		logger.Debug("emit-messages", lager.Data{"messages": messagesToEmit})
		handler.emitter.Emit(messagesToEmit)
		// routesRegistered.Add(messagesToEmit.RouteRegistrationCount())
		// routesUnregistered.Add(messagesToEmit.RouteUnregistrationCount())
	}
}

func (handler *NATSHandler) addAndEmit(logger lager.Logger, actualLRPInfo *endpoint.ActualLRPRoutingInfo) {
	logger.Info("handler-add-and-emit", lager.Data{"net_info": actualLRPInfo.ActualLRP.ActualLRPNetInfo})
	endpoints, err := routing_table.EndpointsFromActual(actualLRPInfo)
	if err != nil {
		logger.Error("failed-to-extract-endpoint-from-actual", err)
		return
	}
	for _, routingEndpoint := range endpoints {
		key := endpoint.RoutingKey{ProcessGUID: actualLRPInfo.ActualLRP.ProcessGuid, ContainerPort: uint32(routingEndpoint.ContainerPort)}
		messagesToEmit := handler.routingTable.AddEndpoint(key, routingEndpoint)
		handler.emitMessages(logger, messagesToEmit)
	}
}

func (handler *NATSHandler) removeAndEmit(logger lager.Logger, actualLRPInfo *endpoint.ActualLRPRoutingInfo) {
	logger.Info("handler-remove-and-emit", lager.Data{"net_info": actualLRPInfo.ActualLRP.ActualLRPNetInfo})
	endpoints, err := routing_table.EndpointsFromActual(actualLRPInfo)
	if err != nil {
		logger.Error("failed-to-extract-endpoint-from-actual", err)
		return
	}

	for _, key := range routing_table.RoutingKeysFromActual(actualLRPInfo.ActualLRP) {
		for _, endpoint := range endpoints {
			if key.ContainerPort == endpoint.ContainerPort {
				messagesToEmit := handler.routingTable.RemoveEndpoint(key, endpoint)
				handler.emitMessages(logger, messagesToEmit)
			}
		}
	}
}
