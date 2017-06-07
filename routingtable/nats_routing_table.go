package routingtable

import (
	"sync"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/runtimeschema/metric"
)

var addressCollisions = metric.Counter("AddressCollisions")

//go:generate counterfeiter -o fakeroutingtable/fake_natsroutingtable.go . NATSRoutingTable

type NATSRoutingTable interface {
	AddEndpoint(key endpoint.RoutingKey, routingEndpoint Endpoint) MessagesToEmit
	SetRoutes(key endpoint.RoutingKey, routes []Route, modTag *models.ModificationTag) MessagesToEmit
	EndpointsForIndex(key endpoint.RoutingKey, index int32) []Endpoint
	GetRoutes(key endpoint.RoutingKey) []Route
	MessagesToEmit() MessagesToEmit
	RemoveEndpoint(key endpoint.RoutingKey, routingEndpoint Endpoint) MessagesToEmit
	RemoveRoutes(key endpoint.RoutingKey, modTag *models.ModificationTag) MessagesToEmit
	RouteCount() int
	Swap(newTable NATSRoutingTable, domains models.DomainSet) MessagesToEmit
}

type noopLocker struct{}

func (noopLocker) Lock()   {}
func (noopLocker) Unlock() {}

type natsRoutingTable struct {
	entries        map[endpoint.RoutingKey]RoutableEndpoints
	addressEntries map[Address]EndpointKey // for collision detection
	sync.Locker
	messageBuilder MessageBuilder
	logger         lager.Logger
}

func NewTempTable(routesMap RoutesByRoutingKey, endpointsByKey EndpointsByRoutingKey) NATSRoutingTable {
	entries := make(map[endpoint.RoutingKey]RoutableEndpoints)
	addressEntries := make(map[Address]EndpointKey)

	for key, routes := range routesMap {
		entries[key] = RoutableEndpoints{
			Routes: routes,
		}
	}

	for key, endpoints := range endpointsByKey {
		entry, ok := entries[key]
		if !ok {
			entry = RoutableEndpoints{}
		}
		entry.Endpoints = EndpointsAsMap(endpoints)
		entries[key] = entry
		for _, endpoint := range endpoints {
			addressEntries[endpoint.address()] = endpoint.key()
		}
	}

	return &natsRoutingTable{
		entries:        entries,
		addressEntries: addressEntries,
		Locker:         noopLocker{},
		messageBuilder: NoopMessageBuilder{},
	}
}

func NewNATSTable(logger lager.Logger, builder MessageBuilder) NATSRoutingTable {
	return &natsRoutingTable{
		entries:        make(map[endpoint.RoutingKey]RoutableEndpoints),
		addressEntries: make(map[Address]EndpointKey),
		Locker:         &sync.Mutex{},
		messageBuilder: builder,
		logger:         logger,
	}
}

func (table *natsRoutingTable) EndpointsForIndex(key endpoint.RoutingKey, index int32) []Endpoint {
	table.Lock()
	defer table.Unlock()

	endpointsForIndex := make([]Endpoint, 0, 2)
	endpointsForKey := table.entries[key].Endpoints

	for _, endpoint := range endpointsForKey {
		if endpoint.Index == index {
			endpointsForIndex = append(endpointsForIndex, endpoint)
		}
	}

	return endpointsForIndex
}

func (table *natsRoutingTable) RouteCount() int {
	table.Lock()

	count := 0
	for _, entry := range table.entries {
		count += len(entry.Routes) * len(entry.Endpoints)
	}

	table.Unlock()
	return count
}

func (table *natsRoutingTable) Swap(t NATSRoutingTable, domains models.DomainSet) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}

	newTable, ok := t.(*natsRoutingTable)
	if !ok {
		return messagesToEmit
	}
	newEntries := newTable.entries
	updatedEntries := make(map[endpoint.RoutingKey]RoutableEndpoints)
	updatedAddressEntries := make(map[Address]EndpointKey)

	table.Lock()
	for key, newEntry := range newEntries {
		// See if we have a match
		existingEntry, _ := table.entries[key]

		//always register everything on sync  NOTE if a merge does occur we may return an altered newEntry
		messagesToEmit = messagesToEmit.Merge(table.messageBuilder.MergedRegistrations(&existingEntry, &newEntry, domains))
		updatedEntries[key] = newEntry
		for _, endpoint := range newEntry.Endpoints {
			updatedAddressEntries[endpoint.address()] = endpoint.key()
		}
	}

	for key, existingEntry := range table.entries {
		newEntry, ok := newEntries[key]
		messagesToEmit = messagesToEmit.Merge(table.messageBuilder.UnregistrationsFor(&existingEntry, &newEntry, domains))

		// maybe reemit old ones no longer found in the new table
		if !ok {
			unfreshRegistrations := table.messageBuilder.UnfreshRegistrations(&existingEntry, domains)
			if len(unfreshRegistrations.RegistrationMessages) > 0 {
				updatedEntries[key] = existingEntry
				for _, endpoint := range existingEntry.Endpoints {
					updatedAddressEntries[endpoint.address()] = endpoint.key()
				}
				messagesToEmit = messagesToEmit.Merge(unfreshRegistrations)
			}
		}
	}

	table.entries = updatedEntries
	table.addressEntries = updatedAddressEntries
	table.Unlock()

	return messagesToEmit
}

