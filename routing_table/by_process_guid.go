package routing_table

import (
	"errors"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
)

type RoutesByProcessGuid map[string]Routes
type EndpointsByProcessGuid map[string][]Endpoint

func RoutesByProcessGuidFromDesireds(desireds []receptor.DesiredLRPResponse) RoutesByProcessGuid {
	routesByProcessGuid := RoutesByProcessGuid{}
	for _, desired := range desireds {
		routes, err := cfroutes.CFRoutesFromRoutingInfo(desired.Routes)
		if err == nil && len(routes) > 0 {
			routesByProcessGuid[desired.ProcessGuid] = Routes{
				URIs:    routes[0].Hostnames,
				LogGuid: desired.LogGuid,
			}
		}
	}

	return routesByProcessGuid
}

func EndpointsByProcessGuidFromActuals(actuals []receptor.ActualLRPResponse) EndpointsByProcessGuid {
	endpointsByProcessGuid := EndpointsByProcessGuid{}
	for _, actual := range actuals {
		endpoints, err := EndpointsFromActual(actual)
		if err != nil {
			continue
		}

		endpointsByProcessGuid[actual.ProcessGuid] = append(endpointsByProcessGuid[actual.ProcessGuid], endpoints...)
	}

	return endpointsByProcessGuid
}

func EndpointsFromActual(actual receptor.ActualLRPResponse) ([]Endpoint, error) {
	if len(actual.Ports) == 0 {
		return []Endpoint{}, errors.New("missing ports")
	}

	endpoints := []Endpoint{}
	for _, portMapping := range actual.Ports {
		endpoint := Endpoint{
			InstanceGuid:  actual.InstanceGuid,
			Host:          actual.Address,
			Port:          uint16(portMapping.HostPort),
			ContainerPort: uint16(portMapping.ContainerPort),
		}
		endpoints = append(endpoints, endpoint)
	}

	return endpoints, nil
}
