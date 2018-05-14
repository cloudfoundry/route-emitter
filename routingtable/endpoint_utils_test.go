package routingtable_test

import (
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/route-emitter/routingtable"
	"code.cloudfoundry.org/routing-info/tcp_routes"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("LRP Utils", func() {
	Describe("NewEndpointsFromActual", func() {
		Context("when actual is not evacuating", func() {
			It("builds a map of container port to endpoint", func() {
				tag := models.ModificationTag{Epoch: "abc", Index: 0}
				actualInfo := &models.FlattenedActualLRP{
					ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
					ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
					ActualLRPInfo: models.ActualLRPInfo{
						ActualLRPNetInfo: models.NewActualLRPNetInfo(
							"1.1.1.1",
							"2.2.2.2",
							models.NewPortMapping(11, 44),
							models.NewPortMapping(66, 99),
						),
						State:           models.ActualLRPStateRunning,
						ModificationTag: tag,
						PlacementState:  models.PlacementStateType_Normal,
					},
				}

				endpoints := routingtable.NewEndpointsFromActual(actualInfo)

				Expect(endpoints).To(ConsistOf([]routingtable.Endpoint{
					routingtable.NewEndpoint("instance-guid", false, "1.1.1.1", "2.2.2.2", 11, 44, &tag),
					routingtable.NewEndpoint("instance-guid", false, "1.1.1.1", "2.2.2.2", 66, 99, &tag),
				}))
			})

			Context("with TLS proxy ports", func() {
				It("builds a map of tls proxy ports to endpoints", func() {
					tag := models.ModificationTag{Epoch: "abc", Index: 0}
					actualInfo := &models.FlattenedActualLRP{
						ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
						ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
						ActualLRPInfo: models.ActualLRPInfo{
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								"1.1.1.1",
								"2.2.2.2",
								models.NewPortMappingWithTLSProxy(11, 44, 61004, 61005),
								models.NewPortMappingWithTLSProxy(66, 99, 61006, 61007),
							),
							State:           models.ActualLRPStateRunning,
							PlacementState:  models.PlacementStateType_Normal,
							ModificationTag: tag,
						},
					}

					endpoints := routingtable.NewEndpointsFromActual(actualInfo)

					Expect(endpoints).To(ConsistOf([]routingtable.Endpoint{
						newEndpointWithTlsProxyPort("instance-guid", false, "1.1.1.1", "2.2.2.2", 11, 44, 61004, 61005, &tag),
						newEndpointWithTlsProxyPort("instance-guid", false, "1.1.1.1", "2.2.2.2", 66, 99, 61006, 61007, &tag),
					}))
				})
			})
		})

		Context("when actual is evacuating", func() {
			It("builds a map of container port to endpoint", func() {
				tag := models.ModificationTag{Epoch: "abc", Index: 0}

				actualInfo := &models.FlattenedActualLRP{
					ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
					ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
					ActualLRPInfo: models.ActualLRPInfo{
						ActualLRPNetInfo: models.NewActualLRPNetInfo(
							"1.1.1.1",
							"2.2.2.2",
							models.NewPortMapping(11, 44),
							models.NewPortMapping(66, 99),
						),
						State:           models.ActualLRPStateRunning,
						PlacementState:  models.PlacementStateType_Evacuating,
						ModificationTag: tag,
					},
				}

				endpoints := routingtable.NewEndpointsFromActual(actualInfo)

				Expect(endpoints).To(ConsistOf([]routingtable.Endpoint{
					routingtable.NewEndpoint("instance-guid", true, "1.1.1.1", "2.2.2.2", 11, 44, &tag),
					routingtable.NewEndpoint("instance-guid", true, "1.1.1.1", "2.2.2.2", 66, 99, &tag),
				}))
			})
		})
	})

	Describe("NewRoutingKeysFromActual", func() {
		It("creates a list of keys for an actual LRP", func() {
			keys := routingtable.NewRoutingKeysFromActual(&models.FlattenedActualLRP{
				ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
				ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
				ActualLRPInfo: models.ActualLRPInfo{
					ActualLRPNetInfo: models.NewActualLRPNetInfo(
						"1.1.1.1",
						"2.2.2.2",
						models.NewPortMapping(11, 44),
						models.NewPortMapping(66, 99),
					),
					State:          models.ActualLRPStateRunning,
					PlacementState: models.PlacementStateType_Normal,
				},
			})

			Expect(keys).To(HaveLen(2))
			Expect(keys).To(ContainElement(routingtable.NewRoutingKey("process-guid", 44)))
			Expect(keys).To(ContainElement(routingtable.NewRoutingKey("process-guid", 99)))
		})

		Context("when the actual lrp has tls proxy ports", func() {
			It("creates a list of keys for an actual LRP", func() {
				keys := routingtable.NewRoutingKeysFromActual(&models.FlattenedActualLRP{
					ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
					ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
					ActualLRPInfo: models.ActualLRPInfo{
						ActualLRPNetInfo: models.NewActualLRPNetInfo(
							"1.1.1.1",
							"2.2.2.2",
							models.NewPortMappingWithTLSProxy(11, 44, 61004, 61005),
							models.NewPortMappingWithTLSProxy(66, 99, 61006, 61007),
						),
						State:          models.ActualLRPStateRunning,
						PlacementState: models.PlacementStateType_Normal,
					},
				})

				Expect(keys).To(HaveLen(4))
				Expect(keys).To(ContainElement(routingtable.NewRoutingKey("process-guid", 44)))
				Expect(keys).To(ContainElement(routingtable.NewRoutingKey("process-guid", 99)))
				Expect(keys).To(ContainElement(routingtable.NewRoutingKey("process-guid", 61005)))
				Expect(keys).To(ContainElement(routingtable.NewRoutingKey("process-guid", 61007)))
			})
		})

		Context("when the actual lrp has no port mappings", func() {
			It("returns no keys", func() {
				keys := routingtable.NewRoutingKeysFromActual(&models.FlattenedActualLRP{
					ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
					ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
					ActualLRPInfo: models.ActualLRPInfo{
						ActualLRPNetInfo: models.NewActualLRPNetInfo(
							"1.1.1.1",
							"2.2.2.2",
						),
						State:          models.ActualLRPStateRunning,
						PlacementState: models.PlacementStateType_Normal,
					},
				})

				Expect(keys).To(HaveLen(0))
			})
		})
	})

	Describe("NewRoutingKeysFromDesired", func() {
		It("creates a list of keys for an actual LRP", func() {
			routes := tcp_routes.TCPRoutes{
				{ExternalPort: 61000, ContainerPort: 8080},
				{ExternalPort: 61001, ContainerPort: 9090},
			}

			desired := (&models.DesiredLRP{
				Domain:      "tests",
				ProcessGuid: "process-guid",
				Ports:       []uint32{8080, 9090},
				Routes:      routes.RoutingInfo(),
				LogGuid:     "abc-guid",
			}).DesiredLRPSchedulingInfo()

			keys := routingtable.NewRoutingKeysFromDesired(&desired)

			Expect(keys).To(HaveLen(2))
			Expect(keys).To(ContainElement(routingtable.NewRoutingKey("process-guid", 8080)))
			Expect(keys).To(ContainElement(routingtable.NewRoutingKey("process-guid", 9090)))
		})

		Context("when the desired LRP does not define any container ports", func() {
			It("returns no keys", func() {
				routes := tcp_routes.TCPRoutes{}

				desired := (&models.DesiredLRP{
					Domain:      "tests",
					ProcessGuid: "process-guid",
					Routes:      routes.RoutingInfo(),
					LogGuid:     "abc-guid",
				}).DesiredLRPSchedulingInfo()

				keys := routingtable.NewRoutingKeysFromDesired(&desired)
				Expect(keys).To(HaveLen(0))
			})
		})
	})
})

func newEndpointWithTlsProxyPort(
	instanceGUID string, evacuating bool,
	host, containerIP string,
	port, containerPort, tlsProxyPort, containerTlsProxyPort uint32,
	modificationTag *models.ModificationTag,
) routingtable.Endpoint {
	return routingtable.Endpoint{
		InstanceGUID:          instanceGUID,
		Evacuating:            evacuating,
		Host:                  host,
		ContainerIP:           containerIP,
		Port:                  port,
		ContainerPort:         containerPort,
		TlsProxyPort:          tlsProxyPort,
		ContainerTlsProxyPort: containerTlsProxyPort,
		ModificationTag:       modificationTag,
	}
}
