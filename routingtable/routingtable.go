package routingtable

import (
	"sync"

	tcpmodels "code.cloudfoundry.org/routing-api/models"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/route-emitter/routingtable/util"
	"code.cloudfoundry.org/routing-info/cfroutes"
	"code.cloudfoundry.org/routing-info/tcp_routes"
)

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

//go:generate counterfeiter -o fakeroutingtable/fake_routingtable.go . RoutingTable

type RoutingTable interface {
	AddEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit)
	RemoveEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit)
	Swap(t RoutingTable, domains models.DomainSet) (TCPRouteMappings, MessagesToEmit)
	Emit() (TCPRouteMappings, MessagesToEmit)

	// ???
	EndpointsForIndex(key endpoint.RoutingKey, index int32) []Endpoint

	// routes
	SetRoutes(beforeLRP, afterLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit)
	RemoveRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit)
	// we only need the following methods to be able to tell if we need to fetch the desired lrp, can we abstract these methods ?
	// GetHTTPRoutes(key endpoint.RoutingKey) []Route
	// GetTCPRoutes(key endpoint.RoutingKey) endpoint.ExternalEndpointInfos
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
		messagesToEmit = messagesToEmit.Merge(table.emitHTP(key, currentEntry, newEntry))
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
	table.Lock()
	defer table.Unlock()

	table.logger.Session("removing-endpoint")
	table.logger.Info("starting")
	defer table.logger.Info("complete")
	endpoints, err := EndpointsFromActual(actualLRP)
	if err != nil {
		table.logger.Error("failed-to-extract-endpoint-from-actual", err)
		return TCPRouteMappings{}, MessagesToEmit{}
	}

	// remove http endpoint
	var messagesToEmit MessagesToEmit
	for _, routingEndpoint := range endpoints {
		key := endpoint.RoutingKey{
			ProcessGUID:   actualLRP.ActualLRP.ProcessGuid,
			ContainerPort: routingEndpoint.ContainerPort,
		}

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

		messagesToEmit = messagesToEmit.Merge(table.emitHTP(key, currentEntry, newEntry))
	}

	// remove tcp endpoints
	var routingEvents TCPRouteMappings
	tcpEndpoints := endpoint.NewEndpointsFromActual(actualLRP)
	for _, routingEndpoint := range tcpEndpoints {
		key := endpoint.RoutingKey{
			ProcessGUID:   actualLRP.ActualLRP.ProcessGuid,
			ContainerPort: routingEndpoint.ContainerPort,
		}
		currentEntry := table.tcpEntries[key]
		endpointKey := routingEndpoint.Key()
		currentEndpoint, ok := currentEntry.Endpoints[endpointKey]

		if !ok ||
			(!currentEndpoint.ModificationTag.Equal(routingEndpoint.ModificationTag) &&
				!currentEndpoint.ModificationTag.SucceededBy(routingEndpoint.ModificationTag)) {
			continue
		}

		newEntry := currentEntry.Copy()
		delete(newEntry.Endpoints, endpointKey)
		table.tcpEntries[key] = newEntry

		//delete(table.addressEntries, routingEndpoint.address())

		routingEvents = routingEvents.Merge(table.emitTCP(key, currentEntry, newEntry))
	}

	return routingEvents, messagesToEmit
}

