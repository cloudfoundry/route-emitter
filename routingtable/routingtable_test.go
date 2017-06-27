package routingtable_test

import (
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/route-emitter/routingtable"
	. "code.cloudfoundry.org/route-emitter/routingtable/matchers"
	tcpmodels "code.cloudfoundry.org/routing-api/models"
	"code.cloudfoundry.org/routing-info/cfroutes"
	"code.cloudfoundry.org/routing-info/tcp_routes"
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

	noFreshDomains := models.NewDomainSet([]string{})

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

	createRoutingInfo := func(port uint32, hostnames []string, rsURL string, externalPorts []uint32, routerGroupGuid string) models.Routes {
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

		for _, e := range externalPorts {
			tcpRoutes := tcp_routes.TCPRoutes{
				{
					RouterGroupGuid: routerGroupGuid,
					ExternalPort:    e,
					ContainerPort:   port,
				},
			}.RoutingInfo()
			for key, message := range *tcpRoutes {
				routes[key] = message
			}
		}

		return routes
	}

	createSchedulingInfoWithRoutes := func(processGuid string, instances int32, routes models.Routes, logGuid string, currentTag models.ModificationTag) *models.DesiredLRPSchedulingInfo {
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

	Describe("Swap", func() {
		Context("when the table has a routable endpoint", func() {
			BeforeEach(func() {
				routingInfo := createRoutingInfo(key.ContainerPort, []string{hostname1}, "", []uint32{5222}, "router-group-guid")
				beforeDesiredLRP := createSchedulingInfoWithRoutes(key.ProcessGUID, 3, routingInfo, logGuid, *currentTag)
				table.SetRoutes(nil, beforeDesiredLRP)

				actualLRP := createActualLRP(key, endpoint1)
				table.AddEndpoint(actualLRP)
			})

			Context("when the domain is not fresh", func() {
				Context("and the new table has nothing in it", func() {
					BeforeEach(func() {
						tempTable := routingtable.NewRoutingTable(logger, false)
						logger.Info("swapping-empty-table")
						tcpRouteMappings, messagesToEmit = table.Swap(tempTable, noFreshDomains)
					})

					FIt("saves the previous tables routes and emits them when an endpoint is added", func() {
						Expect(messagesToEmit).To(BeZero())
						Expect(tcpRouteMappings).To(BeZero())

						actualLRP := createActualLRP(key, endpoint1)
						tempTable := routingtable.NewRoutingTable(logger, false)
						tempTable.AddEndpoint(actualLRP)
						table.Swap(tempTable, noFreshDomains)
						tcpRouteMappings, messagesToEmit = table.GetRoutingEvents()

						expectedHTTP := routingtable.MessagesToEmit{
							RegistrationMessages: []routingtable.RegistryMessage{
								routingtable.RegistryMessageFor(endpoint1, routingtable.Route{Hostname: hostname1, LogGUID: logGuid}),
							},
						}
						Expect(messagesToEmit).To(MatchMessagesToEmit(expectedHTTP))

						ttl := 0
						expectedTCP := tcpmodels.TcpRouteMapping{
							TcpMappingEntity: tcpmodels.TcpMappingEntity{
								RouterGroupGuid: "router-group-guid",
								HostPort:        uint16(endpoint1.Port),
								HostIP:          endpoint1.Host,
								ExternalPort:    5222,
								TTL:             &ttl,
							},
						}
						Expect(tcpRouteMappings.Registrations).To(ConsistOf(expectedTCP))
					})
				})
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
