package routingtable

import (
	"fmt"

	tcpmodels "code.cloudfoundry.org/routing-api/models"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/routing-info/tcp_routes"
)

type EndpointKey struct {
	InstanceGUID string
	Evacuating   bool
}

func (key *EndpointKey) String() string {
	return fmt.Sprintf(`{"InstanceGUID": "%s", "Evacuating": %t}`, key.InstanceGUID, key.Evacuating)
}

func NewEndpointKey(instanceGUID string, evacuating bool) EndpointKey {
	return EndpointKey{
		InstanceGUID: instanceGUID,
		Evacuating:   evacuating,
	}
}

type Address struct {
	Host string
	Port uint32
}

type Endpoint struct {
	InstanceGUID          string
	Index                 int32
	Host                  string
	ContainerIP           string
	Port                  uint32
	ContainerPort         uint32
	TlsProxyPort          uint32
	ContainerTlsProxyPort uint32
	Evacuating            bool
	IsolationSegment      string
	Since                 int64
	ModificationTag       *models.ModificationTag
}

func (e Endpoint) key() EndpointKey {
	return EndpointKey{InstanceGUID: e.InstanceGUID, Evacuating: e.Evacuating}
}

func (e Endpoint) address() Address {
	return Address{Host: e.Host, Port: e.Port}
}

func NewEndpoint(
	instanceGUID string, evacuating bool,
	host, containerIP string,
	port, containerPort uint32,
	modificationTag *models.ModificationTag,
) Endpoint {
	return Endpoint{
		InstanceGUID:    instanceGUID,
		Evacuating:      evacuating,
		Host:            host,
		ContainerIP:     containerIP,
		Port:            port,
		ContainerPort:   containerPort,
		ModificationTag: modificationTag,
	}
}

type ExternalEndpointInfo struct {
	RouterGroupGUID string
	Port            uint32
}

func (info ExternalEndpointInfo) MessageFor(e Endpoint, directInstanceRoute, _ bool) (*RegistryMessage, *tcpmodels.TcpRouteMapping, *RegistryMessage) {
	mapping := tcpmodels.NewTcpRouteMapping(
		info.RouterGroupGUID,
		uint16(info.Port),
		e.Host,
		uint16(e.Port),
		0,
	)
	if directInstanceRoute {
		mapping = tcpmodels.NewTcpRouteMapping(
			info.RouterGroupGUID,
			uint16(info.Port),
			e.ContainerIP,
			uint16(e.ContainerPort),
			0,
		)
	}
	return nil, &mapping, nil
}

type ExternalEndpointInfos []ExternalEndpointInfo

func NewExternalEndpointInfo(routerGroupGUID string, port uint32) ExternalEndpointInfo {
	return ExternalEndpointInfo{
		RouterGroupGUID: routerGroupGUID,
		Port:            port,
	}
}

type Route struct {
	Hostname         string
	RouteServiceUrl  string
	IsolationSegment string
	LogGUID          string
}

func (r Route) MessageFor(endpoint Endpoint, directInstanceAddress, emitEndpointUpdatedAt bool) (*RegistryMessage, *tcpmodels.TcpRouteMapping, *RegistryMessage) {
	generator := RegistryMessageFor
	if directInstanceAddress {
		generator = InternalAddressRegistryMessageFor
	}
	msg := generator(endpoint, r, emitEndpointUpdatedAt)
	return &msg, nil, nil
}

type InternalRoute struct {
	Hostname    string
	ContainerIP string
	LogGUID     string
}

func (r InternalRoute) MessageFor(endpoint Endpoint, _, emitEndpointUpdatedAt bool) (*RegistryMessage, *tcpmodels.TcpRouteMapping, *RegistryMessage) {
	generator := InternalEndpointRegistryMessageFor
	msg := generator(endpoint, r, emitEndpointUpdatedAt)
	return nil, nil, &msg
}

func (entry RoutableEndpoints) copy() RoutableEndpoints {

	clone := RoutableEndpoints{
		Domain:           entry.Domain,
		Endpoints:        map[EndpointKey]Endpoint{},
		Routes:           make([]routeMapping, len(entry.Routes)),
		DesiredInstances: entry.DesiredInstances,
		ModificationTag:  entry.ModificationTag,
	}

	copy(clone.Routes, entry.Routes)

	for k, v := range entry.Endpoints {
		clone.Endpoints[k] = v
	}

	return clone
}

