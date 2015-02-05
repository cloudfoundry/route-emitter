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
	endpoints := EndpointsByProcessGuid{}
	for _, actual := range actuals {
		endpoint, err := EndpointFromActual(actual)
		if err != nil {
			continue
		}

		endpoints[actual.ProcessGuid] = append(endpoints[actual.ProcessGuid], endpoint)
	}

	return endpoints
}

func EndpointFromActual(actual receptor.ActualLRPResponse) (Endpoint, error) {
	if len(actual.Ports) == 0 {
		return Endpoint{}, errors.New("missing ports")
	}

	return Endpoint{
		InstanceGuid: actual.InstanceGuid,
		Host:         actual.Address,
		Port:         uint16(actual.Ports[0].HostPort),
	}, nil
}
