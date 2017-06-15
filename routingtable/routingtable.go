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
	// table modification
	SetRoutes(beforeLRP, afterLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit)
	RemoveRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit)
	AddEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit)
	RemoveEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit)
	Swap(t RoutingTable, domains models.DomainSet) (TCPRouteMappings, MessagesToEmit)
	Emit() (TCPRouteMappings, MessagesToEmit)

	// TODO: this is only used to delete extra instances when the lrp is scaled down. not sure why ? ask #routing
	// EndpointsForIndex(key endpoint.RoutingKey, index int32) []Endpoint

	// routes

	HasExternalRoutes(actual *endpoint.ActualLRPRoutingInfo) bool
	HTTPEndpointCount() int
	TCPRouteCount() int
}

type routingTable struct {
	httpEntries   map[endpoint.RoutingKey]RoutableEndpoints
	tcpEntries    map[endpoint.RoutingKey]endpoint.RoutableEndpoints
	tcpGenerator  func(endpoint.ExternalEndpointInfo, endpoint.Endpoint) tcpmodels.TcpRouteMapping
	httpGenerator func(endpoint Endpoint, route Route) RegistryMessage
	logger        lager.Logger
	sync.Locker
}

func NewRoutingTable(logger lager.Logger, directInstanceRoute bool) RoutingTable {
	tcpGenerator := func(r endpoint.ExternalEndpointInfo, e endpoint.Endpoint) tcpmodels.TcpRouteMapping {
		return tcpmodels.NewTcpRouteMapping(
			r.RouterGroupGUID,
			uint16(r.Port),
			e.Host,
			uint16(e.Port),
			0,
		)
	}

	httpGenerator := RegistryMessageFor

	if directInstanceRoute {
		tcpGenerator = func(r endpoint.ExternalEndpointInfo, e endpoint.Endpoint) tcpmodels.TcpRouteMapping {
			return tcpmodels.NewTcpRouteMapping(
				r.RouterGroupGUID,
				uint16(r.Port),
				e.ContainerIP,
				uint16(e.ContainerPort),
				0,
			)
		}
		httpGenerator = InternalAddressRegistryMessageFor
	}

	return &routingTable{
		httpEntries:   make(map[endpoint.RoutingKey]RoutableEndpoints),
		tcpEntries:    make(map[endpoint.RoutingKey]endpoint.RoutableEndpoints),
		tcpGenerator:  tcpGenerator,
		httpGenerator: httpGenerator,
		logger:        logger,
		Locker:        &sync.Mutex{},
	}
}

