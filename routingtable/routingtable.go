package routingtable

import (
	"sync"

	tcpmodels "code.cloudfoundry.org/routing-api/models"

	"code.cloudfoundry.org/bbs/models"
	loggingclient "code.cloudfoundry.org/diego-logging-client"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/routing-info/cfroutes"
	"code.cloudfoundry.org/routing-info/internalroutes"
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

const addressCollisions = "AddressCollisions"

//go:generate counterfeiter -o fakeroutingtable/fake_routingtable.go . RoutingTable

type RoutingTable interface {
	// table modification
	SetRoutes(beforeLRP, afterLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit)
	RemoveRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit)
	AddEndpoint(actualLRP *ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit)
	RemoveEndpoint(actualLRP *ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit)
	Swap(t RoutingTable, domains models.DomainSet) (TCPRouteMappings, MessagesToEmit)
	GetRoutingEvents() (TCPRouteMappings, MessagesToEmit)

	// routes

	HasExternalRoutes(actual *ActualLRPRoutingInfo) bool
	HTTPAssociationsCount() int     // return number of associations desired-lrp-http-routes * actual-lrps
	InternalAssociationsCount() int // return number of associations desired-lrp-internal-routes * 2 * actual-lrps
	TCPAssociationsCount() int      // return number of associations desired-lrp-tcp-routes * actual-lrps
	TableSize() int
}

type routingTable struct {
	internalEntries     map[RoutingKey]RoutableEndpoints
	entries             map[RoutingKey]RoutableEndpoints
	addressEntries      map[Address]EndpointKey
	addressGenerator    func(endpoint Endpoint) Address
	directInstanceRoute bool
	logger              lager.Logger
	metronClient        loggingclient.IngressClient
	sync.Locker
}

func NewRoutingTable(logger lager.Logger, directInstanceRoute bool, metronClient loggingclient.IngressClient) RoutingTable {
	addressGenerator := func(endpoint Endpoint) Address {
		return Address{Host: endpoint.Host, Port: endpoint.Port}
	}

	if directInstanceRoute {
		addressGenerator = func(endpoint Endpoint) Address {
			return Address{Host: endpoint.ContainerIP, Port: endpoint.ContainerPort}
		}
	}

	return &routingTable{
		internalEntries:     make(map[RoutingKey]RoutableEndpoints),
		entries:             make(map[RoutingKey]RoutableEndpoints),
		addressEntries:      make(map[Address]EndpointKey),
		directInstanceRoute: directInstanceRoute,
		addressGenerator:    addressGenerator,
		logger:              logger,
		metronClient:        metronClient,
		Locker:              &sync.Mutex{},
	}
}

