package routing_table

import "sync"

type RoutingTable interface {
	Sync(routes RoutesByProcessGuid, containers ContainersByProcessGuid) MessagesToEmit
	MessagesToEmit() MessagesToEmit

	RouteCount() int
	SetRoutes(processGuid string, routes Routes) MessagesToEmit
	RemoveRoutes(processGuid string) MessagesToEmit
	AddOrUpdateContainer(processGuid string, container Container) MessagesToEmit
	RemoveContainer(processGuid string, container Container) MessagesToEmit
}

type routingTable struct {
	entries map[string]RoutingTableEntry
	sync.Mutex
}

func New() RoutingTable {
	return &routingTable{
		entries: map[string]RoutingTableEntry{},
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

func (table *routingTable) Sync(routes RoutesByProcessGuid, containers ContainersByProcessGuid) MessagesToEmit {
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

func (table *routingTable) MessagesToEmit() MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	messagesToEmit := MessagesToEmit{}
	for _, entry := range table.entries {
		messagesToEmit = messagesToEmit.merge(registrationsFor(entry))
	}
	return messagesToEmit
}

func (table *routingTable) SetRoutes(processGuid string, routes Routes) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	newEntry := table.entries[processGuid].copy()
	newEntry.URIs = routesAsMap(routes.URIs)
	newEntry.LogGuid = routes.LogGuid

	return table.updateEntry(processGuid, newEntry)
}

func (table *routingTable) RemoveRoutes(processGuid string) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	newEntry := table.entries[processGuid].copy()
	newEntry.URIs = routesAsMap([]string{})

	return table.updateEntry(processGuid, newEntry)
}

func (table *routingTable) AddOrUpdateContainer(processGuid string, container Container) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	newEntry := table.entries[processGuid].copy()
	newEntry.Containers[container.InstanceGuid] = container

	return table.updateEntry(processGuid, newEntry)
}

func (table *routingTable) RemoveContainer(processGuid string, container Container) MessagesToEmit {
	table.Lock()
	defer table.Unlock()

	newEntry := table.entries[processGuid].copy()
	delete(newEntry.Containers, container.InstanceGuid)

	return table.updateEntry(processGuid, newEntry)
}

func (table *routingTable) updateEntry(processGuid string, newEntry RoutingTableEntry) MessagesToEmit {
	existingEntry := table.entries[processGuid]

	messagesToEmit := registrationsForTransition(existingEntry, newEntry)
	messagesToEmit = messagesToEmit.merge(unregistrationsForTransition(existingEntry, newEntry))

	table.entries[processGuid] = newEntry
	return messagesToEmit
}

func combineByProcessGuid(routes RoutesByProcessGuid, containers ContainersByProcessGuid) map[string]RoutingTableEntry {
	entries := map[string]RoutingTableEntry{}

	for processGuid, entry := range routes {
		entries[processGuid] = RoutingTableEntry{
			URIs:    routesAsMap(entry.URIs),
			LogGuid: entry.LogGuid,
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
	if len(entry.URIs) == 0 {
		return messagesToEmit
	}

	for _, container := range entry.Containers {
		message := RegistryMessageFor(container, entry.routes())
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

	//otherwise only register *new* containers
	for _, container := range newEntry.Containers {
		if !existingEntry.hasContainer(container) {
			message := RegistryMessageFor(container, newEntry.routes())
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

	containersThatAreStillPresent := []Container{}
	for _, container := range existingEntry.Containers {
		if newEntry.hasContainer(container) {
			containersThatAreStillPresent = append(containersThatAreStillPresent, container)
		} else {
			//if the container has disappeared unregister all its previous uris
			message := RegistryMessageFor(container, existingEntry.routes())
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
		for _, container := range containersThatAreStillPresent {
			//if a container is still present, and uris have disappeared, unregister those uris
			message := RegistryMessageFor(container, Routes{
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
