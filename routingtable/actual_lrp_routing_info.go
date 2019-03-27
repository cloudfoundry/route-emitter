package routingtable

import "code.cloudfoundry.org/bbs/models"

type ActualLRPRoutingInfo struct {
	ActualLRP  *models.ActualLRP
	Evacuating bool
}

func NewActualLRPRoutingInfo(actualLRP *models.ActualLRP) *ActualLRPRoutingInfo {
	return &ActualLRPRoutingInfo{
		ActualLRP:  actualLRP,
		Evacuating: actualLRP.Presence == models.ActualLRP_Evacuating,
	}
}