type RoutableEndpoints struct {
	Domain           string
	Routes           []routeMapping
	Endpoints        map[EndpointKey]Endpoint
	DesiredInstances int32
	ModificationTag  *models.ModificationTag
}

type InternalRoutableEndpoints struct {
	Routes           []InternalRoute
	Endpoints        map[EndpointKey]Endpoint
	DesiredInstances int32
	ModificationTag  *models.ModificationTag
}

func (entry InternalRoutableEndpoints) copy() InternalRoutableEndpoints {
	clone := InternalRoutableEndpoints{
		Endpoints:        map[EndpointKey]Endpoint{},
		Routes:           make([]InternalRoute, len(entry.Routes)),
		DesiredInstances: entry.DesiredInstances,
		ModificationTag:  entry.ModificationTag,
	}

	copy(clone.Routes, entry.Routes)

	for k, v := range entry.Endpoints {
		clone.Endpoints[k] = v
	}

	return clone
}

func NewEndpointsFromActual(actualLRP *models.FlattenedActualLRP) []Endpoint {
	endpoints := []Endpoint{}

	for _, portMapping := range actualLRP.Ports {
		if portMapping != nil {
			endpoint := Endpoint{
				InstanceGUID:          actualLRP.InstanceGuid,
				Index:                 actualLRP.Index,
				Host:                  actualLRP.Address,
				ContainerIP:           actualLRP.InstanceAddress,
				Port:                  portMapping.HostPort,
				ContainerPort:         portMapping.ContainerPort,
				Evacuating:            actualLRP.ActualLRPInfo.PlacementState == models.PlacementStateType_Evacuating,
				ModificationTag:       &actualLRP.ModificationTag,
				TlsProxyPort:          portMapping.HostTlsProxyPort,
				ContainerTlsProxyPort: portMapping.ContainerTlsProxyPort,
				Since: actualLRP.Since,
			}
			endpoints = append(endpoints, endpoint)
		}
	}

	return endpoints
}

func NewRoutingKeysFromActual(actualLRP *models.FlattenedActualLRP) RoutingKeys {
	keys := RoutingKeys{}
	for _, portMapping := range actualLRP.Ports {
		keys = append(keys, NewRoutingKey(actualLRP.ProcessGuid, portMapping.ContainerPort))

		if portMapping.HostTlsProxyPort != 0 && portMapping.ContainerTlsProxyPort != 0 {
			keys = append(keys, NewRoutingKey(actualLRP.ProcessGuid, portMapping.ContainerTlsProxyPort))
		}
	}

	return keys
}

func NewRoutingKeysFromDesired(desired *models.DesiredLRPSchedulingInfo) RoutingKeys {
	keys := RoutingKeys{}
	routes, err := tcp_routes.TCPRoutesFromRoutingInfo(&desired.Routes)
	if err != nil {
		return keys
	}
	for _, r := range routes {
		keys = append(keys, NewRoutingKey(desired.ProcessGuid, r.ContainerPort))
	}

	return keys
}

func (e ExternalEndpointInfos) HasNoExternalPorts(logger lager.Logger) bool {
	if e == nil || len(e) == 0 {
		logger.Debug("no-external-port")
		return true
	}
	// This originally checked if Port was 0, I think to see if it was a zero value, check and make sure
	return false
}

type RoutingKeys []RoutingKey

type RoutingKey struct {
	ProcessGUID   string
	ContainerPort uint32
}

func NewRoutingKey(processGUID string, containerPort uint32) RoutingKey {
	return RoutingKey{
		ProcessGUID:   processGUID,
		ContainerPort: containerPort,
	}
}

func (e ExternalEndpointInfos) ContainsExternalPort(port uint32) bool {
	for _, existing := range e {
		if existing.Port == port {
			return true
		}
	}
	return false
}

func (lhs RoutingKeys) Remove(rhs RoutingKeys) RoutingKeys {
	result := RoutingKeys{}
	for _, lhsKey := range lhs {
		if !rhs.containsRoutingKey(lhsKey) {
			result = append(result, lhsKey)
		}
	}
	return result
}

func (lhs RoutingKeys) containsRoutingKey(routingKey RoutingKey) bool {
	for _, lhsKey := range lhs {
		if lhsKey == routingKey {
			return true
		}
	}
	return false
}
