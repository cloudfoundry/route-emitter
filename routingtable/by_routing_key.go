package routingtable

import (
	"errors"

	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
)

func EndpointsFromActual(actualLRPInfo *endpoint.ActualLRPRoutingInfo) (map[uint32]Endpoint, error) {
	endpoints := map[uint32]Endpoint{}
	actual := actualLRPInfo.ActualLRP

	if len(actual.Ports) == 0 {
		return endpoints, errors.New("missing ports")
	}

	for _, portMapping := range actual.Ports {
		if portMapping != nil {
			endpoint := Endpoint{
				InstanceGuid:    actual.InstanceGuid,
				Index:           actual.Index,
				Host:            actual.Address,
				ContainerIP:     actual.InstanceAddress,
				Domain:          actual.Domain,
				Port:            portMapping.HostPort,
				ContainerPort:   portMapping.ContainerPort,
				Evacuating:      actualLRPInfo.Evacuating,
				ModificationTag: &actual.ModificationTag,
			}
			endpoints[portMapping.ContainerPort] = endpoint
		}
	}

	return endpoints, nil
}
