package routingtable_test

import (
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/route-emitter/routingtable"
	. "code.cloudfoundry.org/route-emitter/routingtable/matchers"
	"code.cloudfoundry.org/routing-info/cfroutes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("RoutingTable", func() {
	var (
		table            routingtable.RoutingTable
		messagesToEmit   routingtable.MessagesToEmit
		tcpRouteMappings routingtable.TCPRouteMappings
		logger           *lagertest.TestLogger
	)

	key := routingtable.RoutingKey{ProcessGUID: "some-process-guid", ContainerPort: 8080}

	domain := "domain"
	hostname1 := "foo.example.com"

	currentTag := &models.ModificationTag{Epoch: "abc", Index: 1}
	newerTag := &models.ModificationTag{Epoch: "def", Index: 0}

	endpoint1 := routingtable.Endpoint{
		InstanceGUID:    "ig-1",
		Host:            "1.1.1.1",
		ContainerIP:     "1.2.3.4",
		Index:           0,
		Domain:          domain,
		Port:            11,
		ContainerPort:   8080,
		Evacuating:      false,
		ModificationTag: currentTag,
	}
	endpoint2 := routingtable.Endpoint{
		InstanceGUID:    "ig-2",
		Host:            "2.2.2.2",
		ContainerIP:     "2.3.4.5",
		Index:           1,
		Domain:          domain,
		Port:            22,
		ContainerPort:   8080,
		Evacuating:      false,
		ModificationTag: currentTag,
	}
	endpoint3 := routingtable.Endpoint{
		InstanceGUID:    "ig-3",
		Host:            "3.3.3.3",
		ContainerIP:     "3.4.5.6",
		Index:           2,
		Domain:          domain,
		Port:            33,
		ContainerPort:   8080,
		Evacuating:      false,
		ModificationTag: currentTag,
	}

	logGuid := "some-log-guid"

	BeforeEach(func() {
		logger = lagertest.NewTestLogger("test-route-emitter")
		table = routingtable.NewRoutingTable(logger, false)
	})

	createDesiredLRPSchedulingInfo := func(processGuid string, instances int32, port uint32, logGuid, rsURL string, currentTag models.ModificationTag, hostnames ...string) *models.DesiredLRPSchedulingInfo {
		routingInfo := cfroutes.CFRoutes{
			{
				Hostnames:       hostnames,
				Port:            port,
				RouteServiceUrl: rsURL,
			},
		}.RoutingInfo()

		routes := models.Routes{}

		for key, message := range routingInfo {
			routes[key] = message
		}

		info := models.NewDesiredLRPSchedulingInfo(models.NewDesiredLRPKey(processGuid, "domain", logGuid), "", instances, models.NewDesiredLRPResource(0, 0, 0, ""), routes, currentTag, nil, nil)
		return &info
	}

	createActualLRP := func(
		key routingtable.RoutingKey,
		instance routingtable.Endpoint,
	) *routingtable.ActualLRPRoutingInfo {
		return &routingtable.ActualLRPRoutingInfo{
			ActualLRP: &models.ActualLRP{
				ActualLRPKey:         models.NewActualLRPKey(key.ProcessGUID, instance.Index, instance.Domain),
				ActualLRPInstanceKey: models.NewActualLRPInstanceKey(instance.InstanceGUID, "cell-id"),
				ActualLRPNetInfo: models.NewActualLRPNetInfo(
					instance.Host,
					instance.ContainerIP,
					models.NewPortMapping(instance.Port, instance.ContainerPort),
				),
				State:           models.ActualLRPStateRunning,
				ModificationTag: *instance.ModificationTag,
			},
			Evacuating: instance.Evacuating,
		}
	}

	Describe("SetRoutes", func() {
		Context("when the instances are scaled down", func() {
			BeforeEach(func() {
				//beforeLRP
				beforeDesiredLRP := createDesiredLRPSchedulingInfo(key.ProcessGUID, 3, key.ContainerPort, logGuid, "", *currentTag, hostname1)
				table.SetRoutes(nil, beforeDesiredLRP)

				actualLRP := createActualLRP(key, endpoint1)
				table.AddEndpoint(actualLRP)
				actualLRP = createActualLRP(key, endpoint2)
				table.AddEndpoint(actualLRP)
				actualLRP = createActualLRP(key, endpoint3)
				table.AddEndpoint(actualLRP)
				//changedLRP
				afterDesiredLRP := createDesiredLRPSchedulingInfo(key.ProcessGUID, 1, key.ContainerPort, logGuid, "", *newerTag, hostname1)
				tcpRouteMappings, messagesToEmit = table.SetRoutes(beforeDesiredLRP, afterDesiredLRP)
			})

			It("should unregisters extra endpoints", func() {
				Expect(tcpRouteMappings).To(BeZero())
				expected := routingtable.MessagesToEmit{
					UnregistrationMessages: []routingtable.RegistryMessage{
						routingtable.RegistryMessageFor(endpoint2, routingtable.Route{Hostname: hostname1, LogGUID: logGuid}),
						routingtable.RegistryMessageFor(endpoint3, routingtable.Route{Hostname: hostname1, LogGUID: logGuid}),
					},
				}
				Expect(messagesToEmit).To(MatchMessagesToEmit(expected))
			})

			It("no longer emits the extra endpoints", func() {
				tcpRouteMappings, messagesToEmit = table.GetRoutingEvents()

				Expect(tcpRouteMappings).To(BeZero())
				expected := routingtable.MessagesToEmit{
					RegistrationMessages: []routingtable.RegistryMessage{
						routingtable.RegistryMessageFor(endpoint1, routingtable.Route{Hostname: hostname1, LogGUID: logGuid}),
					},
				}
				Expect(messagesToEmit).To(MatchMessagesToEmit(expected))
			})
		})
	})

	Describe("AddEndpoint", func() {
		Context("when a desired LRP has instances field less than number of actual LRP instances", func() {
			BeforeEach(func() {
				afterDesiredLRP := createDesiredLRPSchedulingInfo(key.ProcessGUID, 1, key.ContainerPort, logGuid, "", *currentTag, hostname1)
				table.SetRoutes(nil, afterDesiredLRP)
			})

			It("only registers in the number of instances defined in the desired LRP", func() {
				actualLRP1 := createActualLRP(key, endpoint1)
				actualLRP2 := createActualLRP(key, endpoint2)
				actualLRP3 := createActualLRP(key, endpoint3)
				tcpRouteMappings, messagesToEmit = table.AddEndpoint(actualLRP1)
				Expect(tcpRouteMappings).To(BeZero())
				expected := routingtable.MessagesToEmit{
					RegistrationMessages: []routingtable.RegistryMessage{
						routingtable.RegistryMessageFor(endpoint1, routingtable.Route{Hostname: hostname1, LogGUID: logGuid}),
					},
				}
				Expect(messagesToEmit).To(MatchMessagesToEmit(expected))

				tcpRouteMappings, messagesToEmit = table.AddEndpoint(actualLRP2)
				Expect(tcpRouteMappings).To(BeZero())
				Expect(messagesToEmit).To(BeZero())

				tcpRouteMappings, messagesToEmit = table.AddEndpoint(actualLRP3)
				Expect(tcpRouteMappings).To(BeZero())
				Expect(messagesToEmit).To(BeZero())
			})
		})
	})
})
