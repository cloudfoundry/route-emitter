package routing_table

type EndpointKey struct {
	InstanceGuid string
	Evacuating   bool
}

type Endpoint struct {
	InstanceGuid  string
	Host          string
	Port          uint16
	ContainerPort uint16
	Evacuating    bool
}

func (e Endpoint) key() EndpointKey {
	return EndpointKey{InstanceGuid: e.InstanceGuid, Evacuating: e.Evacuating}
}

type Routes struct {
	Hostnames []string
	LogGuid   string
}

type RoutingTableEntry struct {
	Hostnames map[string]struct{}
	Endpoints map[EndpointKey]Endpoint
	LogGuid   string
}

type RoutingKey struct {
	ProcessGuid   string
	ContainerPort uint16
}

func (entry RoutingTableEntry) hasEndpoint(endpoint Endpoint) bool {
	key := endpoint.key()
	_, found := entry.Endpoints[key]
	if !found {
		key.Evacuating = !key.Evacuating
		_, found = entry.Endpoints[key]
	}
	return found
}

func (entry RoutingTableEntry) hasHostname(hostname string) bool {
	_, ok := entry.Hostnames[hostname]
	return ok
}

func (entry RoutingTableEntry) copy() RoutingTableEntry {
	clone := RoutingTableEntry{
		Hostnames: map[string]struct{}{},
		Endpoints: map[EndpointKey]Endpoint{},
		LogGuid:   entry.LogGuid,
	}

	for k, v := range entry.Hostnames {
		clone.Hostnames[k] = v
	}

	for k, v := range entry.Endpoints {
		clone.Endpoints[k] = v
	}

	return clone
}

func (entry RoutingTableEntry) routes() Routes {
	hostnames := make([]string, len(entry.Hostnames))

	i := 0
	for hostname := range entry.Hostnames {
		hostnames[i] = hostname
		i++
	}

	return Routes{
		Hostnames: hostnames,
		LogGuid:   entry.LogGuid,
	}
}

func routesAsMap(routes []string) map[string]struct{} {
	routesMap := map[string]struct{}{}
	for _, route := range routes {
		routesMap[route] = struct{}{}
	}
	return routesMap
}

func endpointsAsMap(endpoints []Endpoint) map[EndpointKey]Endpoint {
	endpointsMap := map[EndpointKey]Endpoint{}
	for _, endpoint := range endpoints {
		endpointsMap[endpoint.key()] = endpoint
	}
	return endpointsMap
}
