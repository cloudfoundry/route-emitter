package routing_table

type Endpoint struct {
	InstanceGuid string
	Host         string
	Port         uint16
	ContainerPort uint16
}

type Routes struct {
	URIs    []string
	LogGuid string
}

type RoutingTableEntry struct {
	URIs      map[string]struct{}
	Endpoints map[string]Endpoint
	LogGuid   string
}

func (entry RoutingTableEntry) hasEndpoint(endpoint Endpoint) bool {
	_, ok := entry.Endpoints[endpoint.InstanceGuid]
	return ok
}

func (entry RoutingTableEntry) hasURI(uri string) bool {
	_, ok := entry.URIs[uri]
	return ok
}

func (entry RoutingTableEntry) copy() RoutingTableEntry {
	clone := RoutingTableEntry{
		URIs:      map[string]struct{}{},
		Endpoints: map[string]Endpoint{},
		LogGuid:   entry.LogGuid,
	}

	for k, v := range entry.URIs {
		clone.URIs[k] = v
	}

	for k, v := range entry.Endpoints {
		clone.Endpoints[k] = v
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

func endpointsAsMap(endpoints []Endpoint) map[string]Endpoint {
	endpointsMap := map[string]Endpoint{}
	for _, endpoint := range endpoints {
		endpointsMap[endpoint.InstanceGuid] = endpoint
	}
	return endpointsMap
}
