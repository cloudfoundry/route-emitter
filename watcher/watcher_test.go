package watcher_test

import (
	"errors"
	"os"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/receptor/fake_receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"

	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter/fake_nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table/fake_routing_table"
	. "github.com/cloudfoundry-incubator/route-emitter/watcher"
	fake_metrics_sender "github.com/cloudfoundry/dropsonde/metric_sender/fake"
	"github.com/cloudfoundry/dropsonde/metrics"
)

const logGuid = "some-log-guid"

var _ = Describe("Watcher", func() {
	const (
		expectedProcessGuid             = "process-guid"
		expectedInstanceGuid            = "instance-guid"
		expectedHost                    = "1.1.1.1"
		expectedExternalPort            = 11000
		expectedAdditionalExternalPort  = 22000
		expectedContainerPort           = 11
		expectedAdditionalContainerPort = 22
	)

	var (
		receptorClient *fake_receptor.FakeClient
		table          *fake_routing_table.FakeRoutingTable
		emitter        *fake_nats_emitter.FakeNATSEmitter

		watcher *Watcher
		process ifrit.Process

		expectedRoutes     []string
		expectedRoutingKey routing_table.RoutingKey
		expectedCFRoute    cfroutes.CFRoute

		expectedAdditionalRoutes     []string
		expectedAdditionalRoutingKey routing_table.RoutingKey
		expectedAdditionalCFRoute    cfroutes.CFRoute

		dummyMessagesToEmit routing_table.MessagesToEmit
		fakeMetricSender    *fake_metrics_sender.FakeMetricSender
	)

	BeforeEach(func() {
		receptorClient = new(fake_receptor.FakeClient)
		table = &fake_routing_table.FakeRoutingTable{}
		emitter = &fake_nats_emitter.FakeNATSEmitter{}
		logger := lagertest.NewTestLogger("test")

		dummyEndpoint := routing_table.Endpoint{InstanceGuid: expectedInstanceGuid, Host: expectedHost, Port: expectedContainerPort}
		dummyMessage := routing_table.RegistryMessageFor(dummyEndpoint, routing_table.Routes{Hostnames: []string{"foo.com", "bar.com"}, LogGuid: logGuid})
		dummyMessagesToEmit = routing_table.MessagesToEmit{
			RegistrationMessages: []routing_table.RegistryMessage{dummyMessage},
		}

		watcher = NewWatcher(receptorClient, table, emitter, logger)

		expectedRoutes = []string{"route-1", "route-2"}
		expectedCFRoute = cfroutes.CFRoute{Hostnames: expectedRoutes, Port: expectedContainerPort}
		expectedRoutingKey = routing_table.RoutingKey{
			ProcessGuid:   expectedProcessGuid,
			ContainerPort: expectedContainerPort,
		}

		expectedAdditionalRoutes = []string{"additional-1", "additional-2"}
		expectedAdditionalCFRoute = cfroutes.CFRoute{Hostnames: expectedAdditionalRoutes, Port: expectedAdditionalContainerPort}
		expectedAdditionalRoutingKey = routing_table.RoutingKey{
			ProcessGuid:   expectedProcessGuid,
			ContainerPort: expectedAdditionalContainerPort,
		}
		fakeMetricSender = fake_metrics_sender.NewFakeMetricSender()
		metrics.Initialize(fakeMetricSender)
	})

	JustBeforeEach(func() {
		process = ifrit.Invoke(watcher)
	})

	AfterEach(func() {
		process.Signal(os.Interrupt)
		Eventually(process.Wait()).Should(Receive())
	})

	Describe("Desired LRP changes", func() {
		Context("when a create event occurs", func() {
			var desiredLRP receptor.DesiredLRPResponse

			BeforeEach(func() {
				table.SetRoutesReturns(dummyMessagesToEmit)

				eventSource := new(fake_receptor.FakeEventSource)
				receptorClient.SubscribeToEventsReturns(eventSource, nil)

				desiredLRP = receptor.DesiredLRPResponse{
					Action: &models.RunAction{
						Path: "ls",
					},
					Domain:      "tests",
					ProcessGuid: expectedProcessGuid,
					Ports:       []uint16{expectedContainerPort},
					Routes:      cfroutes.CFRoutes{expectedCFRoute}.RoutingInfo(),
					LogGuid:     logGuid,
				}

				eventSource.NextStub = func() (receptor.Event, error) {
					if eventSource.NextCallCount() == 1 {
						return receptor.NewDesiredLRPCreatedEvent(desiredLRP), nil
					} else {
						return nil, nil
					}
				}
			})

			It("should set the routes on the table", func() {
				Eventually(table.SetRoutesCallCount).Should(Equal(1))

				key, routes := table.SetRoutesArgsForCall(0)
				Ω(key).Should(Equal(expectedRoutingKey))
				Ω(routes).Should(Equal(routing_table.Routes{Hostnames: expectedRoutes, LogGuid: logGuid}))
			})

			It("sends a 'routes registered' metric", func() {
				Eventually(func() uint64 {
					return fakeMetricSender.GetCounter("RoutesRegistered")
				}).Should(BeEquivalentTo(2))
			})

			It("sends a 'routes unregistered' metric", func() {
				Eventually(func() uint64 {
					return fakeMetricSender.GetCounter("RoutesUnRegistered")
				}).Should(BeEquivalentTo(0))
			})

			It("should emit whatever the table tells it to emit", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				messagesToEmit := emitter.EmitArgsForCall(0)
				Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
			})

			Context("when there are multiple CF routes", func() {
				BeforeEach(func() {
					desiredLRP.Ports = []uint16{expectedContainerPort, expectedAdditionalContainerPort}
					desiredLRP.Routes = cfroutes.CFRoutes{expectedCFRoute, expectedAdditionalCFRoute}.RoutingInfo()
				})

				It("registers all of the routes on the table", func() {
					Eventually(table.SetRoutesCallCount).Should(Equal(2))

					key, routes := table.SetRoutesArgsForCall(0)
					Ω(key).Should(Equal(expectedRoutingKey))
					Ω(routes).Should(Equal(routing_table.Routes{Hostnames: expectedRoutes, LogGuid: logGuid}))

					key, routes = table.SetRoutesArgsForCall(1)
					Ω(key).Should(Equal(expectedAdditionalRoutingKey))
					Ω(routes).Should(Equal(routing_table.Routes{Hostnames: expectedAdditionalRoutes, LogGuid: logGuid}))
				})

				It("emits whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(2))

					messagesToEmit := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))

					messagesToEmit = emitter.EmitArgsForCall(1)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})
			})
		})

		Context("when a change event occurs", func() {
			var originalDesiredLRP receptor.DesiredLRPResponse
			var changedDesiredLRP receptor.DesiredLRPResponse

			BeforeEach(func() {
				table.SetRoutesReturns(dummyMessagesToEmit)

				eventSource := new(fake_receptor.FakeEventSource)
				receptorClient.SubscribeToEventsReturns(eventSource, nil)

				originalDesiredLRP = receptor.DesiredLRPResponse{
					Action: &models.RunAction{
						Path: "ls",
					},
					Domain:      "tests",
					ProcessGuid: expectedProcessGuid,
					LogGuid:     logGuid,
					Ports:       []uint16{expectedContainerPort},
				}
				changedDesiredLRP = receptor.DesiredLRPResponse{
					Action: &models.RunAction{
						Path: "ls",
					},
					Domain:      "tests",
					ProcessGuid: expectedProcessGuid,
					LogGuid:     logGuid,
					Ports:       []uint16{expectedContainerPort},
					Routes:      cfroutes.CFRoutes{{Hostnames: expectedRoutes, Port: expectedContainerPort}}.RoutingInfo(),
				}

				eventSource.NextStub = func() (receptor.Event, error) {
					if eventSource.NextCallCount() == 1 {
						return receptor.NewDesiredLRPChangedEvent(
							originalDesiredLRP,
							changedDesiredLRP,
						), nil
					} else {
						return nil, nil
					}
				}
			})

			It("should set the routes on the table", func() {
				Eventually(table.SetRoutesCallCount).Should(Equal(1))
				key, routes := table.SetRoutesArgsForCall(0)
				Ω(key).Should(Equal(expectedRoutingKey))
				Ω(routes).Should(Equal(routing_table.Routes{Hostnames: expectedRoutes, LogGuid: logGuid}))
			})

			It("sends a 'routes registered' metric", func() {
				Eventually(func() uint64 {
					return fakeMetricSender.GetCounter("RoutesRegistered")
				}).Should(BeEquivalentTo(2))
			})

			It("sends a 'routes unregistered' metric", func() {
				Eventually(func() uint64 {
					return fakeMetricSender.GetCounter("RoutesUnRegistered")
				}).Should(BeEquivalentTo(0))
			})

			It("should emit whatever the table tells it to emit", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				messagesToEmit := emitter.EmitArgsForCall(0)
				Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
			})

			Context("when CF routes are added without an associated container port", func() {
				BeforeEach(func() {
					changedDesiredLRP.Ports = []uint16{expectedContainerPort}
					changedDesiredLRP.Routes = cfroutes.CFRoutes{expectedCFRoute, expectedAdditionalCFRoute}.RoutingInfo()
				})

				It("registers all of the routes associated with a port on the table", func() {
					Eventually(table.SetRoutesCallCount).Should(Equal(1))

					key, routes := table.SetRoutesArgsForCall(0)
					Ω(key).Should(Equal(expectedRoutingKey))
					Ω(routes).Should(Equal(routing_table.Routes{Hostnames: expectedRoutes, LogGuid: logGuid}))
				})

				It("emits whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(1))

					messagesToEmit := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})
			})

			Context("when CF routes and container ports are added", func() {
				BeforeEach(func() {
					changedDesiredLRP.Ports = []uint16{expectedContainerPort, expectedAdditionalContainerPort}
					changedDesiredLRP.Routes = cfroutes.CFRoutes{expectedCFRoute, expectedAdditionalCFRoute}.RoutingInfo()
				})

				It("registers all of the routes on the table", func() {
					Eventually(table.SetRoutesCallCount).Should(Equal(2))

					key, routes := table.SetRoutesArgsForCall(0)
					Ω(key).Should(Equal(expectedRoutingKey))
					Ω(routes).Should(Equal(routing_table.Routes{Hostnames: expectedRoutes, LogGuid: logGuid}))

					key, routes = table.SetRoutesArgsForCall(1)
					Ω(key).Should(Equal(expectedAdditionalRoutingKey))
					Ω(routes).Should(Equal(routing_table.Routes{Hostnames: expectedAdditionalRoutes, LogGuid: logGuid}))
				})

				It("emits whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(2))

					messagesToEmit := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))

					messagesToEmit = emitter.EmitArgsForCall(1)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})
			})

			Context("when CF routes are removed", func() {
				BeforeEach(func() {
					changedDesiredLRP.Ports = []uint16{expectedContainerPort}
					changedDesiredLRP.Routes = cfroutes.CFRoutes{}.RoutingInfo()

					table.SetRoutesReturns(routing_table.MessagesToEmit{})
					table.RemoveRoutesReturns(dummyMessagesToEmit)
				})

				It("deletes the routes for the missng key", func() {
					Eventually(table.RemoveRoutesCallCount).Should(Equal(1))

					key := table.RemoveRoutesArgsForCall(0)
					Ω(key).Should(Equal(expectedRoutingKey))
				})

				It("emits whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(1))

					messagesToEmit := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})
			})

			Context("when container ports are removed", func() {
				BeforeEach(func() {
					changedDesiredLRP.Ports = []uint16{}
					changedDesiredLRP.Routes = cfroutes.CFRoutes{expectedCFRoute}.RoutingInfo()

					table.SetRoutesReturns(routing_table.MessagesToEmit{})
					table.RemoveRoutesReturns(dummyMessagesToEmit)
				})

				It("deletes the routes for the missng key", func() {
					Eventually(table.RemoveRoutesCallCount).Should(Equal(1))

					key := table.RemoveRoutesArgsForCall(0)
					Ω(key).Should(Equal(expectedRoutingKey))
				})

				It("emits whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(1))

					messagesToEmit := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})
			})
		})

		Context("when a delete event occurs", func() {
			var desiredLRP receptor.DesiredLRPResponse

			BeforeEach(func() {
				table.RemoveRoutesReturns(dummyMessagesToEmit)

				eventSource := new(fake_receptor.FakeEventSource)
				receptorClient.SubscribeToEventsReturns(eventSource, nil)

				desiredLRP = receptor.DesiredLRPResponse{
					Action: &models.RunAction{
						Path: "ls",
					},
					Domain:      "tests",
					ProcessGuid: expectedProcessGuid,
					Ports:       []uint16{expectedContainerPort},
					Routes:      cfroutes.CFRoutes{expectedCFRoute}.RoutingInfo(),
					LogGuid:     logGuid,
				}

				eventSource.NextStub = func() (receptor.Event, error) {
					if eventSource.NextCallCount() == 1 {
						return receptor.NewDesiredLRPRemovedEvent(desiredLRP), nil
					} else {
						return nil, nil
					}
				}
			})

			It("should remove the routes from the table", func() {
				Eventually(table.RemoveRoutesCallCount).Should(Equal(1))
				key := table.RemoveRoutesArgsForCall(0)
				Ω(key).Should(Equal(expectedRoutingKey))
			})

			It("should emit whatever the table tells it to emit", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))

				messagesToEmit := emitter.EmitArgsForCall(0)
				Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
			})

			Context("when there are multiple CF routes", func() {
				BeforeEach(func() {
					desiredLRP.Ports = []uint16{expectedContainerPort, expectedAdditionalContainerPort}
					desiredLRP.Routes = cfroutes.CFRoutes{expectedCFRoute, expectedAdditionalCFRoute}.RoutingInfo()
				})

				It("should remove the routes from the table", func() {
					Eventually(table.RemoveRoutesCallCount).Should(Equal(2))

					key := table.RemoveRoutesArgsForCall(0)
					Ω(key).Should(Equal(expectedRoutingKey))

					key = table.RemoveRoutesArgsForCall(1)
					Ω(key).Should(Equal(expectedAdditionalRoutingKey))
				})

				It("emits whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(2))

					messagesToEmit := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))

					messagesToEmit = emitter.EmitArgsForCall(1)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})
			})
		})
	})

	Describe("Actual LRP changes", func() {
		Context("when a create event occurs", func() {
			var actualLRP receptor.ActualLRPResponse

			Context("when the resulting LRP is in the RUNNING state", func() {
				BeforeEach(func() {
					table.AddEndpointReturns(dummyMessagesToEmit)

					eventSource := new(fake_receptor.FakeEventSource)
					receptorClient.SubscribeToEventsReturns(eventSource, nil)

					actualLRP = receptor.ActualLRPResponse{
						ProcessGuid:  expectedProcessGuid,
						Index:        1,
						Domain:       "domain",
						InstanceGuid: expectedInstanceGuid,
						CellID:       "cell-id",
						Address:      expectedHost,
						Ports: []receptor.PortMapping{
							{ContainerPort: expectedContainerPort, HostPort: expectedExternalPort},
							{ContainerPort: expectedAdditionalContainerPort, HostPort: expectedExternalPort},
						},
						State: receptor.ActualLRPStateRunning,
					}

					eventSource.NextStub = func() (receptor.Event, error) {
						if eventSource.NextCallCount() == 1 {
							return receptor.NewActualLRPCreatedEvent(actualLRP), nil
						} else {
							return nil, nil
						}
					}
				})

				It("should add/update the endpoints on the table", func() {
					Eventually(table.AddEndpointCallCount).Should(Equal(2))

					keys := routing_table.RoutingKeysFromActual(actualLRP)
					endpoints, err := routing_table.EndpointsFromActual(actualLRP)
					Ω(err).ShouldNot(HaveOccurred())

					key, endpoint := table.AddEndpointArgsForCall(0)
					Ω(keys).Should(ContainElement(key))
					Ω(endpoint).Should(Equal(endpoints[key.ContainerPort]))

					key, endpoint = table.AddEndpointArgsForCall(1)
					Ω(keys).Should(ContainElement(key))
					Ω(endpoint).Should(Equal(endpoints[key.ContainerPort]))
				})

				It("should emit whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(2))

					messagesToEmit := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})

				It("sends a 'routes registered' metric", func() {
					Eventually(func() uint64 {
						return fakeMetricSender.GetCounter("RoutesRegistered")
					}).Should(BeEquivalentTo(4))
				})

				It("sends a 'routes unregistered' metric", func() {
					Eventually(func() uint64 {
						return fakeMetricSender.GetCounter("RoutesUnRegistered")
					}).Should(BeEquivalentTo(0))
				})
			})

			Context("when the resulting LRP is not in the RUNNING state", func() {
				BeforeEach(func() {
					eventSource := new(fake_receptor.FakeEventSource)
					receptorClient.SubscribeToEventsReturns(eventSource, nil)

					actualLRP := receptor.ActualLRPResponse{
						ProcessGuid:  expectedProcessGuid,
						Index:        1,
						Domain:       "domain",
						InstanceGuid: expectedInstanceGuid,
						CellID:       "cell-id",
						Address:      expectedHost,
						Ports: []receptor.PortMapping{
							{ContainerPort: expectedContainerPort, HostPort: expectedExternalPort},
							{ContainerPort: expectedAdditionalContainerPort, HostPort: expectedExternalPort},
						},
						State: receptor.ActualLRPStateUnclaimed,
					}

					eventSource.NextStub = func() (receptor.Event, error) {
						if eventSource.NextCallCount() == 1 {
							return receptor.NewActualLRPCreatedEvent(actualLRP), nil
						} else {
							return nil, nil
						}
					}
				})

				It("doesn't add/update the endpoint on the table", func() {
					Consistently(table.AddEndpointCallCount).Should(Equal(0))
				})

				It("doesn't emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(0))
				})
			})
		})

		Context("when a change event occurs", func() {
			Context("when the resulting LRP is in the RUNNING state", func() {
				BeforeEach(func() {
					table.AddEndpointReturns(dummyMessagesToEmit)

					eventSource := new(fake_receptor.FakeEventSource)
					receptorClient.SubscribeToEventsReturns(eventSource, nil)

					beforeActualLRP := receptor.ActualLRPResponse{
						ProcessGuid:  expectedProcessGuid,
						Index:        1,
						Domain:       "domain",
						InstanceGuid: expectedInstanceGuid,
						CellID:       "cell-id",
						State:        receptor.ActualLRPStateClaimed,
					}
					afterActualLRP := receptor.ActualLRPResponse{
						ProcessGuid:  expectedProcessGuid,
						Index:        1,
						Domain:       "domain",
						InstanceGuid: expectedInstanceGuid,
						CellID:       "cell-id",
						Address:      expectedHost,
						Ports: []receptor.PortMapping{
							{ContainerPort: expectedContainerPort, HostPort: expectedExternalPort},
							{ContainerPort: expectedAdditionalContainerPort, HostPort: expectedAdditionalExternalPort},
						},
						State: receptor.ActualLRPStateRunning,
					}

					eventSource.NextStub = func() (receptor.Event, error) {
						if eventSource.NextCallCount() == 1 {
							return receptor.NewActualLRPChangedEvent(beforeActualLRP, afterActualLRP), nil
						} else {
							return nil, nil
						}
					}
				})

				It("should add/update the endpoint on the table", func() {
					Eventually(table.AddEndpointCallCount).Should(Equal(2))

					key, endpoint := table.AddEndpointArgsForCall(0)
					Ω(key).Should(Equal(expectedRoutingKey))
					Ω(endpoint).Should(Equal(routing_table.Endpoint{
						InstanceGuid:  expectedInstanceGuid,
						Host:          expectedHost,
						Port:          expectedExternalPort,
						ContainerPort: expectedContainerPort,
					}))

					key, endpoint = table.AddEndpointArgsForCall(1)
					Ω(key).Should(Equal(expectedAdditionalRoutingKey))
					Ω(endpoint).Should(Equal(routing_table.Endpoint{
						InstanceGuid:  expectedInstanceGuid,
						Host:          expectedHost,
						Port:          expectedAdditionalExternalPort,
						ContainerPort: expectedAdditionalContainerPort,
					}))
				})

				It("should emit whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(2))

					messagesToEmit := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})

				It("sends a 'routes registered' metric", func() {
					Eventually(func() uint64 {
						return fakeMetricSender.GetCounter("RoutesRegistered")
					}).Should(BeEquivalentTo(4))
				})

				It("sends a 'routes unregistered' metric", func() {
					Eventually(func() uint64 {
						return fakeMetricSender.GetCounter("RoutesUnRegistered")
					}).Should(BeEquivalentTo(0))
				})
			})

			Context("when the resulting LRP transitions away form the RUNNING state", func() {
				BeforeEach(func() {
					table.RemoveEndpointReturns(dummyMessagesToEmit)

					eventSource := new(fake_receptor.FakeEventSource)
					receptorClient.SubscribeToEventsReturns(eventSource, nil)

					beforeActualLRP := receptor.ActualLRPResponse{
						ProcessGuid:  expectedProcessGuid,
						Index:        1,
						Domain:       "domain",
						InstanceGuid: expectedInstanceGuid,
						CellID:       "cell-id",
						Address:      expectedHost,
						Ports: []receptor.PortMapping{
							{ContainerPort: expectedContainerPort, HostPort: expectedExternalPort},
							{ContainerPort: expectedAdditionalContainerPort, HostPort: expectedAdditionalExternalPort},
						},
						State: receptor.ActualLRPStateRunning,
					}
					afterActualLRP := receptor.ActualLRPResponse{
						ProcessGuid: expectedProcessGuid,
						Index:       1,
						Domain:      "domain",
						State:       receptor.ActualLRPStateUnclaimed,
					}

					eventSource.NextStub = func() (receptor.Event, error) {
						if eventSource.NextCallCount() == 1 {
							return receptor.NewActualLRPChangedEvent(beforeActualLRP, afterActualLRP), nil
						} else {
							return nil, nil
						}
					}
				})

				It("should remove the endpoint from the table", func() {
					Eventually(table.RemoveEndpointCallCount).Should(Equal(2))

					key, endpoint := table.RemoveEndpointArgsForCall(0)
					Ω(key).Should(Equal(expectedRoutingKey))
					Ω(endpoint).Should(Equal(routing_table.Endpoint{
						InstanceGuid:  expectedInstanceGuid,
						Host:          expectedHost,
						Port:          expectedExternalPort,
						ContainerPort: expectedContainerPort,
					}))

					key, endpoint = table.RemoveEndpointArgsForCall(1)
					Ω(key).Should(Equal(expectedAdditionalRoutingKey))
					Ω(endpoint).Should(Equal(routing_table.Endpoint{
						InstanceGuid:  expectedInstanceGuid,
						Host:          expectedHost,
						Port:          expectedAdditionalExternalPort,
						ContainerPort: expectedAdditionalContainerPort,
					}))
				})

				It("should emit whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(2))

					messagesToEmit := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})
			})

			Context("when the endpoint neither starts nor ends in the RUNNING state", func() {
				BeforeEach(func() {
					eventSource := new(fake_receptor.FakeEventSource)
					receptorClient.SubscribeToEventsReturns(eventSource, nil)

					beforeActualLRP := receptor.ActualLRPResponse{
						ProcessGuid: expectedProcessGuid,
						Index:       1,
						Domain:      "domain",
						State:       receptor.ActualLRPStateUnclaimed,
					}
					afterActualLRP := receptor.ActualLRPResponse{
						ProcessGuid:  expectedProcessGuid,
						Index:        1,
						Domain:       "domain",
						InstanceGuid: expectedInstanceGuid,
						CellID:       "cell-id",
						State:        receptor.ActualLRPStateClaimed,
					}

					eventSource.NextStub = func() (receptor.Event, error) {
						if eventSource.NextCallCount() == 1 {
							return receptor.NewActualLRPChangedEvent(beforeActualLRP, afterActualLRP), nil
						} else {
							return nil, nil
						}
					}
				})

				It("should not remove the endpoint", func() {
					Consistently(table.RemoveEndpointCallCount).Should(BeZero())
				})

				It("should not add or update the endpoint", func() {
					Consistently(table.AddEndpointCallCount).Should(BeZero())
				})

				It("should not emit anything", func() {
					Consistently(emitter.EmitCallCount).Should(BeZero())
				})
			})
		})

		Context("when a delete event occurs", func() {
			Context("when the actual is in the RUNNING state", func() {
				BeforeEach(func() {
					table.RemoveEndpointReturns(dummyMessagesToEmit)

					eventSource := new(fake_receptor.FakeEventSource)
					receptorClient.SubscribeToEventsReturns(eventSource, nil)

					actualLRP := receptor.ActualLRPResponse{
						ProcessGuid:  expectedProcessGuid,
						Index:        1,
						Domain:       "domain",
						InstanceGuid: expectedInstanceGuid,
						CellID:       "cell-id",
						Address:      expectedHost,
						Ports: []receptor.PortMapping{
							{ContainerPort: expectedContainerPort, HostPort: expectedExternalPort},
							{ContainerPort: expectedAdditionalContainerPort, HostPort: expectedAdditionalExternalPort},
						},
						State: receptor.ActualLRPStateRunning,
					}

					eventSource.NextStub = func() (receptor.Event, error) {
						if eventSource.NextCallCount() == 1 {
							return receptor.NewActualLRPRemovedEvent(actualLRP), nil
						} else {
							return nil, nil
						}
					}
				})

				It("should remove the endpoint from the table", func() {
					Eventually(table.RemoveEndpointCallCount).Should(Equal(2))

					key, endpoint := table.RemoveEndpointArgsForCall(0)
					Ω(key).Should(Equal(expectedRoutingKey))
					Ω(endpoint).Should(Equal(routing_table.Endpoint{
						InstanceGuid:  expectedInstanceGuid,
						Host:          expectedHost,
						Port:          expectedExternalPort,
						ContainerPort: expectedContainerPort,
					}))

					key, endpoint = table.RemoveEndpointArgsForCall(1)
					Ω(key).Should(Equal(expectedAdditionalRoutingKey))
					Ω(endpoint).Should(Equal(routing_table.Endpoint{
						InstanceGuid:  expectedInstanceGuid,
						Host:          expectedHost,
						Port:          expectedAdditionalExternalPort,
						ContainerPort: expectedAdditionalContainerPort,
					}))
				})

				It("should emit whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(2))

					messagesToEmit := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))

					messagesToEmit = emitter.EmitArgsForCall(1)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})
			})

			Context("when the actual is not in the RUNNING state", func() {
				BeforeEach(func() {
					eventSource := new(fake_receptor.FakeEventSource)
					receptorClient.SubscribeToEventsReturns(eventSource, nil)

					actualLRP := receptor.ActualLRPResponse{
						ProcessGuid: expectedProcessGuid,
						Index:       1,
						Domain:      "domain",
						State:       receptor.ActualLRPStateCrashed,
					}

					eventSource.NextStub = func() (receptor.Event, error) {
						if eventSource.NextCallCount() == 1 {
							return receptor.NewActualLRPRemovedEvent(actualLRP), nil
						} else {
							return nil, nil
						}
					}
				})

				It("doesn't remove the endpoint from the table", func() {
					Consistently(table.RemoveEndpointCallCount).Should(Equal(0))
				})

				It("doesn't emit", func() {
					Consistently(emitter.EmitCallCount).Should(Equal(0))
				})
			})
		})
	})

	Describe("Unrecognized events", func() {
		BeforeEach(func() {
			eventSource := new(fake_receptor.FakeEventSource)
			receptorClient.SubscribeToEventsReturns(eventSource, nil)

			eventSource.NextStub = func() (receptor.Event, error) {
				if eventSource.NextCallCount() == 1 {
					return unrecognizedEvent{}, nil
				} else {
					return nil, nil
				}
			}
		})

		It("does not emit any messages", func() {
			Consistently(emitter.EmitCallCount).Should(BeZero())
		})
	})

	Context("when the event source returns an error", func() {
		var subscribeErr, nextErr error

		BeforeEach(func() {
			subscribeErr = errors.New("subscribe-error")
			nextErr = errors.New("next-error")

			eventSource := new(fake_receptor.FakeEventSource)
			receptorClient.SubscribeToEventsStub = func() (receptor.EventSource, error) {
				if receptorClient.SubscribeToEventsCallCount() == 1 {
					return eventSource, nil
				}
				return nil, subscribeErr
			}

			eventSource.NextStub = func() (receptor.Event, error) {
				return nil, nextErr
			}
		})

		It("re-subscribes", func() {
			Eventually(receptorClient.SubscribeToEventsCallCount).Should(Equal(2))
		})

		Context("when re-subscribing fails", func() {
			It("returns an error", func() {
				Eventually(process.Wait()).Should(Receive(Equal(subscribeErr)))
			})
		})
	})

	Describe("interrupting the process", func() {
		BeforeEach(func() {
			eventSource := new(fake_receptor.FakeEventSource)
			receptorClient.SubscribeToEventsReturns(eventSource, nil)
		})

		It("should be possible to SIGINT the route emitter", func() {
			process.Signal(os.Interrupt)
			Eventually(process.Wait()).Should(Receive())
		})
	})
})

type unrecognizedEvent struct{}

func (u unrecognizedEvent) EventType() receptor.EventType {
	return "unrecognized-event"
}
