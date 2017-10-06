package routingtable_test

import (
	"fmt"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/route-emitter/routingtable"
	. "code.cloudfoundry.org/route-emitter/routingtable/matchers"
	tcpmodels "code.cloudfoundry.org/routing-api/models"
	"code.cloudfoundry.org/routing-info/cfroutes"
	"code.cloudfoundry.org/routing-info/internalroutes"
	"code.cloudfoundry.org/routing-info/tcp_routes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func createDesiredLRPSchedulingInfo(processGuid string, instances int32, port uint32, logGuid, rsURL string, currentTag models.ModificationTag, hostnames ...string) *models.DesiredLRPSchedulingInfo {
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

func createRoutingInfo(port uint32, hostnames, internalHostnames []string, rsURL string, externalPorts []uint32, routerGroupGuid string) models.Routes {
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

	internalRoutes := internalroutes.InternalRoutes{}

	for _, host := range internalHostnames {
		internalRoutes = append(internalRoutes,
			internalroutes.InternalRoute{Hostname: host},
		)
	}
	internalRoutingInfo := internalRoutes.RoutingInfo()
	for key, message := range internalRoutingInfo {
		routes[key] = message
	}

	return routes
}

func createSchedulingInfoWithRoutes(processGuid string, instances int32, routes models.Routes, logGuid string, currentTag models.ModificationTag) *models.DesiredLRPSchedulingInfo {
	info := models.NewDesiredLRPSchedulingInfo(models.NewDesiredLRPKey(processGuid, "domain", logGuid), "", instances, models.NewDesiredLRPResource(0, 0, 0, ""), routes, currentTag, nil, nil)
	return &info
}

func createActualLRP(
	key routingtable.RoutingKey,
	instance routingtable.Endpoint,
	domain string,
) *routingtable.ActualLRPRoutingInfo {
	return &routingtable.ActualLRPRoutingInfo{
		ActualLRP: &models.ActualLRP{
			ActualLRPKey:         models.NewActualLRPKey(key.ProcessGUID, instance.Index, domain),
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

var _ = Describe("RoutingTable", func() {
	var (
		table                           routingtable.RoutingTable
		messagesToEmit                  routingtable.MessagesToEmit
		tcpRouteMappings                routingtable.TCPRouteMappings
		logger                          *lagertest.TestLogger
		endpoint1, endpoint2, endpoint3 routingtable.Endpoint
	)

	key := routingtable.RoutingKey{ProcessGUID: "some-process-guid", ContainerPort: 8080}

	domain := "domain"
	hostname1 := "foo.example.com"

	noFreshDomains := models.NewDomainSet([]string{})
	freshDomains := models.NewDomainSet([]string{domain})

	currentTag := &models.ModificationTag{Epoch: "abc", Index: 1}
	newerTag := &models.ModificationTag{Epoch: "def", Index: 0}

	logGuid := "some-log-guid"

	BeforeEach(func() {
		logger = lagertest.NewTestLogger("test-route-emitter")
		table = routingtable.NewRoutingTable(logger, false)

		endpoint1 = routingtable.Endpoint{
			InstanceGUID:    "ig-1",
			Host:            "1.1.1.1",
			ContainerIP:     "1.2.3.4",
			Index:           0,
			Port:            11,
			ContainerPort:   8080,
			Evacuating:      false,
			ModificationTag: currentTag,
		}
		endpoint2 = routingtable.Endpoint{
			InstanceGUID:    "ig-2",
			Host:            "2.2.2.2",
			ContainerIP:     "2.3.4.5",
			Index:           1,
			Port:            22,
			ContainerPort:   8080,
			Evacuating:      false,
			ModificationTag: currentTag,
		}
		endpoint3 = routingtable.Endpoint{
			InstanceGUID:    "ig-3",
			Host:            "3.3.3.3",
			ContainerIP:     "3.4.5.6",
			Index:           2,
			Port:            33,
			ContainerPort:   8080,
			Evacuating:      false,
			ModificationTag: currentTag,
		}
	})

	Describe("SetRoutes", func() {
		var (
			instances        int32
			beforeDesiredLRP *models.DesiredLRPSchedulingInfo
			internalHostname string
		)

		BeforeEach(func() {
			instances = 1
		})

		JustBeforeEach(func() {
			internalHostname = "internal"

			// Add routes and internal routes
			routes := createRoutingInfo(key.ContainerPort, []string{hostname1}, []string{internalHostname}, "", []uint32{}, logGuid)

			// Set routes on desired lrp
			beforeDesiredLRP = createSchedulingInfoWithRoutes(key.ProcessGUID, instances, routes, logGuid, *currentTag)
			table.SetRoutes(nil, beforeDesiredLRP)

			actualLRP := createActualLRP(key, endpoint1, domain)
			table.AddEndpoint(actualLRP)
		})

		Context("when the route is removed", func() {
			JustBeforeEach(func() {
				afterDesiredLRP := createDesiredLRPSchedulingInfo(key.ProcessGUID, instances, key.ContainerPort, logGuid, "", *newerTag, "")
				afterDesiredLRP.Routes = nil
				tcpRouteMappings, messagesToEmit = table.SetRoutes(beforeDesiredLRP, afterDesiredLRP)
			})

			It("emits an unregistration", func() {
				expectedUnregistrationMessages := []routingtable.RegistryMessage{
					routingtable.RegistryMessageFor(endpoint1, routingtable.Route{Hostname: hostname1, LogGUID: logGuid}),
				}
				Expect(messagesToEmit.UnregistrationMessages).To(Equal(expectedUnregistrationMessages))
			})

			Context("when the a new route is added", func() {
				JustBeforeEach(func() {
					newerTag := &models.ModificationTag{Epoch: "ghi", Index: 0}
					afterDesiredLRP := createDesiredLRPSchedulingInfo(key.ProcessGUID, instances, key.ContainerPort, logGuid, "", *newerTag, "bar.example.com")
					tcpRouteMappings, messagesToEmit = table.SetRoutes(beforeDesiredLRP, afterDesiredLRP)
				})

				It("emits a registration", func() {
					expected := routingtable.MessagesToEmit{
						RegistrationMessages: []routingtable.RegistryMessage{
							routingtable.RegistryMessageFor(endpoint1, routingtable.Route{Hostname: "bar.example.com", LogGUID: logGuid}),
						},
					}
					Expect(messagesToEmit).To(Equal(expected))
				})

				Context("when the endpoint has a tls proxy port", func() {
					BeforeEach(func() {
						endpoint1.TlsProxyPort = 6010
					})

					It("emits a registration", func() {
						expected := routingtable.MessagesToEmit{
							RegistrationMessages: []routingtable.RegistryMessage{
								routingtable.RegistryMessageFor(endpoint1, routingtable.Route{Hostname: "bar.example.com", LogGUID: logGuid}),
							},
						}
						Expect(messagesToEmit).To(Equal(expected))
					})
				})
			})

			Context("when an update is received with old modification tag", func() {
				JustBeforeEach(func() {
					afterDesiredLRP := createDesiredLRPSchedulingInfo(key.ProcessGUID, instances, key.ContainerPort, logGuid, "", *newerTag, "bar.example.com")
					tcpRouteMappings, messagesToEmit = table.SetRoutes(beforeDesiredLRP, afterDesiredLRP)
				})

				It("does not emit anything", func() {
					Expect(messagesToEmit).To(BeZero())
				})
			})
		})

		Context("when the internal route is removed", func() {
			JustBeforeEach(func() {
				afterDesiredLRP := createDesiredLRPSchedulingInfo(key.ProcessGUID, instances, key.ContainerPort, logGuid, "", *newerTag, hostname1)
				tcpRouteMappings, messagesToEmit = table.SetRoutes(beforeDesiredLRP, afterDesiredLRP)
			})

			It("emits an internal unregistration", func() {
				expected := routingtable.MessagesToEmit{
					InternalUnregistrationMessages: []routingtable.RegistryMessage{
						{
							URIs:                 []string{internalHostname, fmt.Sprintf("%d.%s", 0, internalHostname)},
							Host:                 endpoint1.ContainerIP,
							Tags:                 map[string]string{"component": "route-emitter"},
							App:                  logGuid,
							PrivateInstanceIndex: "0",
						},
					},
				}
				Expect(messagesToEmit).To(Equal(expected))
			})

			Context("when an update is received with old modification tag", func() {
				JustBeforeEach(func() {
					routes := createRoutingInfo(key.ContainerPort, []string{hostname1}, []string{}, "", []uint32{}, logGuid)

					afterDesiredLRP := createSchedulingInfoWithRoutes(key.ProcessGUID, instances, routes, logGuid, *newerTag)
					tcpRouteMappings, messagesToEmit = table.SetRoutes(beforeDesiredLRP, afterDesiredLRP)
				})

				It("does not emit anything", func() {
					Expect(messagesToEmit).To(BeZero())
				})
			})
		})

		Context("when a new internal route is added", func() {
			var internalHostname2 string
			JustBeforeEach(func() {
				internalHostname2 = "internal-2"
				newerTag := &models.ModificationTag{Epoch: "ghi", Index: 0}
				routes := createRoutingInfo(key.ContainerPort, []string{hostname1}, []string{internalHostname, internalHostname2}, "", []uint32{}, logGuid)

				afterDesiredLRP := createSchedulingInfoWithRoutes(key.ProcessGUID, instances, routes, logGuid, *newerTag)

				tcpRouteMappings, messagesToEmit = table.SetRoutes(beforeDesiredLRP, afterDesiredLRP)
			})

			It("emits an internal registration", func() {
				expected := routingtable.MessagesToEmit{
					InternalRegistrationMessages: []routingtable.RegistryMessage{
						{
							URIs:                 []string{internalHostname2, fmt.Sprintf("%d.%s", 0, internalHostname2)},
							Host:                 endpoint1.ContainerIP,
							Tags:                 map[string]string{"component": "route-emitter"},
							App:                  logGuid,
							PrivateInstanceIndex: "0",
						},
					},
				}
				Expect(messagesToEmit).To(Equal(expected))
			})
		})

		Context("when the instances are scaled down", func() {
			BeforeEach(func() {
				instances = 3
			})

			JustBeforeEach(func() {
				// add 2 more instances
				actualLRP := createActualLRP(key, endpoint2, domain)
				table.AddEndpoint(actualLRP)
				actualLRP = createActualLRP(key, endpoint3, domain)
				table.AddEndpoint(actualLRP)

				//changedLRP
				routes := createRoutingInfo(key.ContainerPort, []string{hostname1}, []string{internalHostname}, "", []uint32{}, logGuid)
				afterDesiredLRP := createSchedulingInfoWithRoutes(key.ProcessGUID, 1, routes, logGuid, *newerTag)

				tcpRouteMappings, messagesToEmit = table.SetRoutes(beforeDesiredLRP, afterDesiredLRP)
			})

			It("should unregisters extra endpoints", func() {
				Expect(tcpRouteMappings).To(BeZero())
				expected := routingtable.MessagesToEmit{
					UnregistrationMessages: []routingtable.RegistryMessage{
						routingtable.RegistryMessageFor(endpoint2, routingtable.Route{Hostname: hostname1, LogGUID: logGuid}),
						routingtable.RegistryMessageFor(endpoint3, routingtable.Route{Hostname: hostname1, LogGUID: logGuid}),
					},
					InternalUnregistrationMessages: []routingtable.RegistryMessage{
						{
							URIs:                 []string{internalHostname, fmt.Sprintf("%d.%s", 1, internalHostname)},
							Host:                 endpoint2.ContainerIP,
							Tags:                 map[string]string{"component": "route-emitter"},
							App:                  logGuid,
							PrivateInstanceIndex: "1",
						},
						{
							URIs:                 []string{internalHostname, fmt.Sprintf("%d.%s", 2, internalHostname)},
							Host:                 endpoint3.ContainerIP,
							Tags:                 map[string]string{"component": "route-emitter"},
							App:                  logGuid,
							PrivateInstanceIndex: "2",
						},
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
					InternalRegistrationMessages: []routingtable.RegistryMessage{
						{
							URIs:                 []string{internalHostname, fmt.Sprintf("%d.%s", 0, internalHostname)},
							Host:                 endpoint1.ContainerIP,
							Tags:                 map[string]string{"component": "route-emitter"},
							App:                  logGuid,
							PrivateInstanceIndex: "0",
						},
					},
				}
				Expect(messagesToEmit).To(MatchMessagesToEmit(expected))
			})
		})
	})

	Describe("Swap", func() {
		It("preserves the desired LRP domain", func() {
			By("creating a routing table with a route and endpoint")
			routingInfo := createRoutingInfo(key.ContainerPort, []string{hostname1}, []string{}, "", []uint32{5222}, "router-group-guid")
			beforeDesiredLRP := createSchedulingInfoWithRoutes(key.ProcessGUID, 3, routingInfo, logGuid, *currentTag)
			table.SetRoutes(nil, beforeDesiredLRP)
			actualLRP := createActualLRP(key, endpoint1, domain)
			table.AddEndpoint(actualLRP)

			By("removing the route and making the domains unfresh")
			tempTable := routingtable.NewRoutingTable(logger, false)
			actualLRP = createActualLRP(key, endpoint1, domain)
			tempTable.AddEndpoint(actualLRP)
			table.Swap(tempTable, noFreshDomains)

			By("making the domain fresh again")
			tempTable = routingtable.NewRoutingTable(logger, false)
			actualLRP = createActualLRP(key, endpoint1, domain)
			tempTable.AddEndpoint(actualLRP)

			tcpRouteMappings, messagesToEmit = table.Swap(tempTable, freshDomains)

			expectedHTTP := routingtable.MessagesToEmit{
				UnregistrationMessages: []routingtable.RegistryMessage{
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
			Expect(tcpRouteMappings.Unregistrations).To(ConsistOf(expectedTCP))
		})

		Context("when the table has a routable endpoint", func() {
			BeforeEach(func() {
				routingInfo := createRoutingInfo(key.ContainerPort, []string{hostname1}, []string{}, "", []uint32{5222}, "router-group-guid")
				beforeDesiredLRP := createSchedulingInfoWithRoutes(key.ProcessGUID, 3, routingInfo, logGuid, *currentTag)
				table.SetRoutes(nil, beforeDesiredLRP)

				actualLRP := createActualLRP(key, endpoint1, domain)
				table.AddEndpoint(actualLRP)
			})

			Context("when the domain is not fresh", func() {
				Context("and the new table has nothing in it", func() {
					BeforeEach(func() {
						tempTable := routingtable.NewRoutingTable(logger, false)
						tcpRouteMappings, messagesToEmit = table.Swap(tempTable, noFreshDomains)
					})

					It("unregisters non-existent endpoints", func() {
						expectedHTTP := routingtable.MessagesToEmit{
							UnregistrationMessages: []routingtable.RegistryMessage{
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
						Expect(tcpRouteMappings.Unregistrations).To(ConsistOf(expectedTCP))
					})

					It("saves the previous tables routes and emits them when an endpoint is added", func() {
						actualLRP := createActualLRP(key, endpoint1, domain)
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

	Describe("TableSize", func() {
		var (
			desiredLRP *models.DesiredLRPSchedulingInfo
			actualLRP  *routingtable.ActualLRPRoutingInfo
		)

		BeforeEach(func() {
			routes := createRoutingInfo(key.ContainerPort, []string{hostname1}, []string{}, "", []uint32{5222}, "router-group-guid")
			desiredLRP = createSchedulingInfoWithRoutes(key.ProcessGUID, 1, routes, logGuid, *currentTag)
			table.SetRoutes(nil, desiredLRP)
			actualLRP = createActualLRP(key, endpoint1, domain)
			table.AddEndpoint(actualLRP)

			Expect(table.TableSize()).To(Equal(1))
		})

		Context("when routes are deleted", func() {
			BeforeEach(func() {
				newDesiredLRP := createSchedulingInfoWithRoutes(key.ProcessGUID, 1, nil, logGuid, *newerTag)
				table.SetRoutes(desiredLRP, newDesiredLRP)
			})

			It("doesn't remove the entry from the table", func() {
				Expect(table.TableSize()).To(Equal(1))
			})

			Context("and endpoints are deleted", func() {
				BeforeEach(func() {
					table.RemoveEndpoint(actualLRP)
				})

				It("removes the entry", func() {
					Expect(table.TableSize()).To(Equal(0))
				})
			})
		})

		Context("when all endpoints are deleted", func() {
			BeforeEach(func() {
				table.RemoveEndpoint(actualLRP)
			})

			It("doesn't remove the entry from the table", func() {
				Expect(table.TableSize()).To(Equal(1))
			})

			Context("and endpoints are deleted", func() {
				BeforeEach(func() {
					newDesiredLRP := createSchedulingInfoWithRoutes(key.ProcessGUID, 1, nil, logGuid, *newerTag)
					table.SetRoutes(desiredLRP, newDesiredLRP)
				})

				It("removes the entry", func() {
					Expect(table.TableSize()).To(Equal(0))
				})
			})
		})

		Context("when the table is swaped and the lrp is deleted", func() {
			BeforeEach(func() {
				tempTable := routingtable.NewRoutingTable(logger, false)
				table.Swap(tempTable, freshDomains)
			})

			It("removes the entry", func() {
				Expect(table.TableSize()).To(Equal(0))
			})
		})
	})

	Describe("RouteCounts", func() {
		BeforeEach(func() {
			internalHostname := "internal"
			routes := createRoutingInfo(key.ContainerPort, []string{hostname1}, []string{internalHostname}, "", []uint32{5222}, "router-group-guid")
			afterDesiredLRP := createSchedulingInfoWithRoutes(key.ProcessGUID, 1, routes, logGuid, *currentTag)
			table.SetRoutes(nil, afterDesiredLRP)
			actualLRP1 := createActualLRP(key, endpoint1, domain)
			table.AddEndpoint(actualLRP1)
		})

		It("returns the right tcp route count", func() {
			Expect(table.TCPAssociationsCount()).To(Equal(1))
		})

		It("returns the right http route count", func() {
			Expect(table.HTTPAssociationsCount()).To(Equal(1))
		})

		It("returns the right internal route count", func() {
			Expect(table.InternalAssociationsCount()).To(Equal(2))
		})
	})

	Describe("AddEndpoint", func() {
		Context("when a desired LRP has instances field less than number of actual LRP instances", func() {
			var internalHostname string
			BeforeEach(func() {
				internalHostname = "internal-hostname"
				// Add internal routes
				routes := createRoutingInfo(key.ContainerPort, []string{hostname1}, []string{internalHostname}, "", []uint32{}, "")

				// Set routes on desired lrp
				afterDesiredLRP := createSchedulingInfoWithRoutes(key.ProcessGUID, 1, routes, logGuid, *currentTag)
				table.SetRoutes(nil, afterDesiredLRP)
			})

			It("only registers in the number of instances defined in the desired LRP", func() {
				actualLRP1 := createActualLRP(key, endpoint1, domain)
				actualLRP2 := createActualLRP(key, endpoint2, domain)
				actualLRP3 := createActualLRP(key, endpoint3, domain)
				tcpRouteMappings, messagesToEmit = table.AddEndpoint(actualLRP1)
				Expect(tcpRouteMappings).To(BeZero())
				expected := routingtable.MessagesToEmit{
					RegistrationMessages: []routingtable.RegistryMessage{
						routingtable.RegistryMessageFor(endpoint1, routingtable.Route{Hostname: hostname1, LogGUID: logGuid}),
					},
					InternalRegistrationMessages: []routingtable.RegistryMessage{
						routingtable.RegistryMessage{
							Host: endpoint1.ContainerIP,
							URIs: []string{
								internalHostname,
								fmt.Sprintf("%d.%s", 0, internalHostname),
							},
							Tags: map[string]string{
								"component": "route-emitter",
							},
							PrivateInstanceIndex: "0",
							App:                  logGuid,
						},
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
