package routing_table

type Container struct {
	Host string
	Port uint16
}

type Routes struct {
	URIs    []string
	LogGuid string
}

type RoutingTableEntry struct {
	URIs       map[string]struct{}
	Containers map[Container]struct{}
	LogGuid    string
}

func (entry RoutingTableEntry) hasContainer(container Container) bool {
	_, ok := entry.Containers[container]
	return ok
}

func (entry RoutingTableEntry) hasURI(uri string) bool {
	_, ok := entry.URIs[uri]
	return ok
}

func (entry RoutingTableEntry) copy() RoutingTableEntry {
	clone := RoutingTableEntry{
		URIs:       map[string]struct{}{},
		Containers: map[Container]struct{}{},
		LogGuid:    entry.LogGuid,
	}

	for k, v := range entry.URIs {
		clone.URIs[k] = v
	}

	for k, v := range entry.Containers {
		clone.Containers[k] = v
	}

	return clone
}

func (entry RoutingTableEntry) routes() Routes {
	uris := make([]string, len(entry.URIs))

	i := 0
	for uri := range entry.URIs {
		uris[i] = uri
		i++
	}

	return Routes{
		URIs:    uris,
		LogGuid: entry.LogGuid,
	}
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
