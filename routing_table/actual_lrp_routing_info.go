package routing_table

import "github.com/cloudfoundry-incubator/bbs/models"

type ActualLRPRoutingInfo struct {
	ActualLRP  *models.ActualLRP
	Evacuating bool
}

func NewActualLRPRoutingInfo(actualLRPGroup *models.ActualLRPGroup) *ActualLRPRoutingInfo {
	lrp, evacuating := actualLRPGroup.Resolve()
	return &ActualLRPRoutingInfo{
		ActualLRP:  lrp,
		Evacuating: evacuating,
	}
}
