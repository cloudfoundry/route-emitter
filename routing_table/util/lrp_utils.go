package util

import (
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/routing_table/schema/endpoint"
	"code.cloudfoundry.org/routing-info/cfroutes"
	"code.cloudfoundry.org/routing-info/tcp_routes"
)

func DesiredLRPData(lrp *models.DesiredLRPSchedulingInfo) lager.Data {
	logRoutes := make(models.Routes)
	logRoutes[cfroutes.CF_ROUTER] = lrp.Routes[cfroutes.CF_ROUTER]
	logRoutes[tcp_routes.TCP_ROUTER] = lrp.Routes[tcp_routes.TCP_ROUTER]

	return lager.Data{
		"process-guid": lrp.ProcessGuid,
		"routes":       logRoutes,
	}
}

func ActualLRPData(info *endpoint.ActualLRPRoutingInfo) lager.Data {
	lrp := info.ActualLRP

	return lager.Data{
		"process-guid":  lrp.ProcessGuid,
		"index":         lrp.Index,
		"domain":        lrp.Domain,
		"instance-guid": lrp.InstanceGuid,
		"cell-id":       lrp.CellId,
		"address":       lrp.Address,
		"ports":         lrp.Ports,
		"evacuating":    info.Evacuating,
		"state":         lrp.State,
	}
}
