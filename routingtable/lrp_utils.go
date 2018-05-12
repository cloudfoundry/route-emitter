package routingtable

import (
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/routing-info/cfroutes"
	"code.cloudfoundry.org/routing-info/internalroutes"
	"code.cloudfoundry.org/routing-info/tcp_routes"
)

func DesiredLRPData(lrp *models.DesiredLRPSchedulingInfo) lager.Data {
	if lrp == nil {
		return lager.Data{}
	}

	logRoutes := make(models.Routes)
	logRoutes[cfroutes.CF_ROUTER] = lrp.Routes[cfroutes.CF_ROUTER]
	logRoutes[tcp_routes.TCP_ROUTER] = lrp.Routes[tcp_routes.TCP_ROUTER]
	logRoutes[internalroutes.INTERNAL_ROUTER] = lrp.Routes[internalroutes.INTERNAL_ROUTER]

	return lager.Data{
		"process-guid": lrp.ProcessGuid,
		"routes":       logRoutes,
		"domain":       lrp.Domain,
		"instances":    lrp.GetInstances(),
	}
}

func ActualLRPData(actualLRP *models.FlattenedActualLRP) lager.Data {
	return lager.Data{
		"process-guid":  actualLRP.ProcessGuid,
		"index":         actualLRP.Index,
		"domain":        actualLRP.Domain,
		"instance-guid": actualLRP.InstanceGuid,
		"cell-id":       actualLRP.CellId,
		"address":       actualLRP.Address,
		"ports":         actualLRP.Ports,
		"state":         actualLRP.State,
	}
}
