package routing_table

import "code.cloudfoundry.org/bbs/models"

type EndpointKey struct {
	Index      int32
	Evacuating bool
}

type Address struct {
	Host string
	Port uint32
}

type Endpoint struct {
	InstanceGuid    string
	Index           int32
	Host            string
	Domain          string
	Port            uint32
	ContainerPort   uint32
	Evacuating      bool
	ModificationTag *models.ModificationTag
}

func (e Endpoint) key() EndpointKey {
	return EndpointKey{Index: e.Index, Evacuating: e.Evacuating}
}

func (e Endpoint) address() Address {
	return Address{Host: e.Host, Port: e.Port}
}

type Routes struct {
	Hostnames       []string
	LogGuid         string
	RouteServiceUrl string
	ModificationTag *models.ModificationTag
}

type RoutableEndpoints struct {
	Hostnames       map[string]struct{}
	Endpoints       map[EndpointKey]Endpoint
	LogGuid         string
	ModificationTag *models.ModificationTag
	RouteServiceUrl string
}

type RoutingKey struct {
	ProcessGuid   string
	ContainerPort uint32
}

func NewRoutableEndpoints() RoutableEndpoints {
	return RoutableEndpoints{
		Hostnames: map[string]struct{}{},
		Endpoints: map[EndpointKey]Endpoint{},
	}
}

func (entry RoutableEndpoints) hasEndpoint(endpoint Endpoint) bool {
	key := endpoint.key()
	_, found := entry.Endpoints[key]
	if !found {
		key.Evacuating = !key.Evacuating
		_, found = entry.Endpoints[key]
	}
	return found
}

func (entry RoutableEndpoints) hasHostname(hostname string) bool {
	_, ok := entry.Hostnames[hostname]
	return ok
}

func (entry RoutableEndpoints) copy() RoutableEndpoints {
	clone := RoutableEndpoints{
		Hostnames:       map[string]struct{}{},
		Endpoints:       map[EndpointKey]Endpoint{},
		LogGuid:         entry.LogGuid,
		ModificationTag: entry.ModificationTag,
		RouteServiceUrl: entry.RouteServiceUrl,
	}

	for k, v := range entry.Hostnames {
		clone.Hostnames[k] = v
	}

	for k, v := range entry.Endpoints {
		clone.Endpoints[k] = v
	}

	return clone
}

func (entry RoutableEndpoints) routes() Routes {
	hostnames := make([]string, len(entry.Hostnames))

	i := 0
	for hostname := range entry.Hostnames {
		hostnames[i] = hostname
		i++
	}

	return Routes{
		Hostnames:       hostnames,
		LogGuid:         entry.LogGuid,
		RouteServiceUrl: entry.RouteServiceUrl,
	}
}

func routesAsMap(routes []string) map[string]struct{} {
	routesMap := map[string]struct{}{}
	for _, route := range routes {
		routesMap[route] = struct{}{}
	}
	return routesMap
}

func EndpointsAsMap(endpoints []Endpoint) map[EndpointKey]Endpoint {
	endpointsMap := map[EndpointKey]Endpoint{}
	for _, endpoint := range endpoints {
		endpointsMap[endpoint.key()] = endpoint
	}
	return endpointsMap
}
