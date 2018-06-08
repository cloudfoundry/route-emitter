package routingtable

import "code.cloudfoundry.org/bbs/models"

type ActualLRPRoutingInfo struct {
	ActualLRP  *models.ActualLRP
	Evacuating bool
}

func NewActualLRPRoutingInfo(actualLRPGroup *models.ActualLRPGroup) (*ActualLRPRoutingInfo, error) {
	lrp, evacuating, err := actualLRPGroup.Resolve()
	return &ActualLRPRoutingInfo{
		ActualLRP:  lrp,
		Evacuating: evacuating,
	}, err
}
