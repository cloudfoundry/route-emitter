package routing_table

import "sync"

type RoutingTable struct {
	entries map[string]RoutingTableEntry
	sync.Mutex
}

func New() *RoutingTable {
	return &RoutingTable{
		entries: map[string]RoutingTableEntry{},
	}
}

func (table *RoutingTable) Sync(routes RoutesByProcessGuid, containers ContainersByProcessGuid) MessagesToEmit {
	newEntries := combineByProcessGuid(routes, containers)

	table.Lock()
	defer table.Unlock()

	messagesToEmit := MessagesToEmit{}

	for processGuid, existingEntry := range table.entries {
		newEntry, stillPresent := newEntries[processGuid]
		if !stillPresent {
			messagesToEmit = messagesToEmit.merge(unregisterAll(existingEntry))
			continue
		}

		messagesToEmit = messagesToEmit.merge(registrationsFor(newEntry))
		messagesToEmit = messagesToEmit.merge(unregistrationsForTransition(existingEntry, newEntry))
	}

	for processGuid, newEntry := range newEntries {
		_, alreadyPresent := table.entries[processGuid]
		if !alreadyPresent {
			messagesToEmit = messagesToEmit.merge(registrationsFor(newEntry))
		}
	}

	table.entries = newEntries

	return messagesToEmit
}

func combineByProcessGuid(routes RoutesByProcessGuid, containers ContainersByProcessGuid) map[string]RoutingTableEntry {
	entries := map[string]RoutingTableEntry{}

	for processGuid, routes := range routes {
		entries[processGuid] = RoutingTableEntry{
			Routes: routesAsMap(routes),
		}
	}

	for processGuid, containers := range containers {
		entry, ok := entries[processGuid]
		if !ok {
			entry = RoutingTableEntry{}
		}
		entry.Containers = containersAsMap(containers)
		entries[processGuid] = entry
	}

	return entries
}

func unregisterAll(entry RoutingTableEntry) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}
	if len(entry.Routes) == 0 {
		return messagesToEmit
	}
	for container := range entry.Containers {
		message := RegistryMessageFor(container, entry.AllRoutes()...)
		messagesToEmit.UnregistrationMessages = append(messagesToEmit.UnregistrationMessages, message)
	}
	return messagesToEmit
}

func registrationsFor(entry RoutingTableEntry) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}
	if len(entry.Routes) == 0 {
		return messagesToEmit
	}
	for container := range entry.Containers {
		message := RegistryMessageFor(container, entry.AllRoutes()...)
		messagesToEmit.RegistrationMessages = append(messagesToEmit.RegistrationMessages, message)
	}
	return messagesToEmit
}

func unregistrationsForTransition(existingEntry RoutingTableEntry, newEntry RoutingTableEntry) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}

	if len(existingEntry.Routes) == 0 {
		// the existing entry has no routes and so there is nothing to unregister
		return messagesToEmit
	}

	containersThatAreStillPresent := []Container{}
	for container := range existingEntry.Containers {
		if newEntry.HasContainer(container) {
			containersThatAreStillPresent = append(containersThatAreStillPresent, container)
		} else {
			message := RegistryMessageFor(container, existingEntry.AllRoutes()...)
			messagesToEmit.UnregistrationMessages = append(messagesToEmit.UnregistrationMessages, message)
		}
	}

	routesThatDisappeared := []string{}
	for route := range existingEntry.Routes {
		if !newEntry.HasRoute(route) {
			routesThatDisappeared = append(routesThatDisappeared, route)
		}
	}

	if len(routesThatDisappeared) > 0 {
		for _, container := range containersThatAreStillPresent {
			message := RegistryMessageFor(container, routesThatDisappeared...)
			messagesToEmit.UnregistrationMessages = append(messagesToEmit.UnregistrationMessages, message)
		}
	}

	return messagesToEmit
}