func (table *routingTable) AddEndpoint(actualLRP *ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit) {
	logger := table.logger.Session("AddEndpoint", lager.Data{"actual_lrp": actualLRP})
	logger.Debug("starting")
	defer logger.Debug("completed")

	table.Lock()
	defer table.Unlock()

	logger.Info("handler-add-and-emit", lager.Data{"net_info": actualLRP.ActualLRP.ActualLRPNetInfo})
	endpoints := NewEndpointsFromActual(actualLRP)

	// collision detection

	for _, endpoint := range endpoints {
		address := table.addressGenerator(endpoint)
		// if the address exists and the instance guid doesn't match then we have a collision
		if existingEndpointKey, ok := table.addressEntries[address]; ok && existingEndpointKey.InstanceGUID != endpoint.InstanceGUID {
			table.metronClient.IncrementCounter(addressCollisions)
			existingInstanceGuid := existingEndpointKey.InstanceGUID
			table.logger.Info("collision-detected-with-endpoint", lager.Data{
				"instance_guid_a": existingInstanceGuid,
				"instance_guid_b": endpoint.InstanceGUID,
				"Address":         address,
			})
		}

		table.addressEntries[address] = endpoint.key()
	}

	// add endpoints

	var messagesToEmit MessagesToEmit
	var mappings TCPRouteMappings

	for _, routingEndpoint := range endpoints {
		key := RoutingKey{
			ProcessGUID:   actualLRP.ActualLRP.ProcessGuid,
			ContainerPort: routingEndpoint.ContainerPort,
		}
		internalKey := RoutingKey{
			ProcessGUID: actualLRP.ActualLRP.ProcessGuid,
		}
		currentEntry := table.entries[key]
		currentInternalEntry := table.internalEntries[internalKey]
		// Since desiredLRP is same, only need to check one entry
		if currentEntry.DesiredInstances > 0 && routingEndpoint.Index >= currentEntry.DesiredInstances {
			logger.Debug("skipping-undesired-instance")
			continue
		}
		currentEndpoint, ok := currentEntry.Endpoints[routingEndpoint.key()]
		if ok && !currentEndpoint.ModificationTag.SucceededBy(routingEndpoint.ModificationTag) {
			continue
		}
		newEntry := currentEntry.copy()
		newEntry.Endpoints[routingEndpoint.key()] = routingEndpoint
		table.entries[key] = newEntry
		mapping, message := table.emitDiffMessages(key, currentEntry, newEntry)
		mappings = mappings.Merge(mapping)
		messagesToEmit = messagesToEmit.Merge(message)

		newInternalEntry := currentInternalEntry.copy()
		//Internal Endpoints do not need container or host ports
		routingEndpoint.ContainerPort = 0
		routingEndpoint.Port = 0
		newInternalEntry.Endpoints[routingEndpoint.key()] = routingEndpoint
		table.internalEntries[internalKey] = newInternalEntry
		_, message = table.emitDiffMessages(internalKey, currentInternalEntry, newInternalEntry)
		messagesToEmit = messagesToEmit.Merge(message)
	}

	return mappings, messagesToEmit
}

func (table *routingTable) RemoveEndpoint(actualLRP *ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit) {
	logger := table.logger.Session("RemoveEndpoint", lager.Data{"actual_lrp": actualLRP})
	logger.Debug("starting")
	defer logger.Debug("completed")

	table.Lock()
	defer table.Unlock()

	table.logger.Session("removing-endpoint")
	table.logger.Info("starting")
	defer table.logger.Info("complete")
	endpoints := NewEndpointsFromActual(actualLRP)

	// remove address
	for _, endpoint := range endpoints {
		address := table.addressGenerator(endpoint)
		currentEntry, ok := table.addressEntries[address]
		if ok && currentEntry.InstanceGUID != endpoint.InstanceGUID {
			table.logger.Info("collision-detected-with-endpoint", lager.Data{
				"instance_guid_a": currentEntry.InstanceGUID,
				"instance_guid_b": endpoint.InstanceGUID,
				"Address":         address,
			})
			continue
		}
		delete(table.addressEntries, address)
	}

	// remove endpoint
	var messagesToEmit MessagesToEmit
	var mappings TCPRouteMappings
	for _, routingEndpoint := range endpoints {
		key := RoutingKey{
			ProcessGUID:   actualLRP.ActualLRP.ProcessGuid,
			ContainerPort: routingEndpoint.ContainerPort,
		}

		internalKey := RoutingKey{
			ProcessGUID: actualLRP.ActualLRP.ProcessGuid,
		}

		currentEntry := table.entries[key]
		endpointKey := routingEndpoint.key()
		currentEndpoint, ok := currentEntry.Endpoints[endpointKey]

		if !ok ||
			(!currentEndpoint.ModificationTag.Equal(routingEndpoint.ModificationTag) &&
				!currentEndpoint.ModificationTag.SucceededBy(routingEndpoint.ModificationTag)) {
			continue
		}

		currentInternalEntry := table.internalEntries[internalKey]
		currentEndpoint, ok = currentInternalEntry.Endpoints[endpointKey]

		if !ok ||
			(!currentEndpoint.ModificationTag.Equal(routingEndpoint.ModificationTag) &&
				!currentEndpoint.ModificationTag.SucceededBy(routingEndpoint.ModificationTag)) {
			continue
		}

		newEntry := currentEntry.copy()
		delete(newEntry.Endpoints, endpointKey)

		table.entries[key] = newEntry
		table.deleteEntryIfEmpty(key)

		mapping, message := table.emitDiffMessages(key, currentEntry, newEntry)
		messagesToEmit = messagesToEmit.Merge(message)
		mappings = mappings.Merge(mapping)

		newInternalEntry := currentInternalEntry.copy()
		delete(newInternalEntry.Endpoints, endpointKey)

		table.internalEntries[internalKey] = newInternalEntry
		table.deleteInternalEntryIfEmpty(internalKey)

		_, message = table.emitDiffMessages(internalKey, currentInternalEntry, newInternalEntry)
		messagesToEmit = messagesToEmit.Merge(message)
	}

	return mappings, messagesToEmit
}

