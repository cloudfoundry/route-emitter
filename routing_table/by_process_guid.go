package routing_table

import (
	"errors"

	"github.com/cloudfoundry-incubator/receptor"
)

type RoutesByProcessGuid map[string]Routes
type ContainersByProcessGuid map[string][]Container

func RoutesByProcessGuidFromDesireds(desireds []receptor.DesiredLRPResponse) RoutesByProcessGuid {
	routesByProcessGuid := RoutesByProcessGuid{}
	for _, desired := range desireds {
		routes, err := receptor.CFRoutesFromRoutingInfo(desired.Routes)
		if err == nil && len(routes) > 0 {
			routesByProcessGuid[desired.ProcessGuid] = Routes{
				URIs:    routes[0].Hostnames,
				LogGuid: desired.LogGuid,
			}
		}
	}

	return routesByProcessGuid
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
