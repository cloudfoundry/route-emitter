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

const addressCollisionsCounter = "AddressCollisions"

//go:generate counterfeiter -o fakeroutingtable/fake_routingtable.go . RoutingTable
type RoutingTable interface {
	// table modification
	SetRoutes(beforeLRP, afterLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit)
	RemoveRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit)
	AddEndpoint(actualLRP *ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit)
	RemoveEndpoint(actualLRP *ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit)
	Swap(t RoutingTable, domains models.DomainSet) (TCPRouteMappings, MessagesToEmit)
	GetInternalRoutingEvents() (TCPRouteMappings, MessagesToEmit)
	GetExternalRoutingEvents() (TCPRouteMappings, MessagesToEmit)

	// routes

	HasExternalRoutes(actual *ActualLRPRoutingInfo) bool
	HTTPAssociationsCount() int     // return number of associations desired-lrp-http-routes * actual-lrps
	InternalAssociationsCount() int // return number of associations desired-lrp-internal-routes * 2 * actual-lrps
	TCPAssociationsCount() int      // return number of associations desired-lrp-tcp-routes * actual-lrps
	TableSize() int
}

type internalRoutingTable struct {
	endpointGenerator        func(*ActualLRPRoutingInfo) []Endpoint
	routesGenerator          func(*models.DesiredLRPSchedulingInfo) map[RoutingKey][]routeMapping
	entries                  map[RoutingKey]RoutableEndpoints
	addressEntries           map[Address]EndpointKey
	addressGenerator         func(endpoint Endpoint) Address
	directInstanceRoute      bool
	logger                   lager.Logger
	metronClient             loggingclient.IngressClient
	suppressAddressCollision bool
	sync.Locker
}

type routingTable struct {
	logger                     lager.Logger
	tcpRoutesRoutingTable      *internalRoutingTable
	httpRoutesRoutingTable     *internalRoutingTable
	internalRoutesRoutingTable *internalRoutingTable
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

	httpRoutingTable := &internalRoutingTable{
		endpointGenerator:   NewEndpointsFromActual,
		routesGenerator:     httpRoutesFromSchedulingInfo,
		entries:             make(map[RoutingKey]RoutableEndpoints),
		addressEntries:      make(map[Address]EndpointKey),
		directInstanceRoute: directInstanceRoute,
		addressGenerator:    addressGenerator,
		logger:              logger.Session("http"),
		metronClient:        metronClient,
		Locker:              &sync.Mutex{},
	}
	tcpRoutingTable := &internalRoutingTable{
		endpointGenerator:        NewEndpointsFromActual,
		routesGenerator:          tcpRoutesFromSchedulingInfo,
		entries:                  make(map[RoutingKey]RoutableEndpoints),
		addressEntries:           make(map[Address]EndpointKey),
		directInstanceRoute:      directInstanceRoute,
		addressGenerator:         addressGenerator,
		logger:                   logger.Session("tcp"),
		metronClient:             metronClient,
		suppressAddressCollision: true,
		Locker: &sync.Mutex{},
	}
	internalRoutingTable := &internalRoutingTable{
		endpointGenerator:        internalEndpointsFromRoutingInfo,
		routesGenerator:          internalRoutesFromSchedulingInfo,
		entries:                  make(map[RoutingKey]RoutableEndpoints),
		addressEntries:           make(map[Address]EndpointKey),
		directInstanceRoute:      directInstanceRoute,
		addressGenerator:         addressGenerator,
		logger:                   logger.Session("internal"),
		metronClient:             metronClient,
		suppressAddressCollision: true,
		Locker: &sync.Mutex{},
	}

	return &routingTable{
		logger:                     logger,
		tcpRoutesRoutingTable:      tcpRoutingTable,
		httpRoutesRoutingTable:     httpRoutingTable,
		internalRoutesRoutingTable: internalRoutingTable,
	}
}

func newRoutingTable() *internalRoutingTable {
	return &internalRoutingTable{}
}

