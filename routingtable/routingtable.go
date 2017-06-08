package routingtable

import (
	"sync"

	tcpmodels "code.cloudfoundry.org/routing-api/models"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/routing-info/cfroutes"
	"code.cloudfoundry.org/routing-info/tcp_routes"
)

//go:generate counterfeiter -o fakeroutingtable/fake_routingtable.go . RoutingTable

type TCPRouteMappings struct {
	Registrations   []tcpmodels.TcpRouteMapping
	Unregistrations []tcpmodels.TcpRouteMapping
}

func (mappings TCPRouteMappings) Merge(other TCPRouteMappings) TCPRouteMappings {
	var result TCPRouteMappings
	result.Registrations = append(mappings.Registrations, other.Registrations...)
	result.Unregistrations = append(mappings.Unregistrations, other.Unregistrations...)
	return result
}

type RoutingTable interface {
	AddEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit)
	RemoveEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit)
	Swap(t RoutingTable, domains models.DomainSet) (TCPRouteMappings, MessagesToEmit)
	Emit() (TCPRouteMappings, MessagesToEmit)

	// ???
	EndpointsForIndex(key endpoint.RoutingKey, index int32) []Endpoint

	// routes
	SetRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit)
	RemoveRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit)
	GetHTTPRoutes(key endpoint.RoutingKey) []Route
	GetTCPRoutes(key endpoint.RoutingKey) endpoint.ExternalEndpointInfos
	HTTPEndpointCount() int
	TCPRouteCount() int
}

type routingTable struct {
	httpEntries    map[endpoint.RoutingKey]RoutableEndpoints
	tcpEntries     map[endpoint.RoutingKey]endpoint.RoutableEndpoints
	messageBuilder MessageBuilder
	logger         lager.Logger
	tcpTTL         int
	sync.Locker
}

func NewRoutingTable(logger lager.Logger, messageBuilder MessageBuilder) RoutingTable {
	return &routingTable{
		httpEntries:    make(map[endpoint.RoutingKey]RoutableEndpoints),
		tcpEntries:     make(map[endpoint.RoutingKey]endpoint.RoutableEndpoints),
		logger:         logger,
		messageBuilder: messageBuilder,
		Locker:         &sync.Mutex{},
	}
}

func (table *routingTable) AddEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit) {
	table.Lock()
	defer table.Unlock()

	table.logger.Info("handler-add-and-emit", lager.Data{"net_info": actualLRP.ActualLRP.ActualLRPNetInfo})
	endpoints, err := EndpointsFromActual(actualLRP)
	if err != nil {
		table.logger.Error("failed-to-extract-endpoint-from-actual", err)
		return TCPRouteMappings{}, MessagesToEmit{}
	}

	// add http endpoints

	var messagesToEmit MessagesToEmit
	for _, routingEndpoint := range endpoints {
		key := endpoint.RoutingKey{
			ProcessGUID:   actualLRP.ActualLRP.ProcessGuid,
			ContainerPort: routingEndpoint.ContainerPort,
		}
		currentEntry := table.httpEntries[key]
		newEntry := currentEntry.copy()
		newEntry.Endpoints[routingEndpoint.key()] = routingEndpoint
		table.httpEntries[key] = newEntry
		// address := routingEndpoint.address()

		// if existingEndpointKey, ok := table.addressEntries[address]; ok {
		// 	if existingEndpointKey.InstanceGuid != routingEndpoint.InstanceGuid {
		// 		addressCollisions.Add(1)
		// 		existingInstanceGuid := existingEndpointKey.InstanceGuid
		// 		table.logger.Info("collision-detected-with-endpoint", lager.Data{
		// 			"instance_guid_a": existingInstanceGuid,
		// 			"instance_guid_b": routingEndpoint.InstanceGuid,
		// 			"Address":         routingEndpoint.address(),
		// 		})
		// 	}
		// }

		// table.addressEntries[address] = routingEndpoint.key()
		messagesToEmit = messagesToEmit.Merge(table.emit(key, currentEntry, newEntry))
	}

	// add tcp endpoints

	var routingEvents TCPRouteMappings

	tcpEndpoints := endpoint.NewEndpointsFromActual(actualLRP)
	for _, routingEndpoint := range tcpEndpoints {
		key := endpoint.RoutingKey{
			ProcessGUID:   actualLRP.ActualLRP.ProcessGuid,
			ContainerPort: routingEndpoint.ContainerPort,
		}
		currentEntry := table.tcpEntries[key]
		newEntry := currentEntry.Copy()
		newEntry.Endpoints[routingEndpoint.Key()] = routingEndpoint
		table.tcpEntries[key] = newEntry
		routingEvents = routingEvents.Merge(table.emitTCP(key, currentEntry, newEntry))
	}

	return routingEvents, messagesToEmit
}

