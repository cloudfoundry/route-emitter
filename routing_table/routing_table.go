package routing_table

import "sync"

//go:generate counterfeiter -o fake_routing_table/fake_routing_table.go . RoutingTable
type RoutingTable interface {
	Sync(routes RoutesByRoutingKey, endpoints EndpointsByRoutingKey) MessagesToEmit
	MessagesToEmit() MessagesToEmit

	RouteCount() int
	SetRoutes(key RoutingKey, routes Routes) MessagesToEmit
	RemoveRoutes(key RoutingKey) MessagesToEmit
	AddOrUpdateEndpoint(key RoutingKey, endpoint Endpoint) MessagesToEmit
	RemoveEndpoint(key RoutingKey, endpoint Endpoint) MessagesToEmit
}

type routingTable struct {
	entries map[RoutingKey]RoutingTableEntry
	sync.Mutex
}

func New() RoutingTable {
	return &routingTable{
		entries: map[RoutingKey]RoutingTableEntry{},
	}
}

func (table *routingTable) RouteCount() int {
	table.Lock()
	defer table.Unlock()

	count := 0
	for _, entry := range table.entries {
		count += len(entry.URIs)
	}

	return count
}

func (table *routingTable) Sync(routes RoutesByRoutingKey, endpoints EndpointsByRoutingKey) MessagesToEmit {
	newEntries := combineByRoutingKey(routes, endpoints)

	table.Lock()
	defer table.Unlock()

	messagesToEmit := MessagesToEmit{}

	for _, newEntry := range newEntries {
		//always register everything on sync
		messagesToEmit = messagesToEmit.merge(registrationsFor(newEntry))
	}

	for key, existingEntry := range table.entries {
		newEntry := newEntries[key]
		messagesToEmit = messagesToEmit.merge(unregistrationsForTransition(existingEntry, newEntry))
	}

	table.entries = newEntries

	return messagesToEmit
}

func (table *routingTable) MessagesToEmit() MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	messagesToEmit := MessagesToEmit{}
	for _, entry := range table.entries {
		messagesToEmit = messagesToEmit.merge(registrationsFor(entry))
	}
	return messagesToEmit
}

func (table *routingTable) SetRoutes(key RoutingKey, routes Routes) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	newEntry := table.entries[key].copy()
	newEntry.URIs = routesAsMap(routes.URIs)
	newEntry.LogGuid = routes.LogGuid

	return table.updateEntry(key, newEntry)
}

func (table *routingTable) RemoveRoutes(key RoutingKey) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	newEntry := table.entries[key].copy()
	newEntry.URIs = routesAsMap([]string{})

	return table.updateEntry(key, newEntry)
}

func (table *routingTable) AddOrUpdateEndpoint(key RoutingKey, endpoint Endpoint) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	newEntry := table.entries[key].copy()
	newEntry.Endpoints[endpoint.InstanceGuid] = endpoint

	return table.updateEntry(key, newEntry)
}

func (table *routingTable) RemoveEndpoint(key RoutingKey, endpoint Endpoint) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	newEntry := table.entries[key].copy()
	delete(newEntry.Endpoints, endpoint.InstanceGuid)

	return table.updateEntry(key, newEntry)
}

func (table *routingTable) updateEntry(key RoutingKey, newEntry RoutingTableEntry) MessagesToEmit {
	existingEntry := table.entries[key]

	messagesToEmit := registrationsForTransition(existingEntry, newEntry)
	messagesToEmit = messagesToEmit.merge(unregistrationsForTransition(existingEntry, newEntry))

	table.entries[key] = newEntry
	return messagesToEmit
}

func combineByRoutingKey(routes RoutesByRoutingKey, endpoints EndpointsByRoutingKey) map[RoutingKey]RoutingTableEntry {
	entries := map[RoutingKey]RoutingTableEntry{}

	for key, entry := range routes {
		entries[key] = RoutingTableEntry{
			URIs:    routesAsMap(entry.URIs),
			LogGuid: entry.LogGuid,
		}
	}

	for key, endpoints := range endpoints {
		entry, ok := entries[key]
		if !ok {
			entry = RoutingTableEntry{}
		}
		entry.Endpoints = endpointsAsMap(endpoints)
		entries[key] = entry
	}

	return entries
}

func registrationsFor(entry RoutingTableEntry) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}
	if len(entry.URIs) == 0 {
		return messagesToEmit
	}

	for _, endpoint := range entry.Endpoints {
		message := RegistryMessageFor(endpoint, entry.routes())
		messagesToEmit.RegistrationMessages = append(messagesToEmit.RegistrationMessages, message)
	}
	return messagesToEmit
}

func registrationsForTransition(existingEntry RoutingTableEntry, newEntry RoutingTableEntry) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}

	if len(newEntry.URIs) == 0 {
		//no uris, so nothing could possibly be registered
		return messagesToEmit
	}

	if urisHaveChanged(existingEntry, newEntry) {
		//register everything
		return registrationsFor(newEntry)
	}

	//otherwise only register *new* endpoints
	for _, endpoint := range newEntry.Endpoints {
		if !existingEntry.hasEndpoint(endpoint) {
			message := RegistryMessageFor(endpoint, newEntry.routes())
			messagesToEmit.RegistrationMessages = append(messagesToEmit.RegistrationMessages, message)
		}
	}

	return messagesToEmit
}

func unregistrationsForTransition(existingEntry RoutingTableEntry, newEntry RoutingTableEntry) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}

	if len(existingEntry.URIs) == 0 {
		// the existing entry has no uris and so there is nothing to unregister
		return messagesToEmit
	}

	endpointsThatAreStillPresent := []Endpoint{}
	for _, endpoint := range existingEntry.Endpoints {
		if newEntry.hasEndpoint(endpoint) {
			endpointsThatAreStillPresent = append(endpointsThatAreStillPresent, endpoint)
		} else {
			//if the endpoint has disappeared unregister all its previous uris
			message := RegistryMessageFor(endpoint, existingEntry.routes())
			messagesToEmit.UnregistrationMessages = append(messagesToEmit.UnregistrationMessages, message)
		}
	}

	urisThatDisappeared := []string{}
	for uri := range existingEntry.URIs {
		if !newEntry.hasURI(uri) {
			urisThatDisappeared = append(urisThatDisappeared, uri)
		}
	}

	if len(urisThatDisappeared) > 0 {
		for _, endpoint := range endpointsThatAreStillPresent {
			//if a endpoint is still present, and uris have disappeared, unregister those uris
			message := RegistryMessageFor(endpoint, Routes{
				URIs:    urisThatDisappeared,
				LogGuid: newEntry.LogGuid,
			})
			messagesToEmit.UnregistrationMessages = append(messagesToEmit.UnregistrationMessages, message)
		}
	}

	return messagesToEmit
}

func urisHaveChanged(existingEntry RoutingTableEntry, newEntry RoutingTableEntry) bool {
	if len(newEntry.URIs) != len(existingEntry.URIs) {
		return true
	} else {
		for uri := range newEntry.URIs {
			if !existingEntry.hasURI(uri) {
				return true
			}
		}
	}

	return false
}