func internalEndpointsFromRoutingInfo(actualLRP *ActualLRPRoutingInfo) []Endpoint {
	return []Endpoint{
		{
			InstanceGUID:    actualLRP.ActualLRP.InstanceGuid,
			Index:           actualLRP.ActualLRP.Index,
			Host:            actualLRP.ActualLRP.Address,
			ContainerIP:     actualLRP.ActualLRP.InstanceAddress,
			Evacuating:      actualLRP.Evacuating,
			Since:           actualLRP.ActualLRP.Since,
			ModificationTag: &actualLRP.ActualLRP.ModificationTag,
		},
	}
}

func (table *routingTable) AddEndpoint(actualLRP *ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit) {
	httpMappings, httpMessages := table.httpRoutesRoutingTable.AddEndpoint(actualLRP)
	tcpMappings, tcpMessages := table.tcpRoutesRoutingTable.AddEndpoint(actualLRP)
	internalMappings, internalMessages := table.internalRoutesRoutingTable.AddEndpoint(actualLRP)

	mappings := httpMappings.Merge(tcpMappings).Merge(internalMappings)
	messages := httpMessages.Merge(tcpMessages).Merge(internalMessages)
	return mappings, messages
}

func (table *routingTable) RemoveEndpoint(actualLRP *ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit) {
	httpMappings, httpMessages := table.httpRoutesRoutingTable.RemoveEndpoint(actualLRP)
	tcpMappings, tcpMessages := table.tcpRoutesRoutingTable.RemoveEndpoint(actualLRP)
	internalMappings, internalMessages := table.internalRoutesRoutingTable.RemoveEndpoint(actualLRP)

	mappings := httpMappings.Merge(tcpMappings).Merge(internalMappings)
	messages := httpMessages.Merge(tcpMessages).Merge(internalMessages)
	return mappings, messages
}

func (t *routingTable) Swap(other RoutingTable, domains models.DomainSet) (TCPRouteMappings, MessagesToEmit) {
	table, ok := other.(*routingTable)
	if !ok {
		t.logger.Error("failed-to-convert-to-routing-table", nil)
		return TCPRouteMappings{}, MessagesToEmit{}
	}

	httpMappings, httpMessages := t.httpRoutesRoutingTable.Swap(table.httpRoutesRoutingTable, domains)
	tcpMappings, tcpMessages := t.tcpRoutesRoutingTable.Swap(table.tcpRoutesRoutingTable, domains)
	internalMappings, internalMessages := t.internalRoutesRoutingTable.Swap(table.internalRoutesRoutingTable, domains)

	mappings := httpMappings.Merge(tcpMappings).Merge(internalMappings)
	messages := httpMessages.Merge(tcpMessages).Merge(internalMessages)
	return mappings, messages
}

func (t *routingTable) GetExternalRoutingEvents() (TCPRouteMappings, MessagesToEmit) {
	httpMappings, httpMessages := t.httpRoutesRoutingTable.GetRoutingEvents()
	tcpMappings, tcpMessages := t.tcpRoutesRoutingTable.GetRoutingEvents()

	mappings := httpMappings.Merge(tcpMappings)
	messages := httpMessages.Merge(tcpMessages)
	return mappings, messages
}

func (t *routingTable) GetInternalRoutingEvents() (TCPRouteMappings, MessagesToEmit) {
	return t.internalRoutesRoutingTable.GetRoutingEvents()
}

func (t *routingTable) SetRoutes(before, after *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit) {
	httpMappings, httpMessages := t.httpRoutesRoutingTable.SetRoutes(before, after)
	tcpMappings, tcpMessages := t.tcpRoutesRoutingTable.SetRoutes(before, after)
	internalMappings, internalMessages := t.internalRoutesRoutingTable.SetRoutes(before, after)

	mappings := httpMappings.Merge(tcpMappings).Merge(internalMappings)
	messages := httpMessages.Merge(tcpMessages).Merge(internalMessages)
	return mappings, messages
}

func (t *routingTable) RemoveRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit) {
	httpMappings, httpMessages := t.httpRoutesRoutingTable.RemoveRoutes(desiredLRP)
	tcpMappings, tcpMessages := t.tcpRoutesRoutingTable.RemoveRoutes(desiredLRP)
	internalMappings, internalMessages := t.internalRoutesRoutingTable.RemoveRoutes(desiredLRP)

	mappings := httpMappings.Merge(tcpMappings).Merge(internalMappings)
	messages := httpMessages.Merge(tcpMessages).Merge(internalMessages)
	return mappings, messages
}