func (table *routingTable) RemoveEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit) {
	table.logger.Session("removing-endpoint")
	table.logger.Info("starting")
	defer table.logger.Info("complete")
	endpoints, err := EndpointsFromActual(actualLRP)
	if err != nil {
		table.logger.Error("failed-to-extract-endpoint-from-actual", err)
		return TCPRouteMappings{}, MessagesToEmit{}
	}

	var messagesToEmit MessagesToEmit
	for _, routingEndpoint := range endpoints {
		key := endpoint.RoutingKey{
			ProcessGUID:   actualLRP.ActualLRP.ProcessGuid,
			ContainerPort: routingEndpoint.ContainerPort,
		}
		table.Lock()
		defer table.Unlock()

		currentEntry := table.httpEntries[key]
		endpointKey := routingEndpoint.key()
		currentEndpoint, ok := currentEntry.Endpoints[endpointKey]

		if !ok ||
			(!currentEndpoint.ModificationTag.Equal(routingEndpoint.ModificationTag) &&
				!currentEndpoint.ModificationTag.SucceededBy(routingEndpoint.ModificationTag)) {
			continue
		}

		newEntry := currentEntry.copy()
		delete(newEntry.Endpoints, endpointKey)
		table.httpEntries[key] = newEntry

		//delete(table.addressEntries, routingEndpoint.address())

		messagesToEmit = messagesToEmit.Merge(table.emit(key, currentEntry, newEntry))
	}
	return TCPRouteMappings{}, messagesToEmit
}

func (t *routingTable) Swap(other RoutingTable, domains models.DomainSet) (TCPRouteMappings, MessagesToEmit) {
	return TCPRouteMappings{}, MessagesToEmit{}
}

func (t *routingTable) Emit() (TCPRouteMappings, MessagesToEmit) {
	return TCPRouteMappings{}, MessagesToEmit{}
}

func (t *routingTable) EndpointsForIndex(key endpoint.RoutingKey, index int32) []Endpoint {
	return nil
}

func (table *routingTable) SetRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit) {
	table.Lock()
	defer table.Unlock()

	// update http routes
	routes, _ := cfroutes.CFRoutesFromRoutingInfo(desiredLRP.Routes)

	routeEntries := make(map[endpoint.RoutingKey][]Route)
	for _, route := range routes {
		key := endpoint.RoutingKey{ProcessGUID: desiredLRP.ProcessGuid, ContainerPort: route.Port}

		routes := []Route{}
		for _, hostname := range route.Hostnames {
			route := Route{
				Hostname:         hostname,
				LogGuid:          desiredLRP.LogGuid,
				RouteServiceUrl:  route.RouteServiceUrl,
				IsolationSegment: route.IsolationSegment,
			}
			routes = append(routes, route)
		}
		routeEntries[key] = append(routeEntries[key], routes...)
	}

	var messagesToEmit MessagesToEmit

	for key, routes := range routeEntries {
		currentEntry := table.httpEntries[key]
		if !currentEntry.ModificationTag.SucceededBy(&desiredLRP.ModificationTag) {
			continue
		}

		newEntry := currentEntry.copy()
		newEntry.Routes = routes
		newEntry.ModificationTag = &desiredLRP.ModificationTag
		table.httpEntries[key] = newEntry
		messagesToEmit = messagesToEmit.Merge(table.emit(key, currentEntry, newEntry))
	}

	// update tcp routes

	tcpRoutes, _ := tcp_routes.TCPRoutesFromRoutingInfo(&desiredLRP.Routes)
	tcpRouteEntries := make(map[endpoint.RoutingKey]endpoint.ExternalEndpointInfos)
	for _, route := range tcpRoutes {
		key := endpoint.RoutingKey{ProcessGUID: desiredLRP.ProcessGuid, ContainerPort: route.ContainerPort}

		tcpRouteEntries[key] = append(tcpRouteEntries[key], endpoint.ExternalEndpointInfo{
			RouterGroupGUID: route.RouterGroupGuid,
			Port:            route.ExternalPort,
		})
	}

	routingEvents := TCPRouteMappings{}
	for key, routes := range tcpRouteEntries {
		currentEntry := table.tcpEntries[key]
		if !currentEntry.ModificationTag.SucceededBy(&desiredLRP.ModificationTag) {
			continue
		}

		newEntry := currentEntry.Copy()
		newEntry.ExternalEndpoints = routes
		newEntry.ModificationTag = &desiredLRP.ModificationTag
		table.tcpEntries[key] = newEntry

		routingEvents = routingEvents.Merge(table.emitTCP(key, currentEntry, newEntry))
	}

	return routingEvents, messagesToEmit
}

