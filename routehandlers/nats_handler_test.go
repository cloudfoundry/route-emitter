package routehandlers_test

import (
	"encoding/json"
	"fmt"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/route-emitter/emitter/fakes"
	"code.cloudfoundry.org/route-emitter/routehandlers"
	"code.cloudfoundry.org/route-emitter/routingtable"
	"code.cloudfoundry.org/route-emitter/routingtable/fakeroutingtable"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/routing-info/cfroutes"
	fake_metrics_sender "github.com/cloudfoundry/dropsonde/metric_sender/fake"
	"github.com/gogo/protobuf/proto"

	"github.com/cloudfoundry/dropsonde/metrics"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

const logGuid = "some-log-guid"

type randomEvent struct {
	proto.Message
}

func (e randomEvent) EventType() string {
	return "random"
}
func (e randomEvent) Key() string {
	return "random"
}

var _ = Describe("NATSHandler", func() {
	const (
		expectedDomain                  = "domain"
		expectedProcessGuid             = "process-guid"
		expectedInstanceGuid            = "instance-guid"
		expectedIndex                   = 0
		expectedHost                    = "1.1.1.1"
		expectedExternalPort            = 11000
		expectedAdditionalExternalPort  = 22000
		expectedContainerPort           = 11
		expectedAdditionalContainerPort = 22
		expectedRouteServiceUrl         = "https://so.good.com"
	)

	var (
		fakeTable   *fakeroutingtable.FakeNATSRoutingTable
		natsEmitter *fakes.FakeNATSEmitter

		expectedRoutes     []string
		expectedRoutingKey endpoint.RoutingKey
		expectedCFRoute    cfroutes.CFRoute

		expectedAdditionalRoutes     []string
		expectedAdditionalRoutingKey endpoint.RoutingKey
		expectedAdditionalCFRoute    cfroutes.CFRoute

		dummyMessagesToEmit routingtable.MessagesToEmit
		fakeMetricSender    *fake_metrics_sender.FakeMetricSender

		logger *lagertest.TestLogger

		routeHandler *routehandlers.NATSHandler
	)

	BeforeEach(func() {
		fakeTable = &fakeroutingtable.FakeNATSRoutingTable{}
		natsEmitter = &fakes.FakeNATSEmitter{}
		logger = lagertest.NewTestLogger("test")

		dummyEndpoint := routingtable.Endpoint{InstanceGuid: expectedInstanceGuid, Index: expectedIndex, Host: expectedHost, Port: expectedContainerPort}
		dummyMessageFoo := routingtable.RegistryMessageFor(dummyEndpoint, routingtable.Route{Hostname: "foo.com", LogGuid: logGuid})
		dummyMessageBar := routingtable.RegistryMessageFor(dummyEndpoint, routingtable.Route{Hostname: "bar.com", LogGuid: logGuid})
		dummyMessagesToEmit = routingtable.MessagesToEmit{
			RegistrationMessages: []routingtable.RegistryMessage{dummyMessageFoo, dummyMessageBar},
		}

		expectedRoutes = []string{"route-1", "route-2"}
		expectedCFRoute = cfroutes.CFRoute{Hostnames: expectedRoutes, Port: expectedContainerPort, RouteServiceUrl: expectedRouteServiceUrl}
		expectedRoutingKey = endpoint.RoutingKey{
			ProcessGUID:   expectedProcessGuid,
			ContainerPort: expectedContainerPort,
		}

		expectedAdditionalRoutes = []string{"additional-1", "additional-2"}
		expectedAdditionalCFRoute = cfroutes.CFRoute{Hostnames: expectedAdditionalRoutes, Port: expectedAdditionalContainerPort}
		expectedAdditionalRoutingKey = endpoint.RoutingKey{
			ProcessGUID:   expectedProcessGuid,
			ContainerPort: expectedAdditionalContainerPort,
		}
		fakeMetricSender = fake_metrics_sender.NewFakeMetricSender()
		metrics.Initialize(fakeMetricSender, nil)

		routeHandler = routehandlers.NewNATSHandler(fakeTable, natsEmitter, false)
	})

	Context("when an unrecoginzed event is received", func() {
		It("logs an error", func() {
			routeHandler.HandleEvent(logger, randomEvent{})
			Expect(logger).To(gbytes.Say("did-not-handle-unrecognizable-event"))
		})
	})

	Describe("DesiredLRP Event", func() {
		Context("DesiredLRPCreated Event", func() {
			var desiredLRP *models.DesiredLRP

			BeforeEach(func() {
				routes := cfroutes.CFRoutes{expectedCFRoute}.RoutingInfo()
				desiredLRP = &models.DesiredLRP{
					Action: models.WrapAction(&models.RunAction{
						User: "me",
						Path: "ls",
					}),
					Domain:      "tests",
					ProcessGuid: expectedProcessGuid,
					Ports:       []uint32{expectedContainerPort},
					Routes:      &routes,
					LogGuid:     logGuid,
				}

				fakeTable.SetRoutesReturns(dummyMessagesToEmit)
			})
			JustBeforeEach(func() {
				routeHandler.HandleEvent(logger, models.NewDesiredLRPCreatedEvent(desiredLRP))
			})

			It("should set the routes on the table", func() {
				Expect(fakeTable.SetRoutesCallCount()).To(Equal(1))

				key, routes, _ := fakeTable.SetRoutesArgsForCall(0)
				Expect(key).To(Equal(expectedRoutingKey))
				Expect(routes).To(ConsistOf(
					routingtable.Route{
						Hostname:        expectedRoutes[0],
						LogGuid:         logGuid,
						RouteServiceUrl: expectedRouteServiceUrl,
					},
					routingtable.Route{
						Hostname:        expectedRoutes[1],
						LogGuid:         logGuid,
						RouteServiceUrl: expectedRouteServiceUrl,
					},
				))
			})

			It("sends a 'routes registered' metric", func() {
				Expect(fakeMetricSender.GetCounter("RoutesRegistered")).To(BeEquivalentTo(2))
			})

			It("sends a 'routes unregistered' metric", func() {
				Expect(fakeMetricSender.GetCounter("RoutesUnregistered")).To(BeEquivalentTo(0))
			})

			It("should emit whatever the table tells it to emit", func() {
				Expect(natsEmitter.EmitCallCount()).To(Equal(1))
				messagesToEmit := natsEmitter.EmitArgsForCall(0)
				Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
			})

			Context("when router_group_guids are part of the desired_lrp", func() {
				var expectedRouterGroupGuid = "default-http"

				BeforeEach(func() {
					expectedCFRoute = cfroutes.CFRoute{Hostnames: expectedRoutes, Port: expectedContainerPort, RouteServiceUrl: expectedRouteServiceUrl, RouterGroupGuid: expectedRouterGroupGuid}
					routesNew := cfroutes.CFRoutes{expectedCFRoute}.RoutingInfo()
					desiredLRP.Routes = &routesNew
					dummyEndpoint := routingtable.Endpoint{InstanceGuid: expectedInstanceGuid, Index: expectedIndex, Host: expectedHost, Port: expectedContainerPort}
					dummyMessageFoo := routingtable.RegistryMessageFor(dummyEndpoint, routingtable.Route{Hostname: "foo.com", LogGuid: logGuid, RouterGroupGuid: expectedRouterGroupGuid})
					dummyMessageBar := routingtable.RegistryMessageFor(dummyEndpoint, routingtable.Route{Hostname: "bar.com", LogGuid: logGuid, RouterGroupGuid: expectedRouterGroupGuid})

					dummyMessagesToEmit = routingtable.MessagesToEmit{
						RegistrationMessages: []routingtable.RegistryMessage{dummyMessageFoo, dummyMessageBar},
					}
					fakeTable.SetRoutesReturns(dummyMessagesToEmit)
				})

				It("should set the routes on the table with the router group", func() {
					Expect(fakeTable.SetRoutesCallCount()).To(Equal(1))
					key, routes, _ := fakeTable.SetRoutesArgsForCall(0)
					Expect(key).To(Equal(expectedRoutingKey))
					Expect(routes).To(ConsistOf(
						routingtable.Route{
							Hostname:        expectedRoutes[0],
							LogGuid:         logGuid,
							RouteServiceUrl: expectedRouteServiceUrl,
							RouterGroupGuid: expectedRouterGroupGuid,
						},
						routingtable.Route{
							Hostname:        expectedRoutes[1],
							LogGuid:         logGuid,
							RouteServiceUrl: expectedRouteServiceUrl,
							RouterGroupGuid: expectedRouterGroupGuid,
						},
					))
				})

				It("should emit whatever the table tells it to emit", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(1))
					messagesToEmit := natsEmitter.EmitArgsForCall(0)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
				})
			})

			Context("when there are diego ssh-keys on the route", func() {
				var (
					foundRoutes bool
				)

				BeforeEach(func() {
					diegoSSHInfo := json.RawMessage([]byte(`{"ssh-key": "ssh-value"}`))

					routes := cfroutes.CFRoutes{expectedCFRoute}.RoutingInfo()
					routes["diego-ssh"] = &diegoSSHInfo

					desiredLRP.Routes = &routes
				})

				It("does not log them", func() {
					Expect(fakeTable.SetRoutesCallCount()).To(Equal(1))
					logs := logger.Logs()

					for _, log := range logs {
						if log.Data["routes"] != nil {
							Expect(log.Data["routes"]).ToNot(HaveKey("diego-ssh"))
							Expect(log.Data["routes"]).To(HaveKey("cf-router"))
							foundRoutes = true
						}
					}
					if !foundRoutes {
						Fail("Expected to find diego-ssh routes on desiredLRP")
					}

					Expect(len(*desiredLRP.Routes)).To(Equal(2))
				})
			})

			Context("when there is a route service binding to only one hostname for a route", func() {
				BeforeEach(func() {
					cfRoute1 := cfroutes.CFRoute{
						Hostnames:       []string{"route-1"},
						Port:            expectedContainerPort,
						RouteServiceUrl: expectedRouteServiceUrl,
					}
					cfRoute2 := cfroutes.CFRoute{
						Hostnames: []string{"route-2"},
						Port:      expectedContainerPort,
					}
					routes := cfroutes.CFRoutes{cfRoute1, cfRoute2}.RoutingInfo()
					desiredLRP.Routes = &routes
				})
				It("registers all of the routes on the table", func() {
					Expect(fakeTable.SetRoutesCallCount()).To(Equal(1))

					key, routes, _ := fakeTable.SetRoutesArgsForCall(0)
					Expect(key).To(Equal(expectedRoutingKey))
					Expect(routes).To(ConsistOf(
						routingtable.Route{
							Hostname:        "route-1",
							LogGuid:         logGuid,
							RouteServiceUrl: expectedRouteServiceUrl,
						},
						routingtable.Route{
							Hostname: "route-2",
							LogGuid:  logGuid,
						},
					))
				})

				It("emits whatever the table tells it to emit", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(1))

					messagesToEmit := natsEmitter.EmitArgsForCall(0)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
				})
			})

			Context("when there are multiple CF routes", func() {
				BeforeEach(func() {
					routes := cfroutes.CFRoutes{expectedCFRoute, expectedAdditionalCFRoute}.RoutingInfo()
					desiredLRP.Routes = &routes
				})

				It("registers all of the routes on the table", func() {
					Expect(fakeTable.SetRoutesCallCount()).To(Equal(2))

					key1, routes1, _ := fakeTable.SetRoutesArgsForCall(0)
					key2, routes2, _ := fakeTable.SetRoutesArgsForCall(1)
					var routes = []routingtable.Route{}
					routes = append(routes, routes1...)
					routes = append(routes, routes2...)

					Expect([]endpoint.RoutingKey{key1, key2}).To(ConsistOf(expectedRoutingKey, expectedAdditionalRoutingKey))
					Expect(routes).To(ConsistOf(
						routingtable.Route{
							Hostname:        expectedRoutes[0],
							LogGuid:         logGuid,
							RouteServiceUrl: expectedRouteServiceUrl,
						},
						routingtable.Route{
							Hostname:        expectedRoutes[1],
							LogGuid:         logGuid,
							RouteServiceUrl: expectedRouteServiceUrl,
						},
						routingtable.Route{
							Hostname: expectedAdditionalRoutes[0],
							LogGuid:  logGuid,
						},
						routingtable.Route{
							Hostname: expectedAdditionalRoutes[1],
							LogGuid:  logGuid,
						},
					))
				})

				It("emits whatever the table tells it to emit", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(2))

					messagesToEmit := natsEmitter.EmitArgsForCall(0)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))

					messagesToEmit = natsEmitter.EmitArgsForCall(1)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
				})
			})
		})

		Context("DesiredLRPChanged Event", func() {
			var originalDesiredLRP, changedDesiredLRP *models.DesiredLRP

			BeforeEach(func() {
				fakeTable.SetRoutesReturns(dummyMessagesToEmit)
				routes := cfroutes.CFRoutes{{Hostnames: expectedRoutes, Port: expectedContainerPort}}.RoutingInfo()

				originalDesiredLRP = &models.DesiredLRP{
					Action: models.WrapAction(&models.RunAction{
						User: "me",
						Path: "ls",
					}),
					Domain:      "tests",
					ProcessGuid: expectedProcessGuid,
					LogGuid:     logGuid,
					Routes:      &routes,
					Instances:   3,
				}
				changedDesiredLRP = &models.DesiredLRP{
					Action: models.WrapAction(&models.RunAction{
						User: "me",
						Path: "ls",
					}),
					Domain:          "tests",
					ProcessGuid:     expectedProcessGuid,
					LogGuid:         logGuid,
					Routes:          &routes,
					ModificationTag: &models.ModificationTag{Epoch: "abcd", Index: 1},
					Instances:       3,
				}
			})

			JustBeforeEach(func() {
				routeHandler.HandleEvent(logger, models.NewDesiredLRPChangedEvent(originalDesiredLRP, changedDesiredLRP))
			})

			Context("when scaling down the number of LRP instances", func() {
				BeforeEach(func() {
					changedDesiredLRP.Instances = 1

					fakeTable.EndpointsForIndexStub = func(key endpoint.RoutingKey, index int32) []routingtable.Endpoint {
						endpoint := routingtable.Endpoint{
							InstanceGuid:  fmt.Sprintf("instance-guid-%d", index),
							Index:         index,
							Host:          fmt.Sprintf("1.1.1.%d", index),
							Domain:        "domain",
							Port:          expectedExternalPort,
							ContainerPort: expectedContainerPort,
							Evacuating:    false,
						}

						return []routingtable.Endpoint{endpoint}
					}
				})

				It("removes route endpoints for instances that are no longer desired", func() {
					Expect(fakeTable.RemoveEndpointCallCount()).To(Equal(2))
				})
			})

			It("should set the routes on the table", func() {
				Expect(fakeTable.SetRoutesCallCount()).To(Equal(1))
				key, routes, _ := fakeTable.SetRoutesArgsForCall(0)
				Expect(key).To(Equal(expectedRoutingKey))
				Expect(routes).To(ConsistOf(
					routingtable.Route{
						Hostname: expectedRoutes[0],
						LogGuid:  logGuid,
					},
					routingtable.Route{
						Hostname: expectedRoutes[1],
						LogGuid:  logGuid,
					},
				))
			})

			It("sends a 'routes registered' metric", func() {
				Expect(fakeMetricSender.GetCounter("RoutesRegistered")).To(BeEquivalentTo(2))
			})

			It("sends a 'routes unregistered' metric", func() {
				Expect(fakeMetricSender.GetCounter("RoutesUnregistered")).To(BeEquivalentTo(0))
			})

			It("should emit whatever the table tells it to emit", func() {
				Expect(natsEmitter.EmitCallCount()).To(Equal(1))
				messagesToEmit := natsEmitter.EmitArgsForCall(0)
				Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
			})

			Context("when router_group_guids are part of the desired_lrp", func() {
				var expectedRouterGroupGuid = "default-http"

				BeforeEach(func() {
					changedDesiredLRP.Instances = 1
					fakeTable.EndpointsForIndexStub = func(key endpoint.RoutingKey, index int32) []routingtable.Endpoint {
						endpoint := routingtable.Endpoint{
							InstanceGuid:  fmt.Sprintf("instance-guid-%d", index),
							Index:         index,
							Host:          fmt.Sprintf("1.1.1.%d", index),
							Domain:        "domain",
							Port:          expectedExternalPort,
							ContainerPort: expectedContainerPort,
							Evacuating:    false,
						}

						return []routingtable.Endpoint{endpoint}
					}
					fakeTable.SetRoutesReturns(dummyMessagesToEmit)
					routes := cfroutes.CFRoutes{{Hostnames: expectedRoutes, Port: expectedContainerPort, RouterGroupGuid: expectedRouterGroupGuid}}.RoutingInfo()

					originalDesiredLRP = &models.DesiredLRP{
						Action: models.WrapAction(&models.RunAction{
							User: "me",
							Path: "ls",
						}),
						Domain:      "tests",
						ProcessGuid: expectedProcessGuid,
						LogGuid:     logGuid,
						Routes:      &routes,
						Instances:   3,
					}
					routesNew := cfroutes.CFRoutes{{Hostnames: expectedRoutes, Port: expectedContainerPort, RouterGroupGuid: "other-group"}}.RoutingInfo()
					changedDesiredLRP = &models.DesiredLRP{
						Action: models.WrapAction(&models.RunAction{
							User: "me",
							Path: "ls",
						}),
						Domain:          "tests",
						ProcessGuid:     expectedProcessGuid,
						LogGuid:         logGuid,
						Routes:          &routesNew,
						ModificationTag: &models.ModificationTag{Epoch: "abcd", Index: 1},
						Instances:       3,
					}
				})

				It("should set the routes on the table with the router group", func() {
					Expect(fakeTable.SetRoutesCallCount()).To(Equal(1))
					key, routes, _ := fakeTable.SetRoutesArgsForCall(0)
					Expect(key).To(Equal(expectedRoutingKey))
					Expect(routes).To(ConsistOf(
						routingtable.Route{
							Hostname:        expectedRoutes[0],
							LogGuid:         logGuid,
							RouterGroupGuid: "other-group",
						},
						routingtable.Route{
							Hostname:        expectedRoutes[1],
							LogGuid:         logGuid,
							RouterGroupGuid: "other-group",
						},
					))
				})
			})

			Context("when there are diego ssh-keys on the route", func() {
				var foundRoutes bool

				BeforeEach(func() {
					diegoSSHInfo := json.RawMessage([]byte(`{"ssh-key": "ssh-value"}`))

					routes := cfroutes.CFRoutes{expectedCFRoute}.RoutingInfo()
					routes["diego-ssh"] = &diegoSSHInfo

					changedDesiredLRP.Routes = &routes
				})

				It("does not log them", func() {
					Expect(fakeTable.SetRoutesCallCount()).To(Equal(1))
					logs := logger.Logs()

					for _, log := range logs {
						if after, ok := log.Data["after"]; ok {
							afterData := after.(map[string]interface{})

							if afterData["routes"] != nil {
								Expect(afterData["routes"]).ToNot(HaveKey("diego-ssh"))
								Expect(afterData["routes"]).To(HaveKey("cf-router"))
								foundRoutes = true
							}
						}
					}
					if !foundRoutes {
						Fail("Expected to find diego-ssh routes on desiredLRP")
					}

					Expect(len(*changedDesiredLRP.Routes)).To(Equal(2))
				})
			})

			Context("when CF routes are added without an associated container port", func() {
				BeforeEach(func() {
					routes := cfroutes.CFRoutes{expectedCFRoute, expectedAdditionalCFRoute}.RoutingInfo()
					changedDesiredLRP.Routes = &routes
				})

				It("registers all of the routes associated with a port on the table", func() {
					Expect(fakeTable.SetRoutesCallCount()).To(Equal(2))

					key1, routes1, _ := fakeTable.SetRoutesArgsForCall(0)
					key2, routes2, _ := fakeTable.SetRoutesArgsForCall(1)
					var routes = []routingtable.Route{}
					routes = append(routes, routes1...)
					routes = append(routes, routes2...)

					Expect([]endpoint.RoutingKey{key1, key2}).To(ConsistOf(expectedRoutingKey, expectedAdditionalRoutingKey))
					Expect(routes).To(ConsistOf(
						routingtable.Route{
							Hostname:        expectedRoutes[0],
							LogGuid:         logGuid,
							RouteServiceUrl: expectedRouteServiceUrl,
						},
						routingtable.Route{
							Hostname:        expectedRoutes[1],
							LogGuid:         logGuid,
							RouteServiceUrl: expectedRouteServiceUrl,
						},
						routingtable.Route{
							Hostname: expectedAdditionalRoutes[0],
							LogGuid:  logGuid,
						},
						routingtable.Route{
							Hostname: expectedAdditionalRoutes[1],
							LogGuid:  logGuid,
						},
					))
				})

				It("emits whatever the table tells it to emit", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(2))

					messagesToEmit := natsEmitter.EmitArgsForCall(1)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
				})
			})

			Context("when CF routes and container ports are added", func() {
				BeforeEach(func() {
					routes := cfroutes.CFRoutes{expectedCFRoute, expectedAdditionalCFRoute}.RoutingInfo()
					changedDesiredLRP.Routes = &routes
				})

				It("registers all of the routes on the table", func() {
					Expect(fakeTable.SetRoutesCallCount()).To(Equal(2))

					key1, routes1, _ := fakeTable.SetRoutesArgsForCall(0)
					key2, routes2, _ := fakeTable.SetRoutesArgsForCall(1)
					var routes = []routingtable.Route{}
					routes = append(routes, routes1...)
					routes = append(routes, routes2...)

					Expect([]endpoint.RoutingKey{key1, key2}).To(ConsistOf(expectedRoutingKey, expectedAdditionalRoutingKey))
					Expect(routes).To(ConsistOf(
						routingtable.Route{
							Hostname:        expectedRoutes[0],
							LogGuid:         logGuid,
							RouteServiceUrl: expectedRouteServiceUrl,
						},
						routingtable.Route{
							Hostname:        expectedRoutes[1],
							LogGuid:         logGuid,
							RouteServiceUrl: expectedRouteServiceUrl,
						},
						routingtable.Route{
							Hostname: expectedAdditionalRoutes[0],
							LogGuid:  logGuid,
						},
						routingtable.Route{
							Hostname: expectedAdditionalRoutes[1],
							LogGuid:  logGuid,
						},
					))
				})

				It("emits whatever the table tells it to emit", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(2))

					messagesToEmit := natsEmitter.EmitArgsForCall(0)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))

					messagesToEmit = natsEmitter.EmitArgsForCall(1)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
				})
			})

			Context("when CF routes are removed", func() {
				BeforeEach(func() {
					routes := cfroutes.CFRoutes{}.RoutingInfo()
					changedDesiredLRP.Routes = &routes

					fakeTable.SetRoutesReturns(routingtable.MessagesToEmit{})
					fakeTable.RemoveRoutesReturns(dummyMessagesToEmit)
				})

				It("deletes the routes for the missng key", func() {
					Expect(fakeTable.RemoveRoutesCallCount()).To(Equal(1))

					key, modTag := fakeTable.RemoveRoutesArgsForCall(0)
					Expect(key).To(Equal(expectedRoutingKey))
					Expect(modTag).To(Equal(changedDesiredLRP.ModificationTag))
				})

				It("emits whatever the table tells it to emit", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(1))

					messagesToEmit := natsEmitter.EmitArgsForCall(0)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
				})
			})
		})

		Context("when a delete event occurs", func() {
			var desiredLRP *models.DesiredLRP

			BeforeEach(func() {
				fakeTable.RemoveRoutesReturns(dummyMessagesToEmit)
				routes := cfroutes.CFRoutes{expectedCFRoute}.RoutingInfo()
				desiredLRP = &models.DesiredLRP{
					Action: models.WrapAction(&models.RunAction{
						User: "me",
						Path: "ls",
					}),
					Domain:          "tests",
					ProcessGuid:     expectedProcessGuid,
					Ports:           []uint32{expectedContainerPort},
					Routes:          &routes,
					LogGuid:         logGuid,
					ModificationTag: &models.ModificationTag{Epoch: "defg", Index: 2},
				}
			})

			JustBeforeEach(func() {
				routeHandler.HandleEvent(logger, models.NewDesiredLRPRemovedEvent(desiredLRP))
			})

			It("should remove the routes from the table", func() {
				Expect(fakeTable.RemoveRoutesCallCount()).To(Equal(1))
				key, modTag := fakeTable.RemoveRoutesArgsForCall(0)
				Expect(key).To(Equal(expectedRoutingKey))
				Expect(modTag).To(Equal(desiredLRP.ModificationTag))
			})

			It("should emit whatever the table tells it to emit", func() {
				Expect(natsEmitter.EmitCallCount()).To(Equal(1))

				messagesToEmit := natsEmitter.EmitArgsForCall(0)
				Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
			})

			Context("when there are diego ssh-keys on the route", func() {
				var (
					foundRoutes bool
				)

				BeforeEach(func() {
					diegoSSHInfo := json.RawMessage([]byte(`{"ssh-key": "ssh-value"}`))

					routes := cfroutes.CFRoutes{expectedCFRoute}.RoutingInfo()
					routes["diego-ssh"] = &diegoSSHInfo

					desiredLRP.Routes = &routes
				})

				It("does not log them", func() {
					Expect(fakeTable.RemoveRoutesCallCount()).To(Equal(1))
					logs := logger.Logs()

					for _, log := range logs {
						if log.Data["routes"] != nil {
							Expect(log.Data["routes"]).ToNot(HaveKey("diego-ssh"))
							Expect(log.Data["routes"]).To(HaveKey("cf-router"))
							foundRoutes = true
						}
					}
					if !foundRoutes {
						Fail("Expected to find diego-ssh routes on desiredLRP")
					}

					Expect(len(*desiredLRP.Routes)).To(Equal(2))
				})
			})

			Context("when there are multiple CF routes", func() {
				BeforeEach(func() {
					routes := cfroutes.CFRoutes{expectedCFRoute, expectedAdditionalCFRoute}.RoutingInfo()
					desiredLRP.Routes = &routes
				})

				It("should remove the routes from the table", func() {
					Expect(fakeTable.RemoveRoutesCallCount()).To(Equal(2))

					key, modTag := fakeTable.RemoveRoutesArgsForCall(0)
					Expect(key).To(Equal(expectedRoutingKey))
					Expect(modTag).To(Equal(desiredLRP.ModificationTag))

					key, modTag = fakeTable.RemoveRoutesArgsForCall(1)
					Expect(key).To(Equal(expectedAdditionalRoutingKey))
					Expect(modTag).To(Equal(desiredLRP.ModificationTag))

					key, modTag = fakeTable.RemoveRoutesArgsForCall(0)
					Expect(key).To(Equal(expectedRoutingKey))
					Expect(modTag).To(Equal(desiredLRP.ModificationTag))
				})

				It("emits whatever the table tells it to emit", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(2))

					messagesToEmit := natsEmitter.EmitArgsForCall(0)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))

					messagesToEmit = natsEmitter.EmitArgsForCall(1)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
				})
			})
		})
	})

	Describe("Actual LRP changes", func() {
		Context("when a create event occurs", func() {
			var (
				actualLRPGroup       *models.ActualLRPGroup
				actualLRP            *models.ActualLRP
				actualLRPRoutingInfo *endpoint.ActualLRPRoutingInfo
			)

			Context("when the resulting LRP is in the RUNNING state", func() {
				BeforeEach(func() {
					actualLRP = &models.ActualLRP{
						ActualLRPKey:         models.NewActualLRPKey(expectedProcessGuid, expectedIndex, "domain"),
						ActualLRPInstanceKey: models.NewActualLRPInstanceKey(expectedInstanceGuid, "cell-id"),
						ActualLRPNetInfo: models.NewActualLRPNetInfo(
							expectedHost,
							models.NewPortMapping(expectedExternalPort, expectedContainerPort),
							models.NewPortMapping(expectedExternalPort, expectedAdditionalContainerPort),
						),
						State: models.ActualLRPStateRunning,
					}

					actualLRPGroup = &models.ActualLRPGroup{
						Instance: actualLRP,
					}

					actualLRPRoutingInfo = &endpoint.ActualLRPRoutingInfo{
						ActualLRP:  actualLRP,
						Evacuating: false,
					}
					fakeTable.AddEndpointReturns(dummyMessagesToEmit)
				})

				JustBeforeEach(func() {
					routeHandler.HandleEvent(logger, models.NewActualLRPCreatedEvent(actualLRPGroup))
				})

				It("should log the net info", func() {
					Expect(logger).To(gbytes.Say(
						fmt.Sprintf(
							`"net_info":\{"address":"%s","ports":\[\{"container_port":%d,"host_port":%d\},\{"container_port":%d,"host_port":%d\}\]\}`,
							expectedHost,
							expectedContainerPort,
							expectedExternalPort,
							expectedAdditionalContainerPort,
							expectedExternalPort,
						),
					))
				})

				It("should add/update the endpoints on the table", func() {
					Expect(fakeTable.AddEndpointCallCount()).To(Equal(2))

					keys := routingtable.RoutingKeysFromActual(actualLRP)
					endpoints, err := routingtable.EndpointsFromActual(actualLRPRoutingInfo)
					Expect(err).NotTo(HaveOccurred())

					key, endpoint := fakeTable.AddEndpointArgsForCall(0)
					Expect(keys).To(ContainElement(key))
					Expect(endpoint).To(Equal(endpoints[key.ContainerPort]))

					key, endpoint = fakeTable.AddEndpointArgsForCall(1)
					Expect(keys).To(ContainElement(key))
					Expect(endpoint).To(Equal(endpoints[key.ContainerPort]))
				})

				It("should emit whatever the table tells it to emit", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(2))

					messagesToEmit := natsEmitter.EmitArgsForCall(0)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
				})

				It("sends a 'routes registered' metric", func() {
					Expect(fakeMetricSender.GetCounter("RoutesRegistered")).To(BeEquivalentTo(4))
				})

				It("sends a 'routes unregistered' metric", func() {
					Expect(fakeMetricSender.GetCounter("RoutesUnregistered")).To(BeEquivalentTo(0))
				})
			})

			Context("when the resulting LRP is not in the RUNNING state", func() {
				JustBeforeEach(func() {
					actualLRP = &models.ActualLRP{
						ActualLRPKey:         models.NewActualLRPKey(expectedProcessGuid, expectedIndex, "domain"),
						ActualLRPInstanceKey: models.NewActualLRPInstanceKey(expectedInstanceGuid, "cell-id"),
						ActualLRPNetInfo: models.NewActualLRPNetInfo(
							expectedHost,
							models.NewPortMapping(expectedExternalPort, expectedContainerPort),
							models.NewPortMapping(expectedExternalPort, expectedAdditionalContainerPort),
						),
						State: models.ActualLRPStateUnclaimed,
					}

					actualLRPGroup = &models.ActualLRPGroup{
						Instance: actualLRP,
					}
				})

				It("should NOT log the net info", func() {
					Expect(logger).ToNot(gbytes.Say(
						fmt.Sprintf(
							`"net_info":\{"address":"%s","ports":\[\{"container_port":%d,"host_port":%d\},\{"container_port":%d,"host_port":%d\}\]\}`,
							expectedHost,
							expectedContainerPort,
							expectedExternalPort,
							expectedAdditionalContainerPort,
							expectedExternalPort,
						),
					))
				})

				It("doesn't add/update the endpoint on the table", func() {
					Expect(fakeTable.AddEndpointCallCount()).Should(Equal(0))
				})

				It("doesn't emit", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(0))
				})
			})
		})

		Context("when a change event occurs", func() {
			Context("when the resulting LRP is in the RUNNING state", func() {
				var (
					afterActualLRP, beforeActualLRP *models.ActualLRPGroup
				)

				BeforeEach(func() {
					fakeTable.AddEndpointReturns(dummyMessagesToEmit)

					beforeActualLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey(expectedProcessGuid, expectedIndex, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey(expectedInstanceGuid, "cell-id"),
							State:                models.ActualLRPStateClaimed,
						},
					}
					afterActualLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey(expectedProcessGuid, expectedIndex, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey(expectedInstanceGuid, "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								expectedHost,
								models.NewPortMapping(expectedExternalPort, expectedContainerPort),
								models.NewPortMapping(expectedAdditionalExternalPort, expectedAdditionalContainerPort),
							),
							State: models.ActualLRPStateRunning,
						},
					}
				})

				JustBeforeEach(func() {
					routeHandler.HandleEvent(logger, models.NewActualLRPChangedEvent(beforeActualLRP, afterActualLRP))
				})

				It("should log the new net info", func() {
					Expect(logger).To(gbytes.Say(
						fmt.Sprintf(
							`"net_info":\{"address":"%s","ports":\[\{"container_port":%d,"host_port":%d\},\{"container_port":%d,"host_port":%d\}\]\}`,
							expectedHost,
							expectedContainerPort,
							expectedExternalPort,
							expectedAdditionalContainerPort,
							expectedAdditionalExternalPort,
						),
					))
				})

				It("should add/update the endpoint on the table", func() {
					Expect(fakeTable.AddEndpointCallCount()).To(Equal(2))

					// Verify the arguments that were passed to AddEndpoint independent of which call was made first.
					type endpointArgs struct {
						key      endpoint.RoutingKey
						endpoint routingtable.Endpoint
					}
					args := make([]endpointArgs, 2)
					key, endpoint := fakeTable.AddEndpointArgsForCall(0)
					args[0] = endpointArgs{key, endpoint}
					key, endpoint = fakeTable.AddEndpointArgsForCall(1)
					args[1] = endpointArgs{key, endpoint}

					Expect(args).To(ConsistOf([]endpointArgs{
						endpointArgs{expectedRoutingKey, routingtable.Endpoint{
							InstanceGuid:    expectedInstanceGuid,
							Index:           expectedIndex,
							Host:            expectedHost,
							Domain:          expectedDomain,
							Port:            expectedExternalPort,
							ContainerPort:   expectedContainerPort,
							ModificationTag: &models.ModificationTag{},
						}},
						endpointArgs{expectedAdditionalRoutingKey, routingtable.Endpoint{
							InstanceGuid:    expectedInstanceGuid,
							Index:           expectedIndex,
							Host:            expectedHost,
							Domain:          expectedDomain,
							Port:            expectedAdditionalExternalPort,
							ContainerPort:   expectedAdditionalContainerPort,
							ModificationTag: &models.ModificationTag{},
						}},
					}))
				})

				It("should emit whatever the table tells it to emit", func() {
					Expect(natsEmitter.EmitCallCount()).Should(Equal(2))

					messagesToEmit := natsEmitter.EmitArgsForCall(0)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
				})

				It("sends a 'routes registered' metric", func() {
					Expect(fakeMetricSender.GetCounter("RoutesRegistered")).To(BeEquivalentTo(4))
				})

				It("sends a 'routes unregistered' metric", func() {
					Expect(fakeMetricSender.GetCounter("RoutesUnregistered")).To(BeEquivalentTo(0))
				})
			})

			Context("when the resulting LRP transitions away from the RUNNING state", func() {
				var (
					beforeActualLRP, afterActualLRP *models.ActualLRPGroup
				)

				BeforeEach(func() {
					beforeActualLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey(expectedProcessGuid, expectedIndex, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey(expectedInstanceGuid, "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								expectedHost,
								models.NewPortMapping(expectedExternalPort, expectedContainerPort),
								models.NewPortMapping(expectedAdditionalExternalPort, expectedAdditionalContainerPort),
							),
							State: models.ActualLRPStateRunning,
						},
					}
					afterActualLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey: models.NewActualLRPKey(expectedProcessGuid, expectedIndex, "domain"),
							State:        models.ActualLRPStateUnclaimed,
						},
					}
					fakeTable.RemoveEndpointReturns(dummyMessagesToEmit)
				})

				JustBeforeEach(func() {
					routeHandler.HandleEvent(logger, models.NewActualLRPChangedEvent(beforeActualLRP, afterActualLRP))
				})

				It("should log the previous net info", func() {
					Expect(logger).To(gbytes.Say(
						fmt.Sprintf(
							`"net_info":\{"address":"%s","ports":\[\{"container_port":%d,"host_port":%d\},\{"container_port":%d,"host_port":%d\}\]\}`,
							expectedHost,
							expectedContainerPort,
							expectedExternalPort,
							expectedAdditionalContainerPort,
							expectedAdditionalExternalPort,
						),
					))
				})

				It("should remove the endpoint from the table", func() {
					Expect(fakeTable.RemoveEndpointCallCount()).To(Equal(2))

					key, endpoint := fakeTable.RemoveEndpointArgsForCall(0)
					Expect(key).To(Equal(expectedRoutingKey))
					Expect(endpoint).To(Equal(routingtable.Endpoint{
						InstanceGuid:    expectedInstanceGuid,
						Index:           expectedIndex,
						Host:            expectedHost,
						Domain:          expectedDomain,
						Port:            expectedExternalPort,
						ContainerPort:   expectedContainerPort,
						ModificationTag: &models.ModificationTag{},
					}))

					key, endpoint = fakeTable.RemoveEndpointArgsForCall(1)
					Expect(key).To(Equal(expectedAdditionalRoutingKey))
					Expect(endpoint).To(Equal(routingtable.Endpoint{
						InstanceGuid:    expectedInstanceGuid,
						Index:           expectedIndex,
						Host:            expectedHost,
						Domain:          expectedDomain,
						Port:            expectedAdditionalExternalPort,
						ContainerPort:   expectedAdditionalContainerPort,
						ModificationTag: &models.ModificationTag{},
					}))

				})

				It("should emit whatever the table tells it to emit", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(2))

					messagesToEmit := natsEmitter.EmitArgsForCall(0)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
				})
			})

			Context("when the endpoint neither starts nor ends in the RUNNING state", func() {
				JustBeforeEach(func() {
					beforeActualLRP := &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey: models.NewActualLRPKey(expectedProcessGuid, expectedIndex, "domain"),
							State:        models.ActualLRPStateUnclaimed,
						},
					}
					afterActualLRP := &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey(expectedProcessGuid, expectedIndex, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey(expectedInstanceGuid, "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								expectedHost,
								models.NewPortMapping(expectedExternalPort, expectedContainerPort),
								models.NewPortMapping(expectedAdditionalExternalPort, expectedAdditionalContainerPort),
							),
							State: models.ActualLRPStateClaimed,
						},
					}
					routeHandler.HandleEvent(logger, models.NewActualLRPChangedEvent(beforeActualLRP, afterActualLRP))
				})

				It("should NOT log the net info", func() {
					Expect(logger).ToNot(gbytes.Say(
						fmt.Sprintf(
							`"net_info":\{"address":"%s","ports":\[\{"container_port":%d,"host_port":%d\},\{"container_port":%d,"host_port":%d\}\]\}`,
							expectedHost,
							expectedContainerPort,
							expectedExternalPort,
							expectedAdditionalContainerPort,
							expectedExternalPort,
						),
					))
				})

				It("should not remove the endpoint", func() {
					Expect(fakeTable.RemoveEndpointCallCount()).To(BeZero())
				})

				It("should not add or update the endpoint", func() {
					Expect(fakeTable.AddEndpointCallCount()).To(BeZero())
				})

				It("should not emit anything", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(0))
				})
			})

		})

		Context("when a delete event occurs", func() {
			Context("when the actual is in the RUNNING state", func() {
				var (
					actualLRP *models.ActualLRPGroup
				)

				BeforeEach(func() {
					fakeTable.RemoveEndpointReturns(dummyMessagesToEmit)

					actualLRP = &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey(expectedProcessGuid, expectedIndex, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey(expectedInstanceGuid, "cell-id"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								expectedHost,
								models.NewPortMapping(expectedExternalPort, expectedContainerPort),
								models.NewPortMapping(expectedAdditionalExternalPort, expectedAdditionalContainerPort),
							),
							State: models.ActualLRPStateRunning,
						},
					}
				})

				JustBeforeEach(func() {
					routeHandler.HandleEvent(logger, models.NewActualLRPRemovedEvent(actualLRP))
				})

				It("should log the previous net info", func() {
					Expect(logger).To(gbytes.Say(
						fmt.Sprintf(
							`"net_info":\{"address":"%s","ports":\[\{"container_port":%d,"host_port":%d\},\{"container_port":%d,"host_port":%d\}\]\}`,
							expectedHost,
							expectedContainerPort,
							expectedExternalPort,
							expectedAdditionalContainerPort,
							expectedAdditionalExternalPort,
						),
					))
				})

				It("should remove the endpoint from the table", func() {
					Expect(fakeTable.RemoveEndpointCallCount()).To(Equal(2))

					key, endpoint := fakeTable.RemoveEndpointArgsForCall(0)
					Expect(key).To(Equal(expectedRoutingKey))
					Expect(endpoint).To(Equal(routingtable.Endpoint{
						InstanceGuid:    expectedInstanceGuid,
						Index:           expectedIndex,
						Host:            expectedHost,
						Domain:          expectedDomain,
						Port:            expectedExternalPort,
						ContainerPort:   expectedContainerPort,
						ModificationTag: &models.ModificationTag{},
					}))

					key, endpoint = fakeTable.RemoveEndpointArgsForCall(1)
					Expect(key).To(Equal(expectedAdditionalRoutingKey))
					Expect(endpoint).To(Equal(routingtable.Endpoint{
						InstanceGuid:    expectedInstanceGuid,
						Index:           expectedIndex,
						Host:            expectedHost,
						Domain:          expectedDomain,
						Port:            expectedAdditionalExternalPort,
						ContainerPort:   expectedAdditionalContainerPort,
						ModificationTag: &models.ModificationTag{},
					}))

				})

				It("should emit whatever the table tells it to emit", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(2))

					messagesToEmit := natsEmitter.EmitArgsForCall(0)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))

					messagesToEmit = natsEmitter.EmitArgsForCall(1)
					Expect(messagesToEmit).To(Equal(dummyMessagesToEmit))
				})
			})

			Context("when the actual is not in the RUNNING state", func() {
				JustBeforeEach(func() {
					actualLRP := &models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey: models.NewActualLRPKey(expectedProcessGuid, expectedIndex, "domain"),
							ActualLRPNetInfo: models.NewActualLRPNetInfo(
								expectedHost,
								models.NewPortMapping(expectedExternalPort, expectedContainerPort),
								models.NewPortMapping(expectedAdditionalExternalPort, expectedAdditionalContainerPort),
							),
							State: models.ActualLRPStateCrashed,
						},
					}

					routeHandler.HandleEvent(logger, models.NewActualLRPRemovedEvent(actualLRP))
				})

				It("should NOT log the net info", func() {
					Expect(logger).ToNot(gbytes.Say(
						fmt.Sprintf(
							`"net_info":\{"address":"%s","ports":\[\{"container_port":%d,"host_port":%d\},\{"container_port":%d,"host_port":%d\}\]\}`,
							expectedHost,
							expectedContainerPort,
							expectedExternalPort,
							expectedAdditionalContainerPort,
							expectedExternalPort,
						),
					))
				})

				It("doesn't remove the endpoint from the table", func() {
					Expect(fakeTable.RemoveEndpointCallCount()).To(Equal(0))
				})

				It("doesn't emit", func() {
					Expect(natsEmitter.EmitCallCount()).To(Equal(0))
				})
			})
		})
	})

	Describe("Sync", func() {
		Context("when bbs server returns desired and actual lrps", func() {
			var (
				desiredInfo []*models.DesiredLRPSchedulingInfo
				actualInfo  []*endpoint.ActualLRPRoutingInfo
				domains     models.DomainSet

				endpoint1, endpoint2, endpoint3, endpoint4 routingtable.Endpoint
			)

			BeforeEach(func() {
				currentTag := &models.ModificationTag{Epoch: "abc", Index: 1}
				hostname1 := "foo.example.com"
				hostname2 := "bar.example.com"
				hostname3 := "baz.example.com"

				endpoint1 = routingtable.Endpoint{InstanceGuid: "ig-1",
					Host:            "1.1.1.1",
					Index:           0,
					Port:            11,
					ContainerPort:   8080,
					Evacuating:      false,
					ModificationTag: currentTag,
				}
				endpoint2 = routingtable.Endpoint{InstanceGuid: "ig-2",
					Host:            "2.2.2.2",
					Index:           0,
					Port:            22,
					ContainerPort:   8080,
					Evacuating:      false,
					ModificationTag: currentTag,
				}
				endpoint3 = routingtable.Endpoint{InstanceGuid: "ig-3",
					Host:            "2.2.2.2",
					Index:           1,
					Port:            23,
					ContainerPort:   8080,
					Evacuating:      false,
					ModificationTag: currentTag,
				}

				schedulingInfo1 := &models.DesiredLRPSchedulingInfo{
					DesiredLRPKey: models.NewDesiredLRPKey("pg-1", "tests", "lg1"),
					Routes: cfroutes.CFRoutes{
						cfroutes.CFRoute{
							Hostnames:       []string{hostname1},
							Port:            8080,
							RouteServiceUrl: "https://rs.example.com",
						},
					}.RoutingInfo(),
					Instances: 1,
				}

				schedulingInfo2 := &models.DesiredLRPSchedulingInfo{
					DesiredLRPKey: models.NewDesiredLRPKey("pg-2", "tests", "lg2"),
					Routes: cfroutes.CFRoutes{
						cfroutes.CFRoute{
							Hostnames: []string{hostname2},
							Port:      8080,
						},
					}.RoutingInfo(),
					Instances: 1,
				}

				schedulingInfo3 := &models.DesiredLRPSchedulingInfo{
					DesiredLRPKey: models.NewDesiredLRPKey("pg-3", "tests", "lg3"),
					Routes: cfroutes.CFRoutes{
						cfroutes.CFRoute{
							Hostnames: []string{hostname3},
							Port:      8080,
						},
					}.RoutingInfo(),
					Instances: 1,
				}

				actualLRPGroup1 := &models.ActualLRPGroup{
					Instance: &models.ActualLRP{
						ActualLRPKey:         models.NewActualLRPKey("pg-1", 0, "domain"),
						ActualLRPInstanceKey: models.NewActualLRPInstanceKey(endpoint1.InstanceGuid, "cell-id"),
						ActualLRPNetInfo:     models.NewActualLRPNetInfo(endpoint1.Host, models.NewPortMapping(endpoint1.Port, endpoint1.ContainerPort)),
						State:                models.ActualLRPStateRunning,
					},
				}

				actualLRPGroup2 := &models.ActualLRPGroup{
					Instance: &models.ActualLRP{
						ActualLRPKey:         models.NewActualLRPKey("pg-2", 0, "domain"),
						ActualLRPInstanceKey: models.NewActualLRPInstanceKey(endpoint2.InstanceGuid, "cell-id"),
						ActualLRPNetInfo:     models.NewActualLRPNetInfo(endpoint2.Host, models.NewPortMapping(endpoint2.Port, endpoint2.ContainerPort)),
						State:                models.ActualLRPStateRunning,
					},
				}

				actualLRPGroup3 := &models.ActualLRPGroup{
					Instance: &models.ActualLRP{
						ActualLRPKey:         models.NewActualLRPKey("pg-3", 1, "domain"),
						ActualLRPInstanceKey: models.NewActualLRPInstanceKey(endpoint3.InstanceGuid, "cell-id"),
						ActualLRPNetInfo:     models.NewActualLRPNetInfo(endpoint3.Host, models.NewPortMapping(endpoint3.Port, endpoint3.ContainerPort)),
						State:                models.ActualLRPStateRunning,
					},
				}

				desiredInfo = []*models.DesiredLRPSchedulingInfo{
					schedulingInfo1, schedulingInfo2, schedulingInfo3,
				}
				actualInfo = []*endpoint.ActualLRPRoutingInfo{
					endpoint.NewActualLRPRoutingInfo(actualLRPGroup1),
					endpoint.NewActualLRPRoutingInfo(actualLRPGroup2),
					endpoint.NewActualLRPRoutingInfo(actualLRPGroup3),
				}

				domains = models.NewDomainSet([]string{"domain"})

				fakeTable.SwapStub = func(t routingtable.NATSRoutingTable, d models.DomainSet) routingtable.MessagesToEmit {
					routes := routingtable.RoutesByRoutingKeyFromSchedulingInfos(desiredInfo)
					routesList := make([]routingtable.Route, 3)
					for _, route := range routes {
						routesList = append(routesList, route[0])
					}

					return routingtable.MessagesToEmit{
						RegistrationMessages: []routingtable.RegistryMessage{
							routingtable.RegistryMessageFor(endpoint1, routesList[0]),
							routingtable.RegistryMessageFor(endpoint2, routesList[1]),
							routingtable.RegistryMessageFor(endpoint3, routesList[2]),
						},
					}
				}
			})

			It("updates the routing table", func() {
				routeHandler.Sync(logger, desiredInfo, actualInfo, domains, nil)
				Expect(fakeTable.SwapCallCount()).Should(Equal(1))
				tempRoutingTable, swapDomains := fakeTable.SwapArgsForCall(0)
				Expect(tempRoutingTable.RouteCount()).To(Equal(3))
				Expect(swapDomains).To(Equal(domains))

				Expect(natsEmitter.EmitCallCount()).Should(Equal(1))
			})

			Context("when emitting metrics in localMode", func() {
				BeforeEach(func() {
					routeHandler = routehandlers.NewNATSHandler(fakeTable, natsEmitter, true)
					fakeTable.RouteCountReturns(5)
				})

				It("emits the HTTPRouteCount", func() {
					routeHandler.Sync(logger, desiredInfo, actualInfo, domains, nil)
					Expect(fakeMetricSender.GetValue("HTTPRouteCount").Value).To(BeEquivalentTo(5))
				})
			})

			Context("when NATS events are cached", func() {
				BeforeEach(func() {
					routes := cfroutes.CFRoutes{
						cfroutes.CFRoute{
							Hostnames: []string{"anungunrama.example.com"},
							Port:      8080,
						},
					}.RoutingInfo()
					desiredLRPEvent := models.NewDesiredLRPCreatedEvent(&models.DesiredLRP{
						ProcessGuid: "pg-4",
						Routes:      &routes,
						Instances:   1,
					})
					actualLRPEvent := models.NewActualLRPCreatedEvent(&models.ActualLRPGroup{
						Instance: &models.ActualLRP{
							ActualLRPKey:         models.NewActualLRPKey("pg-4", 1, "domain"),
							ActualLRPInstanceKey: models.NewActualLRPInstanceKey(endpoint4.InstanceGuid, "cell-id"),
							ActualLRPNetInfo:     models.NewActualLRPNetInfo(endpoint4.Host, models.NewPortMapping(endpoint4.Port, endpoint4.ContainerPort)),
							State:                models.ActualLRPStateRunning,
						},
					})

					cachedEvents := map[string]models.Event{
						desiredLRPEvent.Key(): desiredLRPEvent,
						actualLRPEvent.Key():  actualLRPEvent,
					}
					routeHandler.Sync(
						logger,
						desiredInfo,
						actualInfo,
						domains,
						cachedEvents,
					)
				})

				It("updates the routing table and emit cached events", func() {
					Expect(fakeTable.SwapCallCount()).Should(Equal(1))
					tempRoutingTable, _ := fakeTable.SwapArgsForCall(0)
					Expect(tempRoutingTable.RouteCount()).Should(Equal(4))
					Expect(natsEmitter.EmitCallCount()).Should(Equal(1))
				})
			})
		})
	})

	Describe("Emit", func() {
		var registrationMsgs routingtable.MessagesToEmit
		BeforeEach(func() {
			currentTag := &models.ModificationTag{Epoch: "abc", Index: 1}
			endpoint1 := routingtable.Endpoint{InstanceGuid: "ig-1", Host: "1.1.1.1", Index: 0, Port: 11, ContainerPort: 8080, Evacuating: false, ModificationTag: currentTag}
			endpoint2 := routingtable.Endpoint{InstanceGuid: "ig-2", Host: "2.2.2.2", Index: 0, Port: 22, ContainerPort: 8080, Evacuating: false, ModificationTag: currentTag}
			endpoint3 := routingtable.Endpoint{InstanceGuid: "ig-3", Host: "2.2.2.2", Index: 1, Port: 23, ContainerPort: 8080, Evacuating: false, ModificationTag: currentTag}
			route := routingtable.Route{}
			registrationMsgs = routingtable.MessagesToEmit{
				RegistrationMessages: []routingtable.RegistryMessage{
					routingtable.RegistryMessageFor(endpoint1, route),
					routingtable.RegistryMessageFor(endpoint2, route),
					routingtable.RegistryMessageFor(endpoint3, route),
				},
			}

			fakeTable.MessagesToEmitReturns(registrationMsgs)
			fakeTable.RouteCountReturns(3)
		})
		It("emits all registration events", func() {
			routeHandler.Emit(logger)
			Expect(fakeTable.MessagesToEmitCallCount()).To(Equal(1))
			Expect(natsEmitter.EmitCallCount()).To(Equal(1))
			Expect(natsEmitter.EmitArgsForCall(0)).To(Equal(registrationMsgs))
		})

		It("sends a 'routes total' metric", func() {
			routeHandler.Emit(logger)
			Expect(fakeMetricSender.GetValue("RoutesTotal").Value).To(BeEquivalentTo(3))
		})

		It("sends a 'synced routes' metric", func() {
			routeHandler.Emit(logger)
			Expect(fakeMetricSender.GetCounter("RoutesSynced")).To(BeEquivalentTo(3))
		})
	})

	Describe("RefreshDesired", func() {
		BeforeEach(func() {
			fakeTable.SetRoutesReturns(routingtable.MessagesToEmit{})
		})

		It("adds the desired info to the routing table", func() {
			desiredInfo := &models.DesiredLRPSchedulingInfo{
				DesiredLRPKey: models.NewDesiredLRPKey("pg-1", "tests", "lg1"),
				Routes: cfroutes.CFRoutes{
					cfroutes.CFRoute{
						Hostnames:       []string{"foo.example.com"},
						Port:            8080,
						RouteServiceUrl: "https://rs.example.com",
					},
				}.RoutingInfo(),
				Instances: 1,
			}
			routeHandler.RefreshDesired(logger, []*models.DesiredLRPSchedulingInfo{desiredInfo})

			Expect(fakeTable.SetRoutesCallCount()).To(Equal(1))
			key, routes, _ := fakeTable.SetRoutesArgsForCall(0)
			Expect(key).To(Equal(endpoint.RoutingKey{"pg-1", 8080}))
			Expect(routes).To(Equal([]routingtable.Route{{Hostname: "foo.example.com", LogGuid: "lg1", RouteServiceUrl: "https://rs.example.com"}}))
			Expect(natsEmitter.EmitCallCount()).Should(Equal(1))
		})
	})

	Describe("ShouldRefreshDesired", func() {
		var (
			actualInfo *endpoint.ActualLRPRoutingInfo
			hostname   string
		)
		BeforeEach(func() {
			currentTag := models.ModificationTag{Epoch: "abc", Index: 1}
			hostname = "foo.example.com"
			endpoint1 := routingtable.Endpoint{
				InstanceGuid:    "ig-1",
				Host:            "1.1.1.1",
				Index:           0,
				Port:            11,
				ContainerPort:   8080,
				Evacuating:      false,
				ModificationTag: &currentTag,
			}

			actualInfo = &endpoint.ActualLRPRoutingInfo{
				ActualLRP: &models.ActualLRP{
					ActualLRPKey:         models.NewActualLRPKey("pg-1", 0, "domain"),
					ActualLRPInstanceKey: models.NewActualLRPInstanceKey(endpoint1.InstanceGuid, "cell-id"),
					ActualLRPNetInfo: models.NewActualLRPNetInfo(endpoint1.Host,
						models.NewPortMapping(endpoint1.Port, endpoint1.ContainerPort),
						models.NewPortMapping(12, endpoint1.ContainerPort+1),
					),
					State:           models.ActualLRPStateRunning,
					ModificationTag: currentTag,
				},
				Evacuating: false,
			}
		})

		Context("when corresponding desired state exists in the table", func() {
			BeforeEach(func() {
				fakeTable.GetRoutesReturns([]routingtable.Route{
					routingtable.Route{Hostname: hostname, LogGuid: "skldjfls", RouteServiceUrl: "https://rs.example.com"},
				})
			})

			It("returns false", func() {
				Expect(routeHandler.ShouldRefreshDesired(actualInfo)).To(BeFalse())
			})
		})

		Context("when some ports are not known to the routing table", func() {
			BeforeEach(func() {
				fakeTable.GetRoutesStub = func(key endpoint.RoutingKey) []routingtable.Route {
					if key.ContainerPort != 8080 {
						return nil
					}

					return []routingtable.Route{
						routingtable.Route{Hostname: hostname, LogGuid: "skldjfls", RouteServiceUrl: "https://rs.example.com"},
					}
				}
			})

			It("returns false", func() {
				Expect(routeHandler.ShouldRefreshDesired(actualInfo)).To(BeFalse())
			})
		})

		Context("when corresponding desired state does not exist in the table", func() {
			BeforeEach(func() {
				fakeTable.GetRoutesReturns(nil)
			})

			It("returns true", func() {
				Expect(routeHandler.ShouldRefreshDesired(actualInfo)).To(BeTrue())
			})
		})
	})
})
