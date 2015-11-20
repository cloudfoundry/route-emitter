package routing_table

import (
	"sync"

	"github.com/cloudfoundry-incubator/bbs/models"
)

//go:generate counterfeiter -o fake_routing_table/fake_routing_table.go . RoutingTable
type RoutingTable interface {
	RouteCount() int

	Swap(newTable RoutingTable, domains models.DomainSet) MessagesToEmit

	SetRoutes(key RoutingKey, routes Routes) MessagesToEmit
	RemoveRoutes(key RoutingKey, modTag *models.ModificationTag) MessagesToEmit
	AddEndpoint(key RoutingKey, endpoint Endpoint) MessagesToEmit
	RemoveEndpoint(key RoutingKey, endpoint Endpoint) MessagesToEmit

	MessagesToEmit() MessagesToEmit
}

type noopLocker struct{}

func (noopLocker) Lock()   {}
func (noopLocker) Unlock() {}

type routingTable struct {
	entries map[RoutingKey]RoutableEndpoints
	sync.Locker
	messageBuilder MessageBuilder
}

func NewTempTable(routes RoutesByRoutingKey, endpoints EndpointsByRoutingKey) RoutingTable {
	entries := make(map[RoutingKey]RoutableEndpoints)

	for key, entry := range routes {
		entries[key] = RoutableEndpoints{
			Hostnames:       routesAsMap(entry.Hostnames),
			LogGuid:         entry.LogGuid,
			RouteServiceUrl: entry.RouteServiceUrl,
		}
	}

	for key, endpoints := range endpoints {
		entry, ok := entries[key]
		if !ok {
			entry = RoutableEndpoints{}
		}
		entry.Endpoints = EndpointsAsMap(endpoints)
		entries[key] = entry
	}

	return &routingTable{
		entries:        entries,
		Locker:         noopLocker{},
		messageBuilder: NoopMessageBuilder{},
	}
}

func NewTable() RoutingTable {
	return &routingTable{
		entries:        make(map[RoutingKey]RoutableEndpoints),
		Locker:         &sync.Mutex{},
		messageBuilder: MessagesToEmitBuilder{},
	}
}

func (table *routingTable) RouteCount() int {
	table.Lock()

	count := 0
	for _, entry := range table.entries {
		count += len(entry.Hostnames)
	}

	table.Unlock()
	return count
}

func (table *routingTable) Swap(t RoutingTable, domains models.DomainSet) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}

	newTable, ok := t.(*routingTable)
	if !ok {
		return messagesToEmit
	}
	newEntries := newTable.entries
	updatedEntries := make(map[RoutingKey]RoutableEndpoints)

	table.Lock()
	for key, newEntry := range newEntries {
		// See if we have a match
		existingEntry, _ := table.entries[key]

		//always register everything on sync  NOTE if a merge does occur we may return an altered newEntry
		messagesToEmit = messagesToEmit.merge(table.messageBuilder.MergedRegistrations(&existingEntry, &newEntry, domains))
		updatedEntries[key] = newEntry
	}

	for key, existingEntry := range table.entries {
		newEntry, ok := newEntries[key]
		messagesToEmit = messagesToEmit.merge(table.messageBuilder.UnregistrationsFor(&existingEntry, &newEntry, domains))

		// maybe reemit old ones no longer found in the new table
		if !ok {
			unfreshRegistrations := table.messageBuilder.UnfreshRegistrations(&existingEntry, domains)
			if len(unfreshRegistrations.RegistrationMessages) > 0 {
				updatedEntries[key] = existingEntry
				messagesToEmit = messagesToEmit.merge(unfreshRegistrations)
			}
		}
	}

	table.entries = updatedEntries
	table.Unlock()

	return messagesToEmit
}

func (table *routingTable) MessagesToEmit() MessagesToEmit {
	table.Lock()

	messagesToEmit := MessagesToEmit{}
	for _, entry := range table.entries {
		messagesToEmit = messagesToEmit.merge(table.messageBuilder.RegistrationsFor(nil, &entry))
	}

	table.Unlock()
	return messagesToEmit
}

func (table *routingTable) SetRoutes(key RoutingKey, routes Routes) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	currentEntry := table.entries[key]
	if !currentEntry.ModificationTag.SucceededBy(routes.ModificationTag) {
		return MessagesToEmit{}
	}

	newEntry := currentEntry.copy()
	newEntry.Hostnames = routesAsMap(routes.Hostnames)
	newEntry.LogGuid = routes.LogGuid
	newEntry.ModificationTag = routes.ModificationTag
	newEntry.RouteServiceUrl = routes.RouteServiceUrl

	table.entries[key] = newEntry

	return table.emit(key, currentEntry, newEntry)
}

func (table *routingTable) RemoveRoutes(key RoutingKey, modTag *models.ModificationTag) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	currentEntry := table.entries[key]
	if !(currentEntry.ModificationTag.Equal(modTag) || currentEntry.ModificationTag.SucceededBy(modTag)) {
		return MessagesToEmit{}
	}

	newEntry := NewRoutableEndpoints()
	newEntry.Endpoints = currentEntry.Endpoints

	table.entries[key] = currentEntry

	return table.emit(key, currentEntry, newEntry)
}

func (table *routingTable) AddEndpoint(key RoutingKey, endpoint Endpoint) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	currentEntry := table.entries[key]
	newEntry := currentEntry.copy()
	newEntry.Endpoints[endpoint.key()] = endpoint
	table.entries[key] = newEntry

	return table.emit(key, currentEntry, newEntry)
}

func (table *routingTable) RemoveEndpoint(key RoutingKey, endpoint Endpoint) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	currentEntry := table.entries[key]
	endpointKey := endpoint.key()
	currentEndpoint, ok := currentEntry.Endpoints[endpointKey]
	if !ok || !(currentEndpoint.ModificationTag.Equal(endpoint.ModificationTag) || currentEndpoint.ModificationTag.SucceededBy(endpoint.ModificationTag)) {
		return MessagesToEmit{}
	}

	newEntry := currentEntry.copy()
	delete(newEntry.Endpoints, endpointKey)
	table.entries[key] = newEntry

	return table.emit(key, currentEntry, newEntry)
}

func (table *routingTable) emit(key RoutingKey, oldEntry RoutableEndpoints, newEntry RoutableEndpoints) MessagesToEmit {
	messagesToEmit := table.messageBuilder.RegistrationsFor(&oldEntry, &newEntry)
	messagesToEmit = messagesToEmit.merge(table.messageBuilder.UnregistrationsFor(&oldEntry, &newEntry, nil))

	return messagesToEmit
}