func (table *routingTable) emit(key endpoint.RoutingKey, oldEntry, newEntry RoutableEndpoints) MessagesToEmit {
	var messagesToEmit MessagesToEmit
	messagesToEmit = table.messageBuilder.RegistrationsFor(&oldEntry, &newEntry)
	messagesToEmit = messagesToEmit.Merge(table.messageBuilder.UnregistrationsFor(&oldEntry, &newEntry, nil))

	return messagesToEmit
}

func (table *routingTable) emitTCP(key endpoint.RoutingKey, oldEntry, newEntry endpoint.RoutableEndpoints) TCPRouteMappings {
	diff := diffTCPRoutes(oldEntry.ExternalEndpoints, newEntry.ExternalEndpoints)
	return table.routingEvents(diff, oldEntry, newEntry)
}

type tcpRoutesDiff struct {
	removed, added endpoint.ExternalEndpointInfos
}

func diffTCPRoutes(before, after endpoint.ExternalEndpointInfos) tcpRoutesDiff {
	existingRoutes := map[endpoint.ExternalEndpointInfo]struct{}{}
	newRoutes := map[endpoint.ExternalEndpointInfo]struct{}{}
	for _, route := range before {
		existingRoutes[route] = struct{}{}
	}
	for _, route := range after {
		newRoutes[route] = struct{}{}
	}

	diff := tcpRoutesDiff{}
	// generate the diff
	for route := range existingRoutes {
		if _, ok := newRoutes[route]; !ok {
			diff.removed = append(diff.removed, route)
		}
	}
	for route := range newRoutes {
		if _, ok := existingRoutes[route]; !ok {
			diff.added = append(diff.added, route)
		}
	}
	return diff
}

func (table *routingTable) routingEvents(diff tcpRoutesDiff, before, after endpoint.RoutableEndpoints) TCPRouteMappings {
	events := TCPRouteMappings{}

	for _, route := range diff.removed {
		for _, endpoint := range before.Endpoints {
			events.Unregistrations = append(events.Unregistrations, tcpmodels.NewTcpRouteMapping(
				route.RouterGroupGUID,
				uint16(route.Port),
				endpoint.Host,
				uint16(endpoint.Port),
				table.tcpTTL,
			))
		}
	}

	for _, route := range diff.added {
		for _, endpoint := range after.Endpoints {
			events.Registrations = append(events.Registrations, tcpmodels.NewTcpRouteMapping(
				route.RouterGroupGUID,
				uint16(route.Port),
				endpoint.Host,
				uint16(endpoint.Port),
				table.tcpTTL,
			))
		}
	}

	return events
}

func (t *routingTable) RemoveRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit) {
	return TCPRouteMappings{}, MessagesToEmit{}
}
func (t *routingTable) GetHTTPRoutes(key endpoint.RoutingKey) []Route {
	return nil
}
func (t *routingTable) GetTCPRoutes(key endpoint.RoutingKey) endpoint.ExternalEndpointInfos {
	return nil
}
func (t *routingTable) HTTPEndpointCount() int {
	return 0
}
func (t *routingTable) TCPRouteCount() int {
	return len(t.tcpEntries)
}
