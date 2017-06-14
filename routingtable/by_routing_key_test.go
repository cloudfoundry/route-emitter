package routingtable_test

import (
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/route-emitter/routingtable"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ByRoutingKey", func() {

	Describe("EndpointsFromActual", func() {
		It("builds a map of container port to endpoint", func() {
			endpoints, err := routingtable.EndpointsFromActual(&endpoint.ActualLRPRoutingInfo{
				ActualLRP: &models.ActualLRP{
					ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
					ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
					ActualLRPNetInfo:     models.NewActualLRPNetInfo("1.1.1.1", "1.2.3.4", models.NewPortMapping(11, 44), models.NewPortMapping(66, 99)),
				},
				Evacuating: true,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(endpoints).To(ConsistOf([]routingtable.Endpoint{
				routingtable.Endpoint{
					Host:            "1.1.1.1",
					ContainerIP:     "1.2.3.4",
					Domain:          "domain",
					Port:            11,
					InstanceGuid:    "instance-guid",
					ContainerPort:   44,
					Evacuating:      true,
					Index:           0,
					ModificationTag: &models.ModificationTag{},
				},
				routingtable.Endpoint{
					Host:            "1.1.1.1",
					ContainerIP:     "1.2.3.4",
					Domain:          "domain",
					Port:            66,
					InstanceGuid:    "instance-guid",
					ContainerPort:   99,
					Evacuating:      true,
					Index:           0,
					ModificationTag: &models.ModificationTag{},
				},
			}))
		})
	})
})
