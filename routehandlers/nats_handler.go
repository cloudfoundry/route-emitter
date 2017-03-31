package routehandlers

import (
	"errors"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/emitter"
	"code.cloudfoundry.org/route-emitter/routingtable"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/route-emitter/routingtable/util"
	"code.cloudfoundry.org/route-emitter/watcher"
	"code.cloudfoundry.org/routing-info/cfroutes"
	"code.cloudfoundry.org/runtimeschema/metric"
)

var (
	routesTotal  = metric.Metric("RoutesTotal")
	routesSynced = metric.Counter("RoutesSynced")

	routesRegistered   = metric.Counter("RoutesRegistered")
	routesUnregistered = metric.Counter("RoutesUnregistered")
)

type NATSHandler struct {
	routingTable routingtable.NATSRoutingTable
	emitter      emitter.NATSEmitter
}

var _ watcher.RouteHandler = new(NATSHandler)

func NewNATSHandler(routingTable routingtable.NATSRoutingTable, natsEmitter emitter.NATSEmitter) *NATSHandler {
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
	default:
		logger.Error("did-not-handle-unrecognizable-event", errors.New("unrecognizable-event"), lager.Data{"event-type": event.EventType()})
	}
}

func (handler *NATSHandler) Emit(logger lager.Logger) {
	messagesToEmit := handler.routingTable.MessagesToEmit()

	logger.Debug("emitting-messages", lager.Data{"messages": messagesToEmit})
	err := handler.emitter.Emit(messagesToEmit)
	if err != nil {
		logger.Error("failed-to-emit-routes", err)
	}

	routesSynced.Add(messagesToEmit.RouteRegistrationCount())
	err = routesTotal.Send(handler.routingTable.RouteCount())
	if err != nil {
		logger.Error("failed-to-send-routes-total-metric", err)
	}
}

func (handler *NATSHandler) Sync(
	logger lager.Logger,
	desired []*models.DesiredLRPSchedulingInfo,
	actuals []*endpoint.ActualLRPRoutingInfo,
	domains models.DomainSet,
	cachedEvents map[string]models.Event,
) {
	logger = logger.Session("nats-sync")
	logger.Debug("starting")
	defer logger.Debug("completed")

	schedInfoMap := make(map[string]*models.DesiredLRPSchedulingInfo)
	for _, schedInfo := range desired {
		schedInfoMap[schedInfo.ProcessGuid] = schedInfo
	}

	newTable := routingtable.NewTempTable(
		routingtable.RoutesByRoutingKeyFromSchedulingInfos(desired),
		routingtable.EndpointsByRoutingKeyFromActuals(actuals, schedInfoMap),
	)

	/////////

	emitter := handler.emitter
	handler.emitter = nil

	table := handler.routingTable
	handler.routingTable = newTable

	for _, event := range cachedEvents {
		handler.HandleEvent(logger, event)
	}

	handler.routingTable = table
	handler.emitter = emitter

	//////////

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
	handler.setRoutesForDesired(logger, desiredLRP)
}

func (handler *NATSHandler) handleDesiredUpdate(logger lager.Logger, before, after *models.DesiredLRPSchedulingInfo) {
	logger = logger.Session("handling-desired-update", lager.Data{
		"before": util.DesiredLRPData(before),
		"after":  util.DesiredLRPData(after),
	})
	logger.Info("starting")
	defer logger.Info("complete")

	afterKeysSet := handler.setRoutesForDesired(logger, after)

	beforeRoutingKeys := routingtable.RoutingKeysFromSchedulingInfo(before)
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
	for _, key := range routingtable.RoutingKeysFromSchedulingInfo(schedulingInfo) {
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

	routeEntries := make(map[endpoint.RoutingKey][]routingtable.Route)
	for _, route := range routes {
		key := endpoint.RoutingKey{ProcessGUID: schedulingInfo.ProcessGuid, ContainerPort: route.Port}
		routingKeySet.add(key)

		routes := []routingtable.Route{}
		for _, hostname := range route.Hostnames {
			routes = append(routes, routingtable.Route{
				Hostname:        hostname,
				LogGuid:         schedulingInfo.LogGuid,
				RouteServiceUrl: route.RouteServiceUrl,
				RouterGroupGuid: route.RouterGroupGuid,
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

func (handler *NATSHandler) emitMessages(logger lager.Logger, messagesToEmit routingtable.MessagesToEmit) {
	if handler.emitter != nil {
		logger.Debug("emit-messages", lager.Data{"messages": messagesToEmit})
		handler.emitter.Emit(messagesToEmit)
		routesRegistered.Add(messagesToEmit.RouteRegistrationCount())
		routesUnregistered.Add(messagesToEmit.RouteUnregistrationCount())
	}
}

func (handler *NATSHandler) addAndEmit(logger lager.Logger, actualLRPInfo *endpoint.ActualLRPRoutingInfo) {
	logger.Info("handler-add-and-emit", lager.Data{"net_info": actualLRPInfo.ActualLRP.ActualLRPNetInfo})
	endpoints, err := routingtable.EndpointsFromActual(actualLRPInfo)
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
	endpoints, err := routingtable.EndpointsFromActual(actualLRPInfo)
	if err != nil {
		logger.Error("failed-to-extract-endpoint-from-actual", err)
		return
	}

	for _, key := range routingtable.RoutingKeysFromActual(actualLRPInfo.ActualLRP) {
		for _, endpoint := range endpoints {
			if key.ContainerPort == endpoint.ContainerPort {
				messagesToEmit := handler.routingTable.RemoveEndpoint(key, endpoint)
				handler.emitMessages(logger, messagesToEmit)
			}
		}
	}
}
