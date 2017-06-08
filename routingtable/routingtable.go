package routingtable

import (
	"sync"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/event"
	"code.cloudfoundry.org/routing-info/cfroutes"
)

//go:generate counterfeiter -o fakeroutingtable/fake_routingtable.go . RoutingTable

type RoutingTable interface {
	AddEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (event.RoutingEvents, MessagesToEmit)
	RemoveEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (event.RoutingEvents, MessagesToEmit)
	Swap(t RoutingTable, domains models.DomainSet) (event.RoutingEvents, MessagesToEmit)
	Emit() (event.RoutingEvents, MessagesToEmit)

	// ???
	EndpointsForIndex(key endpoint.RoutingKey, index int32) []Endpoint

	// routes
	SetRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (event.RoutingEvents, MessagesToEmit)
	RemoveRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (event.RoutingEvents, MessagesToEmit)
	GetHTTPRoutes(key endpoint.RoutingKey) []Route
	GetTCPRoutes(key endpoint.RoutingKey) endpoint.ExternalEndpointInfos
	HTTPEndpointCount() int
	TCPRouteCount() int
}

type routingTable struct {
	entries        map[endpoint.RoutingKey]RoutableEndpoints
	messageBuilder MessageBuilder
	logger         lager.Logger
	sync.Locker
}

func NewRoutingTable(logger lager.Logger, messageBuilder MessageBuilder) RoutingTable {
	return &routingTable{
		entries:        make(map[endpoint.RoutingKey]RoutableEndpoints),
		logger:         logger,
		messageBuilder: messageBuilder,
		Locker:         &sync.Mutex{},
	}
}

func (table *routingTable) AddEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (event.RoutingEvents, MessagesToEmit) {
	table.logger.Info("handler-add-and-emit", lager.Data{"net_info": actualLRP.ActualLRP.ActualLRPNetInfo})
	endpoints, err := EndpointsFromActual(actualLRP)
	if err != nil {
		table.logger.Error("failed-to-extract-endpoint-from-actual", err)
		return nil, MessagesToEmit{}
	}
	var messagesToEmit MessagesToEmit
	for _, routingEndpoint := range endpoints {
		table.Lock()
		defer table.Unlock()

		key := endpoint.RoutingKey{ProcessGUID: actualLRP.ActualLRP.ProcessGuid, ContainerPort: uint32(routingEndpoint.ContainerPort)}
		currentEntry := table.entries[key]
		newEntry := currentEntry.copy()
		newEntry.Endpoints[routingEndpoint.key()] = routingEndpoint
		table.entries[key] = newEntry
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
	return nil, messagesToEmit
}
func (table *routingTable) RemoveEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (event.RoutingEvents, MessagesToEmit) {
	table.logger.Session("removing-endpoint")
	table.logger.Info("starting")
	defer table.logger.Info("complete")
	endpoints, err := EndpointsFromActual(actualLRP)
	if err != nil {
		table.logger.Error("failed-to-extract-endpoint-from-actual", err)
		return nil, MessagesToEmit{}
	}

	var messagesToEmit MessagesToEmit
	for _, routingEndpoint := range endpoints {
		key := endpoint.RoutingKey{ProcessGUID: actualLRP.ActualLRP.ProcessGuid, ContainerPort: uint32(routingEndpoint.ContainerPort)}
		table.Lock()
		defer table.Unlock()

		currentEntry := table.entries[key]
		endpointKey := routingEndpoint.key()
		currentEndpoint, ok := currentEntry.Endpoints[endpointKey]

		if !ok || (!currentEndpoint.ModificationTag.Equal(routingEndpoint.ModificationTag) && !currentEndpoint.ModificationTag.SucceededBy(routingEndpoint.ModificationTag)) {
			continue
		}

		newEntry := currentEntry.copy()
		delete(newEntry.Endpoints, endpointKey)
		table.entries[key] = newEntry

		//delete(table.addressEntries, routingEndpoint.address())

		messagesToEmit = messagesToEmit.Merge(table.emit(key, currentEntry, newEntry))
	}
	return nil, messagesToEmit
}
func (t *routingTable) Swap(other RoutingTable, domains models.DomainSet) (event.RoutingEvents, MessagesToEmit) {
	return nil, MessagesToEmit{}
}
func (t *routingTable) Emit() (event.RoutingEvents, MessagesToEmit) {
	return nil, MessagesToEmit{}
}
func (t *routingTable) EndpointsForIndex(key endpoint.RoutingKey, index int32) []Endpoint {
	return nil
}
func (table *routingTable) SetRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (event.RoutingEvents, MessagesToEmit) {
	table.Lock()
	defer table.Unlock()

	type set map[interface{}]struct{}

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
		currentEntry := table.entries[key]
		if !currentEntry.ModificationTag.SucceededBy(&desiredLRP.ModificationTag) {
			continue
		}

		newEntry := currentEntry.copy()
		newEntry.Routes = routes
		newEntry.ModificationTag = &desiredLRP.ModificationTag
		table.entries[key] = newEntry
		messagesToEmit = messagesToEmit.Merge(table.emit(key, currentEntry, newEntry))
	}

	return nil, messagesToEmit
}

func (table *routingTable) emit(key endpoint.RoutingKey, oldEntry, newEntry RoutableEndpoints) MessagesToEmit {
	var messagesToEmit MessagesToEmit
	messagesToEmit = table.messageBuilder.RegistrationsFor(&oldEntry, &newEntry)
	messagesToEmit = messagesToEmit.Merge(table.messageBuilder.UnregistrationsFor(&oldEntry, &newEntry, nil))

	return messagesToEmit
}

func (t *routingTable) RemoveRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (event.RoutingEvents, MessagesToEmit) {
	return nil, MessagesToEmit{}
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
	return 0
}
