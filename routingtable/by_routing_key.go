package routingtable

import (
	"errors"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/routing-info/cfroutes"
)

type RoutesByRoutingKey map[endpoint.RoutingKey][]Route
type EndpointsByRoutingKey map[endpoint.RoutingKey][]Endpoint

func RoutesByRoutingKeyFromSchedulingInfos(schedulingInfos []*models.DesiredLRPSchedulingInfo) RoutesByRoutingKey {
	routesByRoutingKey := RoutesByRoutingKey{}
	for _, desired := range schedulingInfos {
		routes, err := cfroutes.CFRoutesFromRoutingInfo(desired.Routes)
		if err == nil && len(routes) > 0 {
			for _, cfRoute := range routes {
				key := endpoint.RoutingKey{ProcessGUID: desired.ProcessGuid, ContainerPort: cfRoute.Port}
				var routeEntries []Route
				for _, hostname := range cfRoute.Hostnames {
					routeEntries = append(routeEntries, Route{
						Hostname:         hostname,
						LogGuid:          desired.LogGuid,
						RouteServiceUrl:  cfRoute.RouteServiceUrl,
						IsolationSegment: cfRoute.IsolationSegment,
					})
				}
				routesByRoutingKey[key] = append(routesByRoutingKey[key], routeEntries...)
			}
		}
	}

	return routesByRoutingKey
}

func EndpointsByRoutingKeyFromActuals(actuals []*endpoint.ActualLRPRoutingInfo, schedInfos map[string]*models.DesiredLRPSchedulingInfo) EndpointsByRoutingKey {
	endpointsByRoutingKey := EndpointsByRoutingKey{}
	for _, actual := range actuals {
		if schedInfo, ok := schedInfos[actual.ActualLRP.ProcessGuid]; ok {
			// Check whether this actual is desired
			if actual.ActualLRP.Index > schedInfo.Instances-1 {
				continue
			}
		}

		endpoints, err := EndpointsFromActual(actual)
		if err != nil {
			continue
		}

		for containerPort, routingEndpoint := range endpoints {
			key := endpoint.RoutingKey{ProcessGUID: actual.ActualLRP.ProcessGuid, ContainerPort: containerPort}
			endpointsByRoutingKey[key] = append(endpointsByRoutingKey[key], routingEndpoint)
		}
	}

	return endpointsByRoutingKey
}

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

func RoutingKeysFromActual(actual *models.ActualLRP) []endpoint.RoutingKey {
	keys := []endpoint.RoutingKey{}
	for _, portMapping := range actual.Ports {
		if portMapping != nil {
			keys = append(keys, endpoint.RoutingKey{ProcessGUID: actual.ProcessGuid, ContainerPort: uint32(portMapping.ContainerPort)})
		}
	}

	return keys
}

func RoutingKeysFromSchedulingInfo(schedulingInfo *models.DesiredLRPSchedulingInfo) []endpoint.RoutingKey {
	keys := []endpoint.RoutingKey{}

	routes, err := cfroutes.CFRoutesFromRoutingInfo(schedulingInfo.Routes)
	if err == nil && len(routes) > 0 {
		for _, cfRoute := range routes {
			keys = append(keys, endpoint.RoutingKey{ProcessGUID: schedulingInfo.ProcessGuid, ContainerPort: cfRoute.Port})
		}
	}
	return keys
}