func (t *routingTable) Swap(other RoutingTable, domains models.DomainSet) (TCPRouteMappings, MessagesToEmit) {
	logger := t.logger.Session("swap", lager.Data{"received-domains": domains})
	logger.Info("started")
	defer logger.Info("finished")

	t.Lock()
	defer t.Unlock()

	otherTable, ok := other.(*routingTable)
	if !ok {
		logger.Error("failed-to-convert-to-routing-table", nil)
		return TCPRouteMappings{}, MessagesToEmit{}
	}

	t.addressEntries = otherTable.addressEntries

	var messagesToEmit MessagesToEmit
	var mappings TCPRouteMappings

	mergedRoutingKeys := map[RoutingKey]struct{}{}
	for key, _ := range otherTable.entries {
		mergedRoutingKeys[key] = struct{}{}
	}
	for key, _ := range t.entries {
		mergedRoutingKeys[key] = struct{}{}
	}

	for key := range mergedRoutingKeys {
		existingEntry, ok := t.entries[key]
		newEntry := otherTable.entries[key]
		if !ok {
			// routing key only exist in the new table
			mapping, message := t.emitDiffMessages(key, RoutableEndpoints{}, newEntry)
			messagesToEmit = messagesToEmit.Merge(message)
			mappings = mappings.Merge(mapping)
			continue
		}

		// entry exists in both tables or in old table, merge the two entries to ensure non-fresh domain endpoints aren't removed
		merged := mergeUnfreshRoutes(existingEntry, newEntry, domains)
		otherTable.entries[key] = merged
		otherTable.deleteEntryIfEmpty(key)
		mapping, message := t.emitDiffMessages(key, existingEntry, merged)
		messagesToEmit = messagesToEmit.Merge(message)
		mappings = mappings.Merge(mapping)
	}

	mergedInternalRoutingKeys := map[RoutingKey]struct{}{}
	for key, _ := range otherTable.internalEntries {
		mergedInternalRoutingKeys[key] = struct{}{}
	}
	for key, _ := range t.internalEntries {
		mergedInternalRoutingKeys[key] = struct{}{}
	}
	for internalKey := range mergedInternalRoutingKeys {
		existingInternalEntry, ok := t.internalEntries[internalKey]
		newInternalEntry := otherTable.internalEntries[internalKey]
		if !ok {
			// routing key only exist in the new table
			_, message := t.emitDiffMessages(internalKey, RoutableEndpoints{}, newInternalEntry)
			messagesToEmit = messagesToEmit.Merge(message)
			continue
		}
		merged := mergeUnfreshRoutes(existingInternalEntry, newInternalEntry, domains)
		otherTable.internalEntries[internalKey] = merged
		otherTable.deleteInternalEntryIfEmpty(internalKey)
		_, message := t.emitDiffMessages(internalKey, existingInternalEntry, merged)
		messagesToEmit = messagesToEmit.Merge(message)
	}

	t.entries = otherTable.entries
	t.internalEntries = otherTable.internalEntries

	return mappings, messagesToEmit
}

