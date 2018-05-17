package routingtable

import "code.cloudfoundry.org/bbs/models"

type ActualLRPRoutingInfo struct {
	ActualLRP  *models.ActualLRP
	Evacuating bool
}

func NewActualLRPRoutingInfo(actualLRPGroup *models.ActualLRPGroup) *ActualLRPRoutingInfo {
	lrp, evacuating, err := actualLRPGroup.Resolve()
	if err != nil {
		// previously, Resolve() would panic in this case - we're putting this here as an interim
		// step while we refactor so that this isn't needded
		panic(err)
	}
	return &ActualLRPRoutingInfo{
		ActualLRP:  lrp,
		Evacuating: evacuating,
	}
}
