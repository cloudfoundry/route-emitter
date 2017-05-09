package routehandlers_test

import (
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/lager/lagertest"
	emitterfakes "code.cloudfoundry.org/route-emitter/emitter/fakes"
	"code.cloudfoundry.org/route-emitter/routehandlers"
	"code.cloudfoundry.org/route-emitter/routingtable"
	"code.cloudfoundry.org/route-emitter/routingtable/fakeroutingtable"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/event"
	"code.cloudfoundry.org/routing-info/tcp_routes"
	fake_metrics_sender "github.com/cloudfoundry/dropsonde/metric_sender/fake"
	"github.com/cloudfoundry/dropsonde/metrics"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("RoutingAPIHandler", func() {
	var (
		logger           lager.Logger
		fakeRoutingTable *fakeroutingtable.FakeTCPRoutingTable
		fakeEmitter      *emitterfakes.FakeRoutingAPIEmitter
		routeHandler     *routehandlers.RoutingAPIHandler
		fakeMetricSender *fake_metrics_sender.FakeMetricSender
	)

	BeforeEach(func() {
		logger = lagertest.NewTestLogger("test")
		fakeRoutingTable = new(fakeroutingtable.FakeTCPRoutingTable)
		fakeEmitter = new(emitterfakes.FakeRoutingAPIEmitter)
		routeHandler = routehandlers.NewRoutingAPIHandler(fakeRoutingTable, fakeEmitter, false)

		fakeMetricSender = fake_metrics_sender.NewFakeMetricSender()
		metrics.Initialize(fakeMetricSender, nil)
	})

	Describe("DesiredLRP Event", func() {
		var (
			desiredLRP    *models.DesiredLRP
			routingEvents event.RoutingEvents
		)

		BeforeEach(func() {
			externalPort := uint32(61000)
			containerPort := uint32(5222)
			tcpRoutes := tcp_routes.TCPRoutes{
				tcp_routes.TCPRoute{
					ExternalPort:  externalPort,
					ContainerPort: containerPort,
				},
			}
			desiredLRP = &models.DesiredLRP{
				ProcessGuid: "process-guid-1",
				Ports:       []uint32{containerPort},
				LogGuid:     "log-guid",
				Routes:      tcpRoutes.RoutingInfo(),
			}
			routingEvents = event.RoutingEvents{
				event.RoutingEvent{
					EventType: event.RouteRegistrationEvent,
					Key:       endpoint.RoutingKey{},
					Entry:     endpoint.RoutableEndpoints{},
				},
			}
		})

		Describe("HandleDesiredCreate", func() {
			JustBeforeEach(func() {
				routeHandler.HandleEvent(logger, models.NewDesiredLRPCreatedEvent(desiredLRP))
			})

			It("invokes AddRoutes on RoutingTable", func() {
				Expect(fakeRoutingTable.AddRoutesCallCount()).Should(Equal(1))
				lrp := fakeRoutingTable.AddRoutesArgsForCall(0)
				Expect(*lrp).Should(Equal(desiredLRP.DesiredLRPSchedulingInfo()))
			})

			Context("when there are routing events", func() {
				BeforeEach(func() {
					fakeRoutingTable.AddRoutesReturns(routingEvents)
				})

				It("invokes Emit on Emitter", func() {
					Expect(fakeEmitter.EmitCallCount()).Should(Equal(1))
					events := fakeEmitter.EmitArgsForCall(0)
					Expect(events).Should(Equal(routingEvents))
				})
			})

			Context("when there are no routing events", func() {
				BeforeEach(func() {
					fakeRoutingTable.AddRoutesReturns(event.RoutingEvents{})
				})

				It("does not invoke Emit on Emitter", func() {
					Expect(fakeEmitter.EmitCallCount()).Should(Equal(0))
				})
			})
		})

		Describe("HandleDesiredUpdate", func() {
			var after *models.DesiredLRP

			BeforeEach(func() {
				externalPort := uint32(62000)
				containerPort := uint32(5222)
				tcpRoutes := tcp_routes.TCPRoutes{
					tcp_routes.TCPRoute{
						ExternalPort:  externalPort,
						ContainerPort: containerPort,
					},
				}
				after = &models.DesiredLRP{
					ProcessGuid: "process-guid-1",
					Ports:       []uint32{containerPort},
					LogGuid:     "log-guid",
					Routes:      tcpRoutes.RoutingInfo(),
				}
			})

			JustBeforeEach(func() {
				routeHandler.HandleEvent(logger, models.NewDesiredLRPChangedEvent(desiredLRP, after))
			})

			It("invokes UpdateRoutes on RoutingTable", func() {
				Expect(fakeRoutingTable.UpdateRoutesCallCount()).Should(Equal(1))
				beforeLrp, afterLrp := fakeRoutingTable.UpdateRoutesArgsForCall(0)
				Expect(*beforeLrp).Should(Equal(desiredLRP.DesiredLRPSchedulingInfo()))
				Expect(*afterLrp).Should(Equal(after.DesiredLRPSchedulingInfo()))
			})

			Context("when there are routing events", func() {
				BeforeEach(func() {
					fakeRoutingTable.UpdateRoutesReturns(routingEvents)
				})

				It("invokes Emit on Emitter", func() {
					Expect(fakeEmitter.EmitCallCount()).Should(Equal(1))
					events := fakeEmitter.EmitArgsForCall(0)
					Expect(events).Should(Equal(routingEvents))
				})
			})

			Context("when there are no routing events", func() {
				BeforeEach(func() {
					fakeRoutingTable.UpdateRoutesReturns(event.RoutingEvents{})
				})

				It("does not invoke Emit on Emitter", func() {
					Expect(fakeEmitter.EmitCallCount()).Should(Equal(0))
				})
			})
		})

		Describe("HandleDesiredDelete", func() {
			BeforeEach(func() {
				unregistrationEvent := event.RoutingEvents{
					event.RoutingEvent{
						EventType: event.RouteUnregistrationEvent,
						Key:       endpoint.RoutingKey{},
						Entry:     endpoint.RoutableEndpoints{},
					},
				}
				fakeRoutingTable.RemoveRoutesReturns(unregistrationEvent)
			})
			JustBeforeEach(func() {
				routeHandler.HandleEvent(logger, models.NewDesiredLRPRemovedEvent(desiredLRP))
			})

			It("does not invoke AddRoutes on RoutingTable", func() {
				Expect(fakeRoutingTable.RemoveRoutesCallCount()).Should(Equal(1))
				Expect(fakeEmitter.EmitCallCount()).Should(Equal(1))
				lrp := fakeRoutingTable.RemoveRoutesArgsForCall(0)
				Expect(*lrp).Should(Equal(desiredLRP.DesiredLRPSchedulingInfo()))
			})
		})
	})

	Describe("ActualLRP Event", func() {
		var (
			actualLRP     *models.ActualLRPGroup
			routingEvents event.RoutingEvents
		)

		BeforeEach(func() {

			routingEvents = event.RoutingEvents{
				event.RoutingEvent{
					EventType: event.RouteRegistrationEvent,
					Key:       endpoint.RoutingKey{},
					Entry:     endpoint.RoutableEndpoints{},
				},
			}
		})

		Describe("HandleActualCreate", func() {
			JustBeforeEach(func() {
				routeHandler.HandleEvent(logger, models.NewActualLRPCreatedEvent(actualLRP))
			})

			Context("when state is Running", func() {
				BeforeEach(func() {
					actualLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								"some-ip",
								"container-ip",
								models.NewPortMapping(611006, 5222),
							),
							State: models.ActualLRPStateRunning,
						},
						Evacuating: nil,
					}
				})

				It("invokes AddEndpoint on RoutingTable", func() {
					Expect(fakeRoutingTable.AddEndpointCallCount()).Should(Equal(1))
					lrp := fakeRoutingTable.AddEndpointArgsForCall(0)
					Expect(lrp).Should(Equal(endpoint.NewActualLRPRoutingInfo(actualLRP)))
				})

				Context("when there are routing events", func() {
					BeforeEach(func() {
						fakeRoutingTable.AddEndpointReturns(routingEvents)
					})

					It("invokes Emit on Emitter", func() {
						Expect(fakeEmitter.EmitCallCount()).Should(Equal(1))
						events := fakeEmitter.EmitArgsForCall(0)
						Expect(events).Should(Equal(routingEvents))
					})
				})

				Context("when there are no routing events", func() {
					BeforeEach(func() {
						fakeRoutingTable.AddEndpointReturns(event.RoutingEvents{})
					})

					It("does not invoke Emit on Emitter", func() {
						Expect(fakeEmitter.EmitCallCount()).Should(Equal(0))
					})
				})
			})

			Context("when state is not in Running", func() {
				BeforeEach(func() {
					actualLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								"some-ip",
								"container-ip",
								models.NewPortMapping(611006, 5222),
							),
							State: models.ActualLRPStateClaimed,
						},
						Evacuating: nil,
					}
				})

				It("does not invoke AddEndpoint on RoutingTable", func() {
					Expect(fakeRoutingTable.AddEndpointCallCount()).Should(Equal(0))
				})

				It("does not invoke Emit on Emitter", func() {
					Expect(fakeEmitter.EmitCallCount()).Should(Equal(0))
				})
			})
		})

		Describe("HandleActualUpdate", func() {
			var (
				afterLRP *models.ActualLRPGroup
			)

			JustBeforeEach(func() {
				routeHandler.HandleEvent(logger, models.NewActualLRPChangedEvent(actualLRP, afterLRP))
			})

			Context("when after state is Running", func() {
				BeforeEach(func() {
					actualLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								"",
								"",
							),
							State: models.ActualLRPStateClaimed,
						},
						Evacuating: nil,
					}

					afterLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								"some-ip",
								"container-ip",
								models.NewPortMapping(611006, 5222),
							),
							State: models.ActualLRPStateRunning,
						},
						Evacuating: nil,
					}
				})

				It("invokes AddEndpoint on RoutingTable", func() {
					Expect(fakeRoutingTable.AddEndpointCallCount()).Should(Equal(1))
					lrp := fakeRoutingTable.AddEndpointArgsForCall(0)
					Expect(lrp.ActualLRP).Should(Equal(afterLRP.Instance))
				})

				Context("when there are routing events", func() {
					BeforeEach(func() {
						fakeRoutingTable.AddEndpointReturns(routingEvents)
					})

					It("invokes Emit on Emitter", func() {
						Expect(fakeEmitter.EmitCallCount()).Should(Equal(1))
						events := fakeEmitter.EmitArgsForCall(0)
						Expect(events).Should(Equal(routingEvents))
					})
				})

				Context("when there are no routing events", func() {
					BeforeEach(func() {
						fakeRoutingTable.AddEndpointReturns(event.RoutingEvents{})
					})

					It("does not invoke Emit on Emitter", func() {
						Expect(fakeEmitter.EmitCallCount()).Should(Equal(0))
					})
				})
			})

			Context("when after state is not Running and before state is Running", func() {
				BeforeEach(func() {
					actualLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								"some-ip",
								"container-ip",
								models.NewPortMapping(611006, 5222),
							),
							State: models.ActualLRPStateRunning,
						},
						Evacuating: nil,
					}

					afterLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								"",
								"",
							),
							State: models.ActualLRPStateCrashed,
						},
						Evacuating: nil,
					}
				})

				It("invokes RemoveEndpoint on RoutingTable", func() {
					Expect(fakeRoutingTable.RemoveEndpointCallCount()).Should(Equal(1))
					lrp := fakeRoutingTable.RemoveEndpointArgsForCall(0)
					Expect(lrp).Should(Equal(endpoint.NewActualLRPRoutingInfo(actualLRP)))
				})

				Context("when there are routing events", func() {
					BeforeEach(func() {
						fakeRoutingTable.RemoveEndpointReturns(routingEvents)
					})

					It("invokes Emit on Emitter", func() {
						Expect(fakeEmitter.EmitCallCount()).Should(Equal(1))
						events := fakeEmitter.EmitArgsForCall(0)
						Expect(events).Should(Equal(routingEvents))
					})
				})

				Context("when there are no routing events", func() {
					BeforeEach(func() {
						fakeRoutingTable.RemoveEndpointReturns(event.RoutingEvents{})
					})

					It("does not invoke Emit on Emitter", func() {
						Expect(fakeEmitter.EmitCallCount()).Should(Equal(0))
					})
				})
			})

			Context("when both after and before state is not Running", func() {
				BeforeEach(func() {
					actualLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", ""),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								"",
								"",
							),
							State: models.ActualLRPStateUnclaimed,
						},
						Evacuating: nil,
					}

					afterLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								"",
								"",
							),
							State: models.ActualLRPStateClaimed,
						},
						Evacuating: nil,
					}
				})

				It("does not invoke AddEndpoint on RoutingTable", func() {
					Expect(fakeRoutingTable.AddEndpointCallCount()).Should(Equal(0))
				})

				It("does not invoke RemoveEndpoint on RoutingTable", func() {
					Expect(fakeRoutingTable.RemoveEndpointCallCount()).Should(Equal(0))
				})

				It("does not invoke Emit on Emitter", func() {
					Expect(fakeEmitter.EmitCallCount()).Should(Equal(0))
				})
			})
		})

		Describe("HandleActualDelete", func() {
			JustBeforeEach(func() {
				routeHandler.HandleEvent(logger, models.NewActualLRPRemovedEvent(actualLRP))
			})

			Context("when state is Running", func() {
				BeforeEach(func() {
					actualLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								"some-ip",
								"container-ip",
								models.NewPortMapping(611006, 5222),
							),
							State: models.ActualLRPStateRunning,
						},
						Evacuating: nil,
					}
				})

				It("invokes RemoveEndpoint on RoutingTable", func() {
					Expect(fakeRoutingTable.RemoveEndpointCallCount()).Should(Equal(1))
					lrp := fakeRoutingTable.RemoveEndpointArgsForCall(0)
					Expect(lrp).Should(Equal(endpoint.NewActualLRPRoutingInfo(actualLRP)))
				})

				Context("when there are routing events", func() {
					BeforeEach(func() {
						fakeRoutingTable.RemoveEndpointReturns(routingEvents)
					})

					It("invokes Emit on Emitter", func() {
						Expect(fakeEmitter.EmitCallCount()).Should(Equal(1))
						events := fakeEmitter.EmitArgsForCall(0)
						Expect(events).Should(Equal(routingEvents))
					})
				})

				Context("when there are no routing events", func() {
					BeforeEach(func() {
						fakeRoutingTable.RemoveEndpointReturns(event.RoutingEvents{})
					})

					It("does not invoke Emit on Emitter", func() {
						Expect(fakeEmitter.EmitCallCount()).Should(Equal(0))
					})
				})
			})

			Context("when state is not in Running", func() {
				BeforeEach(func() {
					actualLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey("process-guid", 0, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								"",
								"",
							),
							State: models.ActualLRPStateClaimed,
						},
						Evacuating: nil,
					}
				})

				It("does not invoke RemoveEndpoint on RoutingTable", func() {
					Expect(fakeRoutingTable.RemoveEndpointCallCount()).Should(Equal(0))
				})

				It("does not invoke Emit on Emitter", func() {
					Expect(fakeEmitter.EmitCallCount()).Should(Equal(0))
				})
			})
		})
	})

	Describe("ShouldRefreshDesired", func() {
		var actualInfo *endpoint.ActualLRPRoutingInfo
		BeforeEach(func() {
			actualInfo = &endpoint.ActualLRPRoutingInfo{
				ActualLRP: &models.ActualLRP{
					ActualLRPKey:         models.NewActualLRPKey("process-guid-1", 0, "domain"),
					ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
					ActualLRPNetInfo: models.NewActualLRPNetInfo(
						"some-ip",
						"container-ip",
						models.NewPortMapping(61006, 5222),
						models.NewPortMapping(61007, 5223),
					),
					State:           models.ActualLRPStateRunning,
					ModificationTag: models.ModificationTag{Epoch: "abc", Index: 1},
				},
				Evacuating: false,
			}
		})

		Context("when corresponding desired state exists in the table", func() {
			BeforeEach(func() {
				fakeRoutingTable.GetRoutesReturns(endpoint.ExternalEndpointInfos{
					{RouterGroupGUID: "guid", Port: 61006},
				})
			})

			It("returns false", func() {
				Expect(routeHandler.ShouldRefreshDesired(actualInfo)).To(BeFalse())
			})
		})

		Context("when some ports are not known to the routing table", func() {
			BeforeEach(func() {
				fakeRoutingTable.GetRoutesStub = func(key endpoint.RoutingKey) endpoint.ExternalEndpointInfos {
					if key.ContainerPort != 5222 {
						return nil
					}

					return endpoint.ExternalEndpointInfos{
						{RouterGroupGUID: "guid", Port: 61006},
					}
				}
			})

			It("returns false", func() {
				Expect(routeHandler.ShouldRefreshDesired(actualInfo)).To(BeFalse())
			})
		})

		Context("when corresponding desired state does not exist in the table", func() {
			BeforeEach(func() {
				fakeRoutingTable.GetRoutesReturns(nil)
			})

			It("returns true", func() {
				Expect(routeHandler.ShouldRefreshDesired(actualInfo)).To(BeTrue())
			})
		})
	})

	Describe("RefreshDesired", func() {
		BeforeEach(func() {
			fakeRoutingTable.AddRoutesReturns(
				event.RoutingEvents{
					event.RoutingEvent{
						EventType: event.RouteRegistrationEvent,
						Key:       endpoint.RoutingKey{},
						Entry:     endpoint.RoutableEndpoints{},
					},
				},
			)
		})

		It("adds the desired info to the routing table", func() {
			modificationTag := models.ModificationTag{Epoch: "abc", Index: 1}
			externalPort := uint32(61000)
			containerPort := uint32(5222)
			tcpRoutes := tcp_routes.TCPRoutes{
				tcp_routes.TCPRoute{
					RouterGroupGuid: "router-group-guid",
					ExternalPort:    externalPort,
					ContainerPort:   containerPort,
				},
			}
			desiredInfo := &models.DesiredLRPSchedulingInfo{
				DesiredLRPKey: models.DesiredLRPKey{
					ProcessGuid: "process-guid-1",
					LogGuid:     "log-guid",
				},
				Routes:          *tcpRoutes.RoutingInfo(),
				ModificationTag: modificationTag,
			}
			routeHandler.RefreshDesired(logger, []*models.DesiredLRPSchedulingInfo{desiredInfo})

			Expect(fakeRoutingTable.AddRoutesCallCount()).To(Equal(1))
			info := fakeRoutingTable.AddRoutesArgsForCall(0)
			Expect(info).To(Equal(desiredInfo))
			Expect(fakeEmitter.EmitCallCount()).Should(Equal(1))
		})
	})

	Describe("Sync", func() {
		Context("when bbs server returns no data", func() {
			It("does not update the routing table", func() {
				routeHandler.Sync(logger, nil, nil, nil, nil)
				Expect(fakeRoutingTable.SwapCallCount()).Should(Equal(0))
			})
		})

		Context("when bbs server returns desired and actual lrps", func() {
			var (
				desiredInfo     []*models.DesiredLRPSchedulingInfo
				actualInfo      []*endpoint.ActualLRPRoutingInfo
				modificationTag models.ModificationTag
			)

			BeforeEach(func() {
				modificationTag = models.ModificationTag{Epoch: "abc", Index: 1}
				externalPort := uint32(61000)
				containerPort := uint32(5222)
				tcpRoutes := tcp_routes.TCPRoutes{
					tcp_routes.TCPRoute{
						RouterGroupGuid: "router-group-guid",
						ExternalPort:    externalPort,
						ContainerPort:   containerPort,
					},
				}

				desiredInfo = []*models.DesiredLRPSchedulingInfo{
					&models.DesiredLRPSchedulingInfo{
						DesiredLRPKey: models.DesiredLRPKey{
							ProcessGuid: "process-guid-1",
							LogGuid:     "log-guid",
						},
						Routes:          *tcpRoutes.RoutingInfo(),
						ModificationTag: modificationTag,
					},
				}

				actualInfo = []*endpoint.ActualLRPRoutingInfo{
					&endpoint.ActualLRPRoutingInfo{
						ActualLRP: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey("process-guid-1", 0, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey("instance-guid", "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								"some-ip",
								"container-ip",
								models.NewPortMapping(61006, 5222),
							),
							State:           models.ActualLRPStateRunning,
							ModificationTag: modificationTag,
						},
						Evacuating: false,
					},
				}

				fakeRoutingTable.SwapStub = func(t routingtable.TCPRoutingTable) event.RoutingEvents {
					routingEvents := event.RoutingEvents{
						event.RoutingEvent{
							EventType: event.RouteRegistrationEvent,
							Key:       endpoint.RoutingKey{},
							Entry:     endpoint.RoutableEndpoints{},
						},
					}
					return routingEvents
				}
			})

			Context("when emitting metrics in localMode", func() {
				BeforeEach(func() {
					routeHandler = routehandlers.NewRoutingAPIHandler(fakeRoutingTable, fakeEmitter, true)

					fakeEmitter.EmitReturns(1, 0, nil)
				})

				It("emits the TCPRouteCount", func() {
					routeHandler.Sync(logger, desiredInfo, actualInfo, nil, nil)
					Expect(fakeMetricSender.GetValue("TCPRouteCount").Value).To(BeEquivalentTo(1))
				})
			})

			It("updates the routing table", func() {
				routeHandler.Sync(logger, desiredInfo, actualInfo, nil, nil)
				Expect(fakeRoutingTable.SwapCallCount()).Should(Equal(1))
				tempRoutingTable := fakeRoutingTable.SwapArgsForCall(0)
				Expect(tempRoutingTable.RouteCount()).To(Equal(1))
				routingEvents := tempRoutingTable.GetRoutingEvents()
				Expect(routingEvents).To(HaveLen(1))
				routingEvent := routingEvents[0]

				key := endpoint.RoutingKey{
					ProcessGUID:   "process-guid-1",
					ContainerPort: 5222,
				}
				endpoints := map[endpoint.EndpointKey]endpoint.Endpoint{
					endpoint.NewEndpointKey("instance-guid", false): endpoint.NewEndpoint(
						"instance-guid", false, "some-ip", 61006, 5222, &modificationTag),
				}

				Expect(routingEvent.Key).Should(Equal(key))
				Expect(routingEvent.EventType).Should(Equal(event.RouteRegistrationEvent))
				externalInfo := []endpoint.ExternalEndpointInfo{
					endpoint.NewExternalEndpointInfo("router-group-guid", 61000),
				}
				expectedEntry := endpoint.NewRoutableEndpoints(
					externalInfo, endpoints, "log-guid", &modificationTag)
				Expect(routingEvent.Entry).Should(Equal(expectedEntry))
				Expect(fakeEmitter.EmitCallCount()).Should(Equal(1))
			})
		})
	})

	Describe("Emit", func() {
		var events event.RoutingEvents
		BeforeEach(func() {
			events = event.RoutingEvents{
				{
					EventType: event.RouteRegistrationEvent,
					Key:       endpoint.RoutingKey{},
					Entry:     endpoint.RoutableEndpoints{},
				},
			}
			fakeRoutingTable.GetRoutingEventsReturns(events)
		})

		It("emits all valid registration events", func() {
			routeHandler.Emit(logger)
			Expect(fakeRoutingTable.GetRoutingEventsCallCount()).To(Equal(1))
			Expect(fakeEmitter.EmitCallCount()).To(Equal(1))
			Expect(fakeEmitter.EmitArgsForCall(0)).To(Equal(events))
		})
	})
})
