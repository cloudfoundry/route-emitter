package cfroutes

import (
	"encoding/json"

	"github.com/cloudfoundry-incubator/bbs/models"
	"github.com/cloudfoundry-incubator/receptor"
)

const CF_ROUTER = "cf-router"

type CFRoutes []CFRoute

type CFRoute struct {
	Hostnames []string `json:"hostnames"`
	Port      uint32   `json:"port"`
}

func (c CFRoutes) RoutingInfo() models.Routes {
	data, _ := json.Marshal(c)
	routingInfo := json.RawMessage(data)
	return models.Routes{
		CF_ROUTER: &routingInfo,
	}
}

func CFRoutesFromRoutingInfo(routingInfo models.Routes) (CFRoutes, error) {
	if routingInfo == nil {
		return nil, nil
	}

	routes := routingInfo
	data, found := routes[CF_ROUTER]
	if !found {
		return nil, nil
	}

	if data == nil {
		return nil, nil
	}

	cfRoutes := CFRoutes{}
	err := json.Unmarshal(*data, &cfRoutes)

	return cfRoutes, err
}

// Old CF Route Stuff for the receptor
type LegacyCFRoutes []LegacyCFRoute

type LegacyCFRoute struct {
	Hostnames []string `json:"hostnames"`
	Port      uint16   `json:"port"`
}

func (c LegacyCFRoutes) LegacyRoutingInfo() receptor.RoutingInfo {
	data, _ := json.Marshal(c)
	routingInfo := json.RawMessage(data)
	return receptor.RoutingInfo{
		CF_ROUTER: &routingInfo,
	}
}

func LegacyCFRoutesFromLegacyRoutingInfo(routingInfo receptor.RoutingInfo) (LegacyCFRoutes, error) {
	if routingInfo == nil {
		return nil, nil
	}

	data, found := routingInfo[CF_ROUTER]
	if !found {
		return nil, nil
	}

	if data == nil {
		return nil, nil
	}

	routes := LegacyCFRoutes{}
	err := json.Unmarshal(*data, &routes)

	return routes, err
}
