package routing_table

import "github.com/cloudfoundry-incubator/runtime-schema/models"

type RoutingTableEntry struct {
	Routes     map[string]struct{}
	Containers map[Container]struct{}
}

func (entry RoutingTableEntry) HasContainer(container Container) bool {
	_, ok := entry.Containers[container]
	return ok
}

func (entry RoutingTableEntry) HasRoute(route string) bool {
	_, ok := entry.Routes[route]
	return ok
}

func routesAsMap(routes []string) map[string]struct{} {
	routesMap := map[string]struct{}{}
	for _, route := range routes {
		routesMap[route] = struct{}{}
	}
	return routesMap
}

func containersAsMap(containers []Container) map[Container]struct{} {
	containersMap := map[Container]struct{}{}
	for _, container := range containers {
		containersMap[container] = struct{}{}
	}
	return containersMap
}

func (entry RoutingTableEntry) AllRoutes() []string {
	routes := make([]string, len(entry.Routes))
	i := 0
	for route := range entry.Routes {
		routes[i] = route
		i++
	}
	return routes
}

type Container struct {
	Host string
	Port int
}

func (c Container) equals(o Container) bool {
	return c.Host == o.Host && c.Port == o.Port
}

type RoutesByProcessGuid map[string][]string
type ContainersByProcessGuid map[string][]Container

func RoutesByProcessGuidFromDesireds(desireds []models.DesiredLRP) RoutesByProcessGuid {
	routes := RoutesByProcessGuid{}
	for _, desired := range desireds {
		routes[desired.ProcessGuid] = desired.Routes
	}

	return routes
}

func ContainersByProcessGuidFromActuals(actuals []models.ActualLRP) ContainersByProcessGuid {
	containers := ContainersByProcessGuid{}
	for _, actual := range actuals {
		if len(actual.Ports) == 0 {
			continue
		}

		containers[actual.ProcessGuid] = append(containers[actual.ProcessGuid], Container{
			Host: actual.Host,
			Port: int(actual.Ports[0].HostPort),
		})
	}

	return containers
}