func (table *routingTable) AddEndpoint(actualLRP *endpoint.ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit) {
	logger := table.logger.Session("AddEndpoint", lager.Data{"actual_lrp": actualLRP})
	logger.Debug("starting")
	defer logger.Debug("completed")

	table.Lock()
	defer table.Unlock()

	logger.Info("handler-add-and-emit", lager.Data{"net_info": actualLRP.ActualLRP.ActualLRPNetInfo})
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
		currentEndpoint, ok := currentEntry.Endpoints[routingEndpoint.key()]
		if ok && !currentEndpoint.ModificationTag.SucceededBy(routingEndpoint.ModificationTag) {
			continue
		}
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
		messagesToEmit = messagesToEmit.Merge(table.emitHTTP(key, currentEntry, newEntry))
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
	logger := table.logger.Session("RemoveEndpoint", lager.Data{"actual_lrp": actualLRP})
	logger.Debug("starting")
	defer logger.Debug("completed")

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

		messagesToEmit = messagesToEmit.Merge(table.emitHTTP(key, currentEntry, newEntry))
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
			// entry doesn't exist in current table, emit registration event
			messagesToEmit = messagesToEmit.Merge(t.emitHTTP(key, RoutableEndpoints{}, endpoints))
			continue
		}
		// entry exists in both tables, merge the two entries to ensure non-fresh domain endpoints aren't removed
		merged := mergeHTTP(existingEntry, endpoints, domains)
		otherTable.httpEntries[key] = merged
		messagesToEmit = messagesToEmit.Merge(t.emitHTTP(key, existingEntry, merged))
	}
	for key, endpoints := range t.httpEntries {
		if _, ok := otherTable.httpEntries[key]; ok {
			continue
		}
		messagesToEmit = messagesToEmit.Merge(t.emitHTTP(key, endpoints, RoutableEndpoints{}))
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

// merge the routes from both endpoints, ensuring that non-fresh routes aren't removed
func mergeHTTP(before, after RoutableEndpoints, domains models.DomainSet) RoutableEndpoints {
	merged := after.copy()

	for _, endpoint := range before.Endpoints {
		// if you are wondering why we have to loop through the endpoints instead
		// of checking the routable endpoint information, that's because the sync
		// loop gets the scheduling info which doesn't include the domain. ideally,
		// the routable endpoint should have the domain.
		if !domains.Contains(endpoint.Domain) {
			// non-fresh domain, append routes from older endpoint
			for _, oldRoute := range before.Routes {
				routeExistInNewLRP := func() bool {
					for _, newRoute := range after.Routes {
						if newRoute == oldRoute {
							return true
						}
					}
					return false
				}
				if !routeExistInNewLRP() {
					merged.Routes = append(merged.Routes, oldRoute)
				}
			}
		}
	}
	return merged
}

func (t *routingTable) Emit() (TCPRouteMappings, MessagesToEmit) {
	// http routes
	var messagesToEmit MessagesToEmit
	for key, route := range t.httpEntries {
		messagesToEmit = messagesToEmit.Merge(t.emitHTTP(key, RoutableEndpoints{}, route))
	}
	//tcp routes
	var routingEvents TCPRouteMappings
	for key, endpoints := range t.tcpEntries {
		routingEvents = routingEvents.Merge(t.emitTCP(key, endpoint.RoutableEndpoints{}, endpoints))
	}

	return routingEvents, messagesToEmit
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

	removedRouteEntries := make(map[endpoint.RoutingKey][]Route)
	var oldHTTPRoutes cfroutes.CFRoutes
	if before != nil {
		oldHTTPRoutes, _ = cfroutes.CFRoutesFromRoutingInfo(before.Routes)
	}

	for _, route := range oldHTTPRoutes {
		key := endpoint.RoutingKey{ProcessGUID: before.ProcessGuid, ContainerPort: route.Port}

		if _, ok := routeEntries[key]; ok {
			// this key exists in the new desired lrp, skip it
			continue
		}

		routes := []Route{}
		for _, hostname := range route.Hostnames {
			route := Route{
				Hostname:         hostname,
				LogGuid:          before.LogGuid,
				RouteServiceUrl:  route.RouteServiceUrl,
				IsolationSegment: route.IsolationSegment,
			}
			routes = append(routes, route)
		}
		removedRouteEntries[key] = append(removedRouteEntries[key], routes...)
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
		messagesToEmit = messagesToEmit.Merge(table.emitHTTP(key, currentEntry, newEntry))
	}

	for key := range removedRouteEntries {
		currentEntry := table.httpEntries[key]
		if !(currentEntry.ModificationTag.Equal(&after.ModificationTag) ||
			currentEntry.ModificationTag.SucceededBy(&after.ModificationTag)) {
			continue
		}
		delete(table.httpEntries, key)

		messagesToEmit = messagesToEmit.Merge(table.emitHTTP(key, currentEntry, RoutableEndpoints{}))
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

func (table *routingTable) emitHTTP(key endpoint.RoutingKey, oldEntry, newEntry RoutableEndpoints) MessagesToEmit {
	routesDiff := diffHTTPRoutes(oldEntry.Routes, newEntry.Routes)
	endpointsDiff := diffHTTPEndpoints(oldEntry.Endpoints, newEntry.Endpoints)
	return table.httpNatsMessages(routesDiff, endpointsDiff)
}

type httpRoutesDiff struct {
	before, after, removed, added []Route
}

type httpEndpointsDiff struct {
	before, after, removed, added map[EndpointKey]Endpoint
}

func diffHTTPRoutes(before, after []Route) httpRoutesDiff {
	existingRoutes := map[Route]struct{}{}
	newRoutes := map[Route]struct{}{}
	newHostnames := map[string]struct{}{}
	for _, route := range before {
		existingRoutes[route] = struct{}{}
	}
	for _, route := range after {
		newRoutes[route] = struct{}{}
		newHostnames[route.Hostname] = struct{}{}
	}

	diff := httpRoutesDiff{
		before: before,
		after:  after,
	}
	// generate the diff
	for route := range existingRoutes {
		if _, ok := newRoutes[route]; !ok {
			// routes that changes the service url but don't change the hostname
			// shouldn't be considered removed. we still add them to the added routes
			// in order to update the Registration. TODO: can we send an
			// unregistration+registration instead and get rid of this code
			if _, ok := newHostnames[route.Hostname]; !ok {
				diff.removed = append(diff.removed, route)
			}
		}
	}

	for route := range newRoutes {
		if _, ok := existingRoutes[route]; !ok {
			diff.added = append(diff.added, route)
		}
	}

	return diff
}

// http endpoints are different if any field is different other than the following:
// 1. ModificationTag
// 2. Evacuating
func httpEndpointDifferent(before, after Endpoint) bool {
	modificationTag := before.ModificationTag
	evacuating := before.Evacuating
	before.ModificationTag = after.ModificationTag
	before.Evacuating = after.Evacuating
	defer func() {
		before.ModificationTag = modificationTag
		before.Evacuating = evacuating
	}()
	return before != after
}

func diffHTTPEndpoints(before, after map[EndpointKey]Endpoint) httpEndpointsDiff {
	diff := httpEndpointsDiff{
		before:  before,
		after:   after,
		removed: make(map[EndpointKey]Endpoint),
		added:   make(map[EndpointKey]Endpoint),
	}
	// generate the diff
	for key, endpoint := range before {
		newEndpoint, ok := after[key]
		if !ok {
			key.Evacuating = !key.Evacuating
			newEndpoint, ok = after[key]
		}
		// TODO: make sure tcp diff follow the same pattern
		if !ok || httpEndpointDifferent(newEndpoint, endpoint) {
			diff.removed[key] = endpoint
		}
	}
	for key, endpoint := range after {
		oldEndpoint, ok := before[key]
		if !ok {
			key.Evacuating = !key.Evacuating
			oldEndpoint, ok = before[key]
		}
		if !ok || httpEndpointDifferent(endpoint, oldEndpoint) {
			diff.added[key] = endpoint
		}
	}

	return diff
}

func (table *routingTable) httpNatsMessages(routesDiff httpRoutesDiff, endpointDiff httpEndpointsDiff) MessagesToEmit {
	// remove duplicates
	unregistrations := map[Route]map[Endpoint]bool{}
	registrations := map[Route]map[Endpoint]bool{}

	// for removed routes remove endpoints previously registered
	for _, route := range routesDiff.removed {
		for _, container := range endpointDiff.before {
			if unregistrations[route] != nil && unregistrations[route][container] {
				continue
			}
			if unregistrations[route] == nil {
				unregistrations[route] = map[Endpoint]bool{}
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
				registrations[route] = map[Endpoint]bool{}
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
				unregistrations[route] = map[Endpoint]bool{}
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
				registrations[route] = map[Endpoint]bool{}
			}
			registrations[route][container] = true
		}
	}

	events := MessagesToEmit{}
	for r, es := range registrations {
		for e := range es {
			events.RegistrationMessages = append(events.RegistrationMessages, table.httpGenerator(e, r))
		}
	}
	for r, es := range unregistrations {
		for e := range es {
			events.UnregistrationMessages = append(events.UnregistrationMessages, table.httpGenerator(e, r))
		}
	}
	return events
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
			events.Registrations = append(events.Registrations, table.tcpGenerator(r, e))
		}
	}
	for r, es := range unregistrations {
		for e := range es {
			events.Unregistrations = append(events.Unregistrations, table.tcpGenerator(r, e))
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

func (t *routingTable) HasExternalRoutes(actual *endpoint.ActualLRPRoutingInfo) bool {
	for _, key := range endpoint.NewRoutingKeysFromActual(actual) {
		if len(t.httpEntries[key].Routes) > 0 {
			return true
		}

		if len(t.tcpEntries[key].ExternalEndpoints) > 0 {
			return true
		}
	}

	return false
}
