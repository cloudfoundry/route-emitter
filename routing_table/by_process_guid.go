package routing_table

import (
	"errors"

	"github.com/cloudfoundry-incubator/runtime-schema/models"
)

type RoutesByProcessGuid map[string]Routes
type ContainersByProcessGuid map[string][]Container

func RoutesByProcessGuidFromDesireds(desireds []models.DesiredLRP) RoutesByProcessGuid {
	routes := RoutesByProcessGuid{}
	for _, desired := range desireds {
		routes[desired.ProcessGuid] = Routes{
			URIs:    desired.Routes,
			LogGuid: desired.LogGuid,
		}
	}

	return routes
}

func ContainersByProcessGuidFromActuals(actuals []models.ActualLRP) ContainersByProcessGuid {
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

func ContainerFromActual(actual models.ActualLRP) (Container, error) {
	if len(actual.Ports) == 0 {
		return Container{}, errors.New("missing ports")
	}

	return Container{
		InstanceGuid: actual.InstanceGuid,
		Host:         actual.Host,
		Port:         uint16(actual.Ports[0].HostPort),
	}, nil
}
