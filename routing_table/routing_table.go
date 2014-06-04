package routing_table

import "sync"

type RoutingTableInterface interface {
	Sync(routes RoutesByProcessGuid, containers ContainersByProcessGuid) MessagesToEmit
	MessagesToEmit() MessagesToEmit

	SetRoutes(processGuid string, routes ...string) MessagesToEmit
	RemoveRoutes(processGuid string) MessagesToEmit
	AddOrUpdateContainer(processGuid string, container Container) MessagesToEmit
	RemoveContainer(processGuid string, container Container) MessagesToEmit
}

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

	for _, newEntry := range newEntries {
		//always register everything on sync
		messagesToEmit = messagesToEmit.merge(registrationsFor(newEntry))
	}

	for processGuid, existingEntry := range table.entries {
		newEntry := newEntries[processGuid]
		messagesToEmit = messagesToEmit.merge(unregistrationsForTransition(existingEntry, newEntry))
	}

	table.entries = newEntries

	return messagesToEmit
}

func (table *RoutingTable) MessagesToEmit() MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	messagesToEmit := MessagesToEmit{}
	for _, entry := range table.entries {
		messagesToEmit = messagesToEmit.merge(registrationsFor(entry))
	}
	return messagesToEmit
}

func (table *RoutingTable) SetRoutes(processGuid string, routes ...string) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	newEntry := table.entries[processGuid].copy()
	newEntry.Routes = routesAsMap(routes)

	return table.updateEntry(processGuid, newEntry)
}

func (table *RoutingTable) RemoveRoutes(processGuid string) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	newEntry := table.entries[processGuid].copy()
	newEntry.Routes = routesAsMap([]string{})

	return table.updateEntry(processGuid, newEntry)
}

func (table *RoutingTable) AddOrUpdateContainer(processGuid string, container Container) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	newEntry := table.entries[processGuid].copy()
	newEntry.Containers[container] = struct{}{}

	return table.updateEntry(processGuid, newEntry)
}

func (table *RoutingTable) RemoveContainer(processGuid string, container Container) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	newEntry := table.entries[processGuid].copy()
	delete(newEntry.Containers, container)

	return table.updateEntry(processGuid, newEntry)
}

func (table *RoutingTable) updateEntry(processGuid string, newEntry RoutingTableEntry) MessagesToEmit {
	existingEntry := table.entries[processGuid]

	messagesToEmit := registrationsForTransition(existingEntry, newEntry)
	messagesToEmit = messagesToEmit.merge(unregistrationsForTransition(existingEntry, newEntry))

	table.entries[processGuid] = newEntry
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

func registrationsFor(entry RoutingTableEntry) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}
	if len(entry.Routes) == 0 {
		return messagesToEmit
	}
	for container := range entry.Containers {
		message := RegistryMessageFor(container, entry.allRoutes()...)
		messagesToEmit.RegistrationMessages = append(messagesToEmit.RegistrationMessages, message)
	}
	return messagesToEmit
}

func registrationsForTransition(existingEntry RoutingTableEntry, newEntry RoutingTableEntry) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}

	if len(newEntry.Routes) == 0 {
		//no routes, so nothing could possibly be registered
		return messagesToEmit
	}

	if routesHaveChanged(existingEntry, newEntry) {
		//register everything
		return registrationsFor(newEntry)
	}

	//otherwise only register *new* containers
	for container := range newEntry.Containers {
		if !existingEntry.hasContainer(container) {
			message := RegistryMessageFor(container, newEntry.allRoutes()...)
			messagesToEmit.RegistrationMessages = append(messagesToEmit.RegistrationMessages, message)
		}
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
		if newEntry.hasContainer(container) {
			containersThatAreStillPresent = append(containersThatAreStillPresent, container)
		} else {
			//if the container has disappeared unregister all its previous routes
			message := RegistryMessageFor(container, existingEntry.allRoutes()...)
			messagesToEmit.UnregistrationMessages = append(messagesToEmit.UnregistrationMessages, message)
		}
	}

	routesThatDisappeared := []string{}
	for route := range existingEntry.Routes {
		if !newEntry.hasRoute(route) {
			routesThatDisappeared = append(routesThatDisappeared, route)
		}
	}

	if len(routesThatDisappeared) > 0 {
		for _, container := range containersThatAreStillPresent {
			//if a container is still present, and routes have disappeared, unregister those routes
			message := RegistryMessageFor(container, routesThatDisappeared...)
			messagesToEmit.UnregistrationMessages = append(messagesToEmit.UnregistrationMessages, message)
		}
	}

	return messagesToEmit
}

func routesHaveChanged(existingEntry RoutingTableEntry, newEntry RoutingTableEntry) bool {
	if len(newEntry.Routes) != len(existingEntry.Routes) {
		return true
	} else {
		for route := range newEntry.Routes {
			if !existingEntry.hasRoute(route) {
				return true
			}
		}
	}

	return false
}