// merge the routes from both endpoints, ensuring that non-fresh routes aren't removed
func mergeUnfreshRoutes(before, after RoutableEndpoints, domains models.DomainSet) RoutableEndpoints {
	merged := after.copy()
	merged.Domain = before.Domain

	if !domains.Contains(before.Domain) {
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

	return merged
}

func (t *routingTable) GetRoutingEvents() (TCPRouteMappings, MessagesToEmit) {
	logger := t.logger.Session("get-routing-events")
	logger.Info("started")
	defer logger.Info("finished")

	t.Lock()
	defer t.Unlock()

	var messagesToEmit MessagesToEmit
	var mappings TCPRouteMappings
	for key, route := range t.entries {
		mapping, message := t.emitDiffMessages(key, RoutableEndpoints{}, route)

		mappings = mappings.Merge(mapping)
		messagesToEmit = messagesToEmit.Merge(message)
	}

	for key, route := range t.internalEntries {
		_, message := t.emitDiffMessages(key, RoutableEndpoints{}, route)

		messagesToEmit = messagesToEmit.Merge(message)
	}

	return mappings, messagesToEmit
}

type routeMapping interface {
	MessageFor(endpoint Endpoint, directInstanceAddress bool) (*RegistryMessage, *tcpmodels.TcpRouteMapping, *RegistryMessage)
}

func httpRoutesFromSchedulingInfo(lrp *models.DesiredLRPSchedulingInfo) map[RoutingKey][]routeMapping {
	if lrp == nil {
		return nil
	}

	routes, _ := cfroutes.CFRoutesFromRoutingInfo(lrp.Routes)
	routeEntries := make(map[RoutingKey][]routeMapping)
	for _, route := range routes {
		key := RoutingKey{ProcessGUID: lrp.ProcessGuid, ContainerPort: route.Port}

		routes := []routeMapping{}
		for _, hostname := range route.Hostnames {
			route := Route{
				Hostname:         hostname,
				LogGUID:          lrp.LogGuid,
				RouteServiceUrl:  route.RouteServiceUrl,
				IsolationSegment: route.IsolationSegment,
			}
			routes = append(routes, route)
		}
		routeEntries[key] = append(routeEntries[key], routes...)
	}
	return routeEntries
}

func tcpRoutesFromSchedulingInfo(lrp *models.DesiredLRPSchedulingInfo) map[RoutingKey][]routeMapping {
	if lrp == nil {
		return nil
	}

	routes, _ := tcp_routes.TCPRoutesFromRoutingInfo(&lrp.Routes)

	routeEntries := make(map[RoutingKey][]routeMapping)
	for _, route := range routes {
		key := RoutingKey{ProcessGUID: lrp.ProcessGuid, ContainerPort: route.ContainerPort}

		routeEntries[key] = append(routeEntries[key], ExternalEndpointInfo{
			RouterGroupGUID: route.RouterGroupGuid,
			Port:            route.ExternalPort,
		})
	}
	return routeEntries
}

func internalRoutesFromSchedulingInfo(lrp *models.DesiredLRPSchedulingInfo) map[RoutingKey][]routeMapping {
	if lrp == nil {
		return nil
	}

	routes, _ := internalroutes.InternalRoutesFromRoutingInfo(lrp.Routes)

	routeEntries := make(map[RoutingKey][]routeMapping)
	for _, route := range routes {
		key := RoutingKey{ProcessGUID: lrp.ProcessGuid}
		routeEntries[key] = append(routeEntries[key], InternalRoute{
			Hostname: route.Hostname,
			LogGUID:  lrp.LogGuid,
		})
	}
	return routeEntries
}

func (table *routingTable) SetRoutes(before, after *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit) {
	logger := table.logger.Session("set-routes", lager.Data{"before_lrp": DesiredLRPData(before), "after_lrp": DesiredLRPData(after)})
	logger.Info("started")
	defer logger.Info("finished")

	table.Lock()
	defer table.Unlock()

	// update routes
	httpRemovedRouteEntries := httpRoutesFromSchedulingInfo(before)
	httpRouteEntries := httpRoutesFromSchedulingInfo(after)
	tcpRemovedRouteEntries := tcpRoutesFromSchedulingInfo(before)
	tcpRouteEntries := tcpRoutesFromSchedulingInfo(after)
	internalRemovedRouteEntries := internalRoutesFromSchedulingInfo(before)
	internalRouteEntries := internalRoutesFromSchedulingInfo(after)
	logger.Info("internal-route-table", lager.Data{"numEntries": len(table.internalEntries)})

	var messagesToEmit MessagesToEmit = MessagesToEmit{}
	var mappings TCPRouteMappings

	// merge the http and tcp routes
	mergeRoutes := func(first, second map[RoutingKey][]routeMapping) map[RoutingKey][]routeMapping {
		result := make(map[RoutingKey][]routeMapping)
		for key, routes := range first {
			result[key] = append(routes, second[key]...)
		}

		for key, routes := range second {
			_, ok := first[key]
			if ok {
				// this has been merged already in the previous loop
				continue
			}
			result[key] = routes
		}

		return result
	}

	routeEntries := mergeRoutes(httpRouteEntries, tcpRouteEntries)
	removedRouteEntries := mergeRoutes(httpRemovedRouteEntries, tcpRemovedRouteEntries)

	for key, routes := range routeEntries {
		currentEntry := table.entries[key]
		// if modification tag is old, ignore the new lrp
		if !currentEntry.ModificationTag.SucceededBy(&after.ModificationTag) {
			continue
		}

		newEntry := currentEntry.copy()
		newEntry.Domain = after.Domain
		newEntry.Routes = routes
		newEntry.ModificationTag = &after.ModificationTag
		newEntry.DesiredInstances = after.Instances

		// check if scaling down
		if before != nil && after != nil && before.Instances > after.Instances {
			newEndpoints := make(map[EndpointKey]Endpoint)

			for endpointKey, endpoint := range newEntry.Endpoints {
				if endpoint.Index < after.Instances {
					newEndpoints[endpointKey] = endpoint
				}
			}
			newEntry.Endpoints = newEndpoints
		}

		table.entries[key] = newEntry

		mapping, message := table.emitDiffMessages(key, currentEntry, newEntry)
		messagesToEmit = messagesToEmit.Merge(message)
		mappings = mappings.Merge(mapping)
	}

	for key, routes := range internalRouteEntries {
		currentEntry := table.internalEntries[key]
		// if modification tag is old, ignore the new desired lrp
		if !currentEntry.ModificationTag.SucceededBy(&after.ModificationTag) {
			continue
		}

		newEntry := currentEntry.copy()
		newEntry.Domain = after.Domain
		newEntry.Routes = routes
		newEntry.ModificationTag = &after.ModificationTag
		newEntry.DesiredInstances = after.Instances

		// check if scaling down
		if before != nil && after != nil && before.Instances > after.Instances {
			newEndpoints := make(map[EndpointKey]Endpoint)

			for endpointKey, endpoint := range newEntry.Endpoints {
				if endpoint.Index < after.Instances {
					newEndpoints[endpointKey] = endpoint
				}
			}
			newEntry.Endpoints = newEndpoints
		}

		table.internalEntries[key] = newEntry

		// Emit diff messages
		mapping, message := table.emitDiffMessages(key, currentEntry, newEntry)
		messagesToEmit = messagesToEmit.Merge(message)
		mappings = mappings.Merge(mapping)
	}

	for key := range removedRouteEntries {
		if _, ok := routeEntries[key]; ok {
			continue
		}

		currentEntry := table.entries[key]
		if after == nil {
			// this is a delete (after == nil), then before lrp modification tag must be >=
			if !currentEntry.ModificationTag.Equal(&before.ModificationTag) && !currentEntry.ModificationTag.SucceededBy(&before.ModificationTag) {
				logger.Debug("skipping-old-update")
				continue
			}
		} else if !currentEntry.ModificationTag.SucceededBy(&after.ModificationTag) {
			// this is an update, then after lrp modification tag must be >
			continue
		}

		newEntry := currentEntry.copy()
		newEntry.Routes = nil
		if after != nil {
			newEntry.Domain = after.Domain
			newEntry.ModificationTag = &after.ModificationTag
			newEntry.DesiredInstances = after.Instances
		}

		table.entries[key] = newEntry

		table.deleteEntryIfEmpty(key)

		mapping, message := table.emitDiffMessages(key, currentEntry, newEntry)
		messagesToEmit = messagesToEmit.Merge(message)
		mappings = mappings.Merge(mapping)
	}

	for key := range internalRemovedRouteEntries {
		currentEntry := table.internalEntries[key]
		if after == nil {
			// this is a delete (after == nil), then before lrp modification tag must be >=
			if !currentEntry.ModificationTag.Equal(&before.ModificationTag) && !currentEntry.ModificationTag.SucceededBy(&before.ModificationTag) {
				logger.Debug("skipping-old-update")
				continue
			}
		} else if !currentEntry.ModificationTag.SucceededBy(&after.ModificationTag) {
			// this is an update, then after lrp modification tag must be >
			continue
		}

		newEntry := currentEntry.copy()
		newEntry.Routes = nil
		if after != nil {
			newEntry.Domain = after.Domain
			newEntry.ModificationTag = &after.ModificationTag
			newEntry.DesiredInstances = after.Instances
		}

		table.internalEntries[key] = newEntry

		table.deleteInternalEntryIfEmpty(key)

		_, message := table.emitDiffMessages(key, currentEntry, newEntry)
		messagesToEmit = messagesToEmit.Merge(message)
	}

	return mappings, messagesToEmit
}

func (table *routingTable) deleteEntryIfEmpty(key RoutingKey) {
	entry := table.entries[key]
	if len(entry.Endpoints) == 0 && len(entry.Routes) == 0 {
		delete(table.entries, key)
	}
}

func (table *routingTable) deleteInternalEntryIfEmpty(key RoutingKey) {
	entry := table.internalEntries[key]
	if len(entry.Endpoints) == 0 && len(entry.Routes) == 0 {
		delete(table.internalEntries, key)
	}
}

func (table *routingTable) emitDiffMessages(key RoutingKey, oldEntry, newEntry RoutableEndpoints) (TCPRouteMappings, MessagesToEmit) {
	routesDiff := diffRoutes(oldEntry.Routes, newEntry.Routes)
	endpointsDiff := diffEndpoints(oldEntry.Endpoints, newEntry.Endpoints)
	return table.messages(routesDiff, endpointsDiff)
}

type routesDiff struct {
	before, after, removed, added []routeMapping
}

type endpointsDiff struct {
	before, after, removed, added map[EndpointKey]Endpoint
}

func diffRoutes(before, after []routeMapping) routesDiff {
	existingRoutes := map[routeMapping]struct{}{}
	newRoutes := map[routeMapping]struct{}{}
	for _, route := range before {
		existingRoutes[route] = struct{}{}
	}
	for _, route := range after {
		newRoutes[route] = struct{}{}
	}

	diff := routesDiff{
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

// endpoints are different if any field is different other than the following:
// 1. ModificationTag
// 2. Evacuating
func endpointDifferent(before, after Endpoint) bool {
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

func diffEndpoints(before, after map[EndpointKey]Endpoint) endpointsDiff {
	diff := endpointsDiff{
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
		if !ok || endpointDifferent(newEndpoint, endpoint) {
			diff.removed[key] = endpoint
		}
	}
	for key, endpoint := range after {
		oldEndpoint, ok := before[key]
		if !ok {
			key.Evacuating = !key.Evacuating
			oldEndpoint, ok = before[key]
		}
		if !ok || endpointDifferent(endpoint, oldEndpoint) {
			diff.added[key] = endpoint
		}
	}

	return diff
}

func (table *routingTable) messages(routesDiff routesDiff, endpointDiff endpointsDiff) (TCPRouteMappings, MessagesToEmit) {
	// maps used to remove duplicates
	unregistrations := map[routeMapping]map[Endpoint]bool{}
	registrations := map[routeMapping]map[Endpoint]bool{}

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

	// for added routes add all currently known endpoints
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

	// for added endpoints register all current routes
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

	messages := MessagesToEmit{}
	mappings := TCPRouteMappings{}

	for r, es := range registrations {
		for e := range es {
			msg, mapping, internalMsg := r.MessageFor(e, table.directInstanceRoute)
			if msg != nil {
				messages.RegistrationMessages = append(messages.RegistrationMessages, *msg)
			}
			if mapping != nil {
				mappings.Registrations = append(mappings.Registrations, *mapping)
			}
			if internalMsg != nil {
				messages.InternalRegistrationMessages = append(messages.InternalRegistrationMessages, *internalMsg)
			}
		}
	}
	for r, es := range unregistrations {
		for e := range es {
			msg, mapping, internalMsg := r.MessageFor(e, table.directInstanceRoute)
			if msg != nil {
				messages.UnregistrationMessages = append(messages.UnregistrationMessages, *msg)
			}
			if mapping != nil {
				mappings.Unregistrations = append(mappings.Unregistrations, *mapping)
			}
			if internalMsg != nil {
				messages.InternalUnregistrationMessages = append(messages.InternalUnregistrationMessages, *internalMsg)
			}
		}
	}
	return mappings, messages
}

func (table *routingTable) RemoveRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit) {
	logger := table.logger.Session("RemoveRoutes", lager.Data{"desired_lrp": DesiredLRPData(desiredLRP)})
	logger.Debug("starting")
	defer logger.Debug("completed")

	return table.SetRoutes(desiredLRP, nil)
}

func (t *routingTable) HTTPAssociationsCount() int {
	t.Lock()
	defer t.Unlock()

	count := 0
	for _, entry := range t.entries {
		count += numberOfHTTPRoutes(entry) * len(entry.Endpoints)
	}

	return count
}

func (t *routingTable) TCPAssociationsCount() int {
	t.Lock()
	defer t.Unlock()

	count := 0
	for _, entry := range t.entries {
		count += numberOfTCPRoutes(entry) * len(entry.Endpoints)
	}

	return count
}

func (t *routingTable) InternalAssociationsCount() int {
	t.Lock()
	defer t.Unlock()

	count := 0
	for _, entry := range t.internalEntries {
		count += 2 * len(entry.Routes) * len(entry.Endpoints)
	}

	return count
}

func (t *routingTable) TableSize() int {
	t.Lock()
	defer t.Unlock()

	return len(t.entries)
}

func numberOfTCPRoutes(routableEndpoints RoutableEndpoints) int {
	count := 0
	for _, route := range routableEndpoints.Routes {
		if _, ok := route.(ExternalEndpointInfo); ok {
			count++
		}
	}
	return count
}

func numberOfHTTPRoutes(routableEndpoints RoutableEndpoints) int {
	return len(routableEndpoints.Routes) - numberOfTCPRoutes(routableEndpoints)
}

func (t *routingTable) HasExternalRoutes(actual *ActualLRPRoutingInfo) bool {
	for _, key := range NewRoutingKeysFromActual(actual) {
		if len(t.entries[key].Routes) > 0 {
			return true
		}
	}

	return false
}