func (table *internalRoutingTable) AddEndpoint(actualLRP *ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit) {
	logger := table.logger.Session("AddEndpoint", lager.Data{"actual_lrp": actualLRP})
	logger.Debug("starting")
	defer logger.Debug("completed")

	table.Lock()
	defer table.Unlock()

	logger.Info("handler-add-and-emit", lager.Data{"net_info": actualLRP.ActualLRP.ActualLRPNetInfo})
	endpoints := table.endpointGenerator(actualLRP)

	// collision detection

	if !table.suppressAddressCollision {
		for _, endpoint := range endpoints {
			address := table.addressGenerator(endpoint)
			// if the address exists and the instance guid doesn't match then we have a collision
			if existingEndpointKey, ok := table.addressEntries[address]; ok && existingEndpointKey.InstanceGUID != endpoint.InstanceGUID {
				table.metronClient.IncrementCounter(addressCollisionsCounter)
				existingInstanceGuid := existingEndpointKey.InstanceGUID
				table.logger.Info("collision-detected-with-endpoint", lager.Data{
					"instance_guid_a": existingInstanceGuid,
					"instance_guid_b": endpoint.InstanceGUID,
					"Address":         address,
				})
			}

			table.addressEntries[address] = endpoint.key()
		}
	}

	// add endpoints

	var messagesToEmit MessagesToEmit
	var mappings TCPRouteMappings

	for _, routingEndpoint := range endpoints {
		key := RoutingKey{
			ProcessGUID:   actualLRP.ActualLRP.ProcessGuid,
			ContainerPort: routingEndpoint.ContainerPort,
		}
		currentEntry := table.entries[key]
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
	}

	return mappings, messagesToEmit
}

func (table *internalRoutingTable) RemoveEndpoint(actualLRP *ActualLRPRoutingInfo) (TCPRouteMappings, MessagesToEmit) {
	logger := table.logger.Session("RemoveEndpoint", lager.Data{"actual_lrp": actualLRP})
	logger.Debug("starting")
	defer logger.Debug("completed")

	table.Lock()
	defer table.Unlock()

	table.logger.Session("removing-endpoint")
	table.logger.Info("starting")
	defer table.logger.Info("complete")

	endpoints := table.endpointGenerator(actualLRP)

	// remove address
	if !table.suppressAddressCollision {
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
	}

	// remove endpoint
	var messagesToEmit MessagesToEmit
	var mappings TCPRouteMappings
	for _, routingEndpoint := range endpoints {
		key := RoutingKey{
			ProcessGUID:   actualLRP.ActualLRP.ProcessGuid,
			ContainerPort: routingEndpoint.ContainerPort,
		}

		currentEntry := table.entries[key]
		endpointKey := routingEndpoint.key()
		currentEndpoint, ok := currentEntry.Endpoints[endpointKey]

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
	}

	return mappings, messagesToEmit
}