func (t *routingTable) Swap(other RoutingTable, domains models.DomainSet) (TCPRouteMappings, MessagesToEmit) {
	t.Lock()
	defer t.Unlock()

	otherTable, ok := other.(*routingTable)
	if !ok {
		t.logger.Error("failed-to-convert-to-routing-table", nil)
		return TCPRouteMappings{}, MessagesToEmit{}
	}

	// http swap
	var messagesToEmit MessagesToEmit
	for key, endpoints := range otherTable.httpEntries {
		existingEntry, ok := t.httpEntries[key]
		if !ok {
			messagesToEmit = messagesToEmit.Merge(t.emitHTP(key, RoutableEndpoints{}, endpoints))
			continue
		}
		messagesToEmit = messagesToEmit.Merge(t.emitHTP(key, existingEntry, endpoints))
	}
	for key, endpoints := range t.httpEntries {
		_, ok := otherTable.httpEntries[key]
		if ok {
			continue
		}
		messagesToEmit = messagesToEmit.Merge(t.emitHTP(key, endpoints, RoutableEndpoints{}))
	}

	t.httpEntries = otherTable.httpEntries

	// tcp swap

	var routingEvents TCPRouteMappings
	for key, endpoints := range otherTable.tcpEntries {
		existingEntry, ok := t.tcpEntries[key]
		if !ok {
			routingEvents = routingEvents.Merge(t.emitTCP(key, endpoint.RoutableEndpoints{}, endpoints))
			continue
		}
		routingEvents = routingEvents.Merge(t.emitTCP(key, existingEntry, endpoints))
	}
	for key, endpoints := range t.tcpEntries {
		_, ok := otherTable.tcpEntries[key]
		if ok {
			continue
		}
		routingEvents = routingEvents.Merge(t.emitTCP(key, endpoints, endpoint.RoutableEndpoints{}))
	}

	t.tcpEntries = otherTable.tcpEntries

	return routingEvents, messagesToEmit
}

func (t *routingTable) Emit() (TCPRouteMappings, MessagesToEmit) {
	var routingEvents TCPRouteMappings
	for key, endpoints := range t.tcpEntries {
		routingEvents = routingEvents.Merge(t.emitTCP(key, endpoint.RoutableEndpoints{}, endpoints))
	}

	return routingEvents, MessagesToEmit{}
}

func (t *routingTable) EndpointsForIndex(key endpoint.RoutingKey, index int32) []Endpoint {
	return nil
}

func (table *routingTable) SetRoutes(before, after *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit) {
	table.Lock()
	defer table.Unlock()

	logger := table.logger.Session("set-routes", lager.Data{"before_lrp": util.DesiredLRPData(before), "after_lrp": util.DesiredLRPData(after)})
	logger.Info("started")
	defer logger.Info("finished")

	// update http routes
	var routes cfroutes.CFRoutes

	if after != nil {
		routes, _ = cfroutes.CFRoutesFromRoutingInfo(after.Routes)
	}

	routeEntries := make(map[endpoint.RoutingKey][]Route)
	for _, route := range routes {
		key := endpoint.RoutingKey{ProcessGUID: after.ProcessGuid, ContainerPort: route.Port}

		routes := []Route{}
		for _, hostname := range route.Hostnames {
			route := Route{
				Hostname:         hostname,
				LogGuid:          after.LogGuid,
				RouteServiceUrl:  route.RouteServiceUrl,
				IsolationSegment: route.IsolationSegment,
			}
			routes = append(routes, route)
		}
		routeEntries[key] = append(routeEntries[key], routes...)
	}

	var messagesToEmit MessagesToEmit = MessagesToEmit{}

	for key, routes := range routeEntries {
		currentEntry := table.httpEntries[key]
		if !currentEntry.ModificationTag.SucceededBy(&after.ModificationTag) {
			continue
		}

		newEntry := currentEntry.copy()
		newEntry.Routes = routes
		newEntry.ModificationTag = &after.ModificationTag
		table.httpEntries[key] = newEntry
		messagesToEmit = messagesToEmit.Merge(table.emitHTP(key, currentEntry, newEntry))
	}

	// update tcp routes

	tcpRouteEntries := make(map[endpoint.RoutingKey]endpoint.ExternalEndpointInfos)
	var tcpRoutes tcp_routes.TCPRoutes
	if after != nil {
		tcpRoutes, _ = tcp_routes.TCPRoutesFromRoutingInfo(&after.Routes)
	}
	for _, route := range tcpRoutes {
		key := endpoint.RoutingKey{ProcessGUID: after.ProcessGuid, ContainerPort: route.ContainerPort}

		tcpRouteEntries[key] = append(tcpRouteEntries[key], endpoint.ExternalEndpointInfo{
			RouterGroupGUID: route.RouterGroupGuid,
			Port:            route.ExternalPort,
		})
	}

	removedTCPRouteEntries := make(map[endpoint.RoutingKey]endpoint.ExternalEndpointInfos)
	var oldTCPRoutes tcp_routes.TCPRoutes
	if before != nil {
		oldTCPRoutes, _ = tcp_routes.TCPRoutesFromRoutingInfo(&before.Routes)
	}

	for _, route := range oldTCPRoutes {
		key := endpoint.RoutingKey{ProcessGUID: before.ProcessGuid, ContainerPort: route.ContainerPort}

		if _, ok := tcpRouteEntries[key]; ok {
			// this key exists in the new desired lrp, skip it
			continue
		}

		removedTCPRouteEntries[key] = append(tcpRouteEntries[key], endpoint.ExternalEndpointInfo{
			RouterGroupGUID: route.RouterGroupGuid,
			Port:            route.ExternalPort,
		})
	}

	routingEvents := TCPRouteMappings{}
	for key, routes := range tcpRouteEntries {
		currentEntry := table.tcpEntries[key]
		if !currentEntry.ModificationTag.SucceededBy(&after.ModificationTag) {
			continue
		}

		newEntry := currentEntry.Copy()
		newEntry.ExternalEndpoints = routes
		newEntry.ModificationTag = &after.ModificationTag
		table.tcpEntries[key] = newEntry

		routingEvents = routingEvents.Merge(table.emitTCP(key, currentEntry, newEntry))
	}

	for key := range removedTCPRouteEntries {
		currentEntry := table.tcpEntries[key]
		if !currentEntry.ModificationTag.SucceededBy(&after.ModificationTag) {
			continue
		}
		delete(table.tcpEntries, key)

		routingEvents = routingEvents.Merge(table.emitTCP(key, currentEntry, endpoint.RoutableEndpoints{}))
	}

	return routingEvents, messagesToEmit
}