func (table *natsRoutingTable) MessagesToEmit() MessagesToEmit {
	table.Lock()

	messagesToEmit := MessagesToEmit{}
	for _, entry := range table.entries {
		messagesToEmit = messagesToEmit.Merge(table.messageBuilder.RegistrationsFor(nil, &entry))
	}

	table.Unlock()
	return messagesToEmit
}

func (table *natsRoutingTable) SetRoutes(key endpoint.RoutingKey, routes []Route, modTag *models.ModificationTag) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	currentEntry := table.entries[key]
	if !currentEntry.ModificationTag.SucceededBy(modTag) {
		return MessagesToEmit{}
	}

	newEntry := currentEntry.copy()
	newEntry.Routes = routes
	newEntry.ModificationTag = modTag
	table.entries[key] = newEntry

	return table.emit(key, currentEntry, newEntry)
}

func (table *natsRoutingTable) GetRoutes(key endpoint.RoutingKey) []Route {
	table.Lock()
	defer table.Unlock()

	currentEntry := table.entries[key]

	return currentEntry.Routes
}

func (table *natsRoutingTable) RemoveRoutes(key endpoint.RoutingKey, modTag *models.ModificationTag) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	currentEntry := table.entries[key]
	if !(currentEntry.ModificationTag.Equal(modTag) || currentEntry.ModificationTag.SucceededBy(modTag)) {
		return MessagesToEmit{}
	}

	newEntry := NewRoutableEndpoints()
	newEntry.Endpoints = currentEntry.Endpoints

	table.entries[key] = newEntry

	return table.emit(key, currentEntry, newEntry)
}

func (table *natsRoutingTable) AddEndpoint(key endpoint.RoutingKey, routingEndpoint Endpoint) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	currentEntry := table.entries[key]
	newEntry := currentEntry.copy()
	newEntry.Endpoints[routingEndpoint.key()] = routingEndpoint
	table.entries[key] = newEntry

	address := routingEndpoint.address()

	if existingEndpointKey, ok := table.addressEntries[address]; ok {
		if existingEndpointKey.InstanceGuid != routingEndpoint.InstanceGuid {
			addressCollisions.Add(1)
			existingInstanceGuid := existingEndpointKey.InstanceGuid
			table.logger.Info("collision-detected-with-endpoint", lager.Data{
				"instance_guid_a": existingInstanceGuid,
				"instance_guid_b": routingEndpoint.InstanceGuid,
				"Address":         routingEndpoint.address(),
			})
		}
	}

	table.addressEntries[address] = routingEndpoint.key()

	return table.emit(key, currentEntry, newEntry)
}

func (table *natsRoutingTable) RemoveEndpoint(key endpoint.RoutingKey, routingEndpoint Endpoint) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	currentEntry := table.entries[key]
	endpointKey := routingEndpoint.key()
	currentEndpoint, ok := currentEntry.Endpoints[endpointKey]

	if !ok || (!currentEndpoint.ModificationTag.Equal(routingEndpoint.ModificationTag) && !currentEndpoint.ModificationTag.SucceededBy(routingEndpoint.ModificationTag)) {
		return MessagesToEmit{}
	}

	newEntry := currentEntry.copy()
	delete(newEntry.Endpoints, endpointKey)
	table.entries[key] = newEntry

	delete(table.addressEntries, routingEndpoint.address())

	return table.emit(key, currentEntry, newEntry)
}

func (table *natsRoutingTable) emit(key endpoint.RoutingKey, oldEntry, newEntry RoutableEndpoints) MessagesToEmit {
	var messagesToEmit MessagesToEmit
	messagesToEmit = table.messageBuilder.RegistrationsFor(&oldEntry, &newEntry)
	messagesToEmit = messagesToEmit.Merge(table.messageBuilder.UnregistrationsFor(&oldEntry, &newEntry, nil))

	return messagesToEmit
}