func (t *internalRoutingTable) Swap(otherTable *internalRoutingTable, domains models.DomainSet) (TCPRouteMappings, MessagesToEmit) {
	logger := t.logger.Session("swap", lager.Data{"received-domains": domains})
	logger.Info("started")
	defer logger.Info("finished")

	t.Lock()
	defer t.Unlock()

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

	t.entries = otherTable.entries

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

func (t *internalRoutingTable) GetRoutingEvents() (TCPRouteMappings, MessagesToEmit) {
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

	return mappings, messagesToEmit
}

type routeMapping interface {
	MessageFor(endpoint Endpoint, directInstanceAddress, emitEndpointUpdatedAt bool) (*RegistryMessage, *tcpmodels.TcpRouteMapping, *RegistryMessage)
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

func (table *internalRoutingTable) SetRoutes(before, after *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit) {
	logger := table.logger.Session("set-routes", lager.Data{"before_lrp": DesiredLRPData(before), "after_lrp": DesiredLRPData(after)})
	logger.Info("started")
	defer logger.Info("finished")

	table.Lock()
	defer table.Unlock()

	// update routes
	removedRouteEntries := table.routesGenerator(before)
	routeEntries := table.routesGenerator(after)
	// logger.Info("internal-route-table", lager.Data{"numEntries": len(table.internalEntries)})

	var messagesToEmit MessagesToEmit = MessagesToEmit{}
	var mappings TCPRouteMappings

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

	return mappings, messagesToEmit
}

func (table *internalRoutingTable) deleteEntryIfEmpty(key RoutingKey) {
	entry := table.entries[key]
	if len(entry.Endpoints) == 0 && len(entry.Routes) == 0 {
		delete(table.entries, key)
	}
}

func (table *internalRoutingTable) emitDiffMessages(key RoutingKey, oldEntry, newEntry RoutableEndpoints) (TCPRouteMappings, MessagesToEmit) {
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
// 3. Since
func endpointDifferent(before, after Endpoint) bool {
	endpoint := before
	endpoint.ModificationTag = after.ModificationTag
	endpoint.Evacuating = after.Evacuating
	endpoint.Since = after.Since
	return endpoint != after
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

func (table *internalRoutingTable) messages(routesDiff routesDiff, endpointDiff endpointsDiff) (TCPRouteMappings, MessagesToEmit) {
	type registrationMetadata struct {
		emitEndpointUpdatedAt bool
	}

	// maps used to remove duplicates
	unregistrations := map[routeMapping]map[Endpoint]bool{}
	registrations := map[routeMapping]map[Endpoint]*registrationMetadata{}

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
			if registrations[route] != nil && registrations[route][container] != nil {
				continue
			}
			if registrations[route] == nil {
				registrations[route] = map[Endpoint]*registrationMetadata{}
			}
			registrations[route][container] = &registrationMetadata{
				emitEndpointUpdatedAt: false,
			}
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
			if registrations[route] != nil && registrations[route][container] != nil {
				continue
			}
			if registrations[route] == nil {
				registrations[route] = map[Endpoint]*registrationMetadata{}
			}
			registrations[route][container] = &registrationMetadata{
				emitEndpointUpdatedAt: true,
			}
		}
	}

	messages := MessagesToEmit{}
	mappings := TCPRouteMappings{}

	for r, es := range registrations {
		for e, metadata := range es {
			msg, mapping, internalMsg := r.MessageFor(e, table.directInstanceRoute, metadata.emitEndpointUpdatedAt)
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
			msg, mapping, internalMsg := r.MessageFor(e, table.directInstanceRoute, false)
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

func (table *internalRoutingTable) RemoveRoutes(desiredLRP *models.DesiredLRPSchedulingInfo) (TCPRouteMappings, MessagesToEmit) {
	logger := table.logger.Session("RemoveRoutes", lager.Data{"desired_lrp": DesiredLRPData(desiredLRP)})
	logger.Debug("starting")
	defer logger.Debug("completed")

	return table.SetRoutes(desiredLRP, nil)
}

func (t *internalRoutingTable) AssociationsCount() int {
	t.Lock()
	defer t.Unlock()

	count := 0
	for _, entry := range t.entries {
		count += len(entry.Routes) * len(entry.Endpoints)
	}

	return count
}

func (t *internalRoutingTable) TableSize() int {
	t.Lock()
	defer t.Unlock()

	return len(t.entries)
}

func (t *internalRoutingTable) HasExternalRoutes(actual *ActualLRPRoutingInfo) bool {
	for _, key := range NewRoutingKeysFromActual(actual) {
		if len(t.entries[key].Routes) > 0 {
			return true
		}
	}

	return false
}

func (t *routingTable) HTTPAssociationsCount() int {
	return t.httpRoutesRoutingTable.AssociationsCount()
}

func (t *routingTable) TCPAssociationsCount() int {
	return t.tcpRoutesRoutingTable.AssociationsCount()
}

func (t *routingTable) InternalAssociationsCount() int {
	return 2 * t.internalRoutesRoutingTable.AssociationsCount()
}

func (t *routingTable) TableSize() int {
	return t.httpRoutesRoutingTable.TableSize() + t.tcpRoutesRoutingTable.TableSize() + t.internalRoutesRoutingTable.TableSize()
}

func (t *routingTable) HasExternalRoutes(actual *ActualLRPRoutingInfo) bool {
	return t.httpRoutesRoutingTable.HasExternalRoutes(actual) || t.tcpRoutesRoutingTable.HasExternalRoutes(actual)
}