func (table *routingTable) emitHTP(key endpoint.RoutingKey, oldEntry, newEntry RoutableEndpoints) MessagesToEmit {
	var messagesToEmit MessagesToEmit
	messagesToEmit = table.messageBuilder.RegistrationsFor(&oldEntry, &newEntry)
	messagesToEmit = messagesToEmit.Merge(table.messageBuilder.UnregistrationsFor(&oldEntry, &newEntry, nil))

	return messagesToEmit
}

func (table *routingTable) emitTCP(key endpoint.RoutingKey, oldEntry, newEntry endpoint.RoutableEndpoints) TCPRouteMappings {
	routesDiff := diffTCPRoutes(oldEntry.ExternalEndpoints, newEntry.ExternalEndpoints)
	endpointsDiff := diffTCPEndpoints(oldEntry.Endpoints, newEntry.Endpoints)
	return table.routingEvents(routesDiff, endpointsDiff)
}

type tcpRoutesDiff struct {
	before, after, removed, added endpoint.ExternalEndpointInfos
}

type tcpEndpointsDiff struct {
	before, after, removed, added map[endpoint.EndpointKey]endpoint.Endpoint
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

	diff := tcpRoutesDiff{
		before: before,
		after:  after,
	}
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

func diffTCPEndpoints(before, after map[endpoint.EndpointKey]endpoint.Endpoint) tcpEndpointsDiff {
	diff := tcpEndpointsDiff{
		before:  before,
		after:   after,
		removed: make(map[endpoint.EndpointKey]endpoint.Endpoint),
		added:   make(map[endpoint.EndpointKey]endpoint.Endpoint),
	}
	// generate the diff
	for key, endpoint := range before {
		newEndpoint, ok := after[key]
		if !ok {
			diff.removed[key] = endpoint
			continue
		}
		if newEndpoint != endpoint && endpoint.ModificationTag.SucceededBy(newEndpoint.ModificationTag) {
			diff.removed[key] = endpoint
		}
	}
	for key, endpoint := range after {
		oldEndpoint, ok := before[key]
		if !ok {
			diff.added[key] = endpoint
			continue
		}
		if endpoint != oldEndpoint && oldEndpoint.ModificationTag.SucceededBy(endpoint.ModificationTag) {
			diff.added[key] = endpoint
		}
	}

	return diff
}

func (table *routingTable) routingEvents(routesDiff tcpRoutesDiff, endpointDiff tcpEndpointsDiff) TCPRouteMappings {
	// remove duplicates
	unregistrations := map[endpoint.ExternalEndpointInfo]map[endpoint.Endpoint]bool{}
	registrations := map[endpoint.ExternalEndpointInfo]map[endpoint.Endpoint]bool{}

	// for removed routes remove endpoints previously registered
	for _, route := range routesDiff.removed {
		for _, container := range endpointDiff.before {
			if unregistrations[route] != nil && unregistrations[route][container] {
				continue
			}
			if unregistrations[route] == nil {
				unregistrations[route] = map[endpoint.Endpoint]bool{}
			}
			unregistrations[route][container] = true
		}
	}

	// for added routes add endpoints newly added
	for _, route := range routesDiff.added {
		for _, container := range endpointDiff.after {
			if registrations[route] != nil && registrations[route][container] {
				continue
			}
			if registrations[route] == nil {
				registrations[route] = map[endpoint.Endpoint]bool{}
			}
			registrations[route][container] = true
		}
	}

	// for removed endpoints remove routes previously registered
	for _, container := range endpointDiff.removed {
		for _, route := range routesDiff.before {
			if unregistrations[route] != nil && unregistrations[route][container] {
				continue
			}
			if unregistrations[route] == nil {
				unregistrations[route] = map[endpoint.Endpoint]bool{}
			}
			unregistrations[route][container] = true
		}
	}

	// for added endpoints register routes that were just added
	for _, container := range endpointDiff.added {
		for _, route := range routesDiff.after {
			if registrations[route] != nil && registrations[route][container] {
				continue
			}
			if registrations[route] == nil {
				registrations[route] = map[endpoint.Endpoint]bool{}
			}
			registrations[route][container] = true
		}
	}

	events := TCPRouteMappings{}
	for r, es := range registrations {
		for e := range es {
			events.Registrations = append(events.Registrations, tcpmodels.NewTcpRouteMapping(
				r.RouterGroupGUID,
				uint16(r.Port),
				e.Host,
				uint16(e.Port),
				table.tcpTTL,
			))
		}
	}
	for r, es := range unregistrations {
		for e := range es {
			events.Unregistrations = append(events.Unregistrations, tcpmodels.NewTcpRouteMapping(
				r.RouterGroupGUID,
				uint16(r.Port),
				e.Host,
				uint16(e.Port),
				table.tcpTTL,
			))
		}
	}
	return events
}

func (table *routingTable) RemoveRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit) {
	logger := table.logger.Session("RemoveRoutes", lager.Data{"desired_lrp": util.DesiredLRPData(desiredLRP)})
	logger.Debug("starting")
	defer logger.Debug("completed")
	after := *desiredLRP
	after.Routes = nil
	return table.SetRoutes(desiredLRP, &after)
}

func (t *routingTable) GetHTTPRoutes(key endpoint.RoutingKey) []Route {
	return nil
}

func (t *routingTable) GetTCPRoutes(key endpoint.RoutingKey) endpoint.ExternalEndpointInfos {
	return nil
}

func (t *routingTable) HTTPEndpointCount() int {
	t.Lock()
	defer t.Unlock()

	count := 0
	for _, entry := range t.httpEntries {
		count += len(entry.Routes) * len(entry.Endpoints)
	}

	return count
}

func (t *routingTable) TCPRouteCount() int {
	return len(t.tcpEntries)
}
