package routing_table

import (
	"errors"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/cc_messages"
)

type RoutesByProcessGuid map[string]Routes
type ContainersByProcessGuid map[string][]Container

func RoutesByProcessGuidFromDesireds(desireds []receptor.DesiredLRPResponse) RoutesByProcessGuid {
	routes := RoutesByProcessGuid{}
	for _, desired := range desireds {
		route := cc_messages.RouteFromRoutingInfo(desired.Routes)
		routes[desired.ProcessGuid] = Routes{
			URIs:    route.Hostnames,
			LogGuid: desired.LogGuid,
		}
	}

	return routes
}

func ContainersByProcessGuidFromActuals(actuals []receptor.ActualLRPResponse) ContainersByProcessGuid {
	containers := ContainersByProcessGuid{}
	for _, actual := range actuals {
		container, err := ContainerFromActual(actual)
		if err != nil {
			continue
		}

		containers[actual.ProcessGuid] = append(containers[actual.ProcessGuid], container)
	}

	return containers
}

func ContainerFromActual(actual receptor.ActualLRPResponse) (Container, error) {
	if len(actual.Ports) == 0 {
		return Container{}, errors.New("missing ports")
	}

	return Container{
		InstanceGuid: actual.InstanceGuid,
		Host:         actual.Address,
		Port:         uint16(actual.Ports[0].HostPort),
	}, nil
}
