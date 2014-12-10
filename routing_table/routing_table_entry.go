package routing_table

type Container struct {
	Host string
	Port uint16
}

type RoutingTableEntry struct {
	Routes     map[string]struct{}
	Containers map[Container]struct{}
}

func (entry RoutingTableEntry) hasContainer(container Container) bool {
	_, ok := entry.Containers[container]
	return ok
}

func (entry RoutingTableEntry) hasRoute(route string) bool {
	_, ok := entry.Routes[route]
	return ok
}

func (entry RoutingTableEntry) copy() RoutingTableEntry {
	clone := RoutingTableEntry{
		Routes:     map[string]struct{}{},
		Containers: map[Container]struct{}{},
	}

	for k, v := range entry.Routes {
		clone.Routes[k] = v
	}

	for k, v := range entry.Containers {
		clone.Containers[k] = v
	}

	return clone
}

func (entry RoutingTableEntry) allRoutes() []string {
	routes := make([]string, len(entry.Routes))
	i := 0
	for route := range entry.Routes {
		routes[i] = route
		i++
	}
	return routes
}

func routesAsMap(routes []string) map[string]struct{} {
	routesMap := map[string]struct{}{}
	for _, route := range routes {
		routesMap[route] = struct{}{}
	}
	return routesMap
}

func containersAsMap(containers []Container) map[Container]struct{} {
	containersMap := map[Container]struct{}{}
	for _, container := range containers {
		containersMap[container] = struct{}{}
	}
	return containersMap
}
