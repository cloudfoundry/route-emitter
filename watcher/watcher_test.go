package watcher_test

import (
	"errors"
	"os"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/receptor/fake_receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/cc_messages"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"

	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter/fake_nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table/fake_routing_table"
	. "github.com/cloudfoundry-incubator/route-emitter/watcher"
)

const logGuid = "some-log-guid"

var _ = Describe("Watcher", func() {
	const (
		expectedProcessGuid   = "process-guid"
		expectedInstanceGuid  = "instance-guid"
		expectedHost          = "1.1.1.1"
		expectedExternalPort  = 11000
		expectedContainerPort = uint16(11)
	)
	var expectedRoutes = []string{"route-1", "route-2"}

	var (
		receptorClient *fake_receptor.FakeClient
		table          *fake_routing_table.FakeRoutingTable
		emitter        *fake_nats_emitter.FakeNATSEmitter

		dummyMessagesToEmit routing_table.MessagesToEmit

		watcher *Watcher

		process ifrit.Process
	)

	BeforeEach(func() {
		receptorClient = new(fake_receptor.FakeClient)
		table = &fake_routing_table.FakeRoutingTable{}
		emitter = &fake_nats_emitter.FakeNATSEmitter{}
		logger := lagertest.NewTestLogger("test")

		dummyContainer := routing_table.Container{InstanceGuid: expectedInstanceGuid, Host: expectedHost, Port: expectedContainerPort}
		dummyMessage := routing_table.RegistryMessageFor(dummyContainer, routing_table.Routes{URIs: []string{"foo.com", "bar.com"}, LogGuid: logGuid})
		dummyMessagesToEmit = routing_table.MessagesToEmit{
			RegistrationMessages: []routing_table.RegistryMessage{dummyMessage},
		}

		watcher = NewWatcher(receptorClient, table, emitter, logger)
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
			BeforeEach(func() {
				table.SetRoutesReturns(dummyMessagesToEmit)

				eventSource := new(fake_receptor.FakeEventSource)
				receptorClient.SubscribeToEventsReturns(eventSource, nil)

				desiredLRP := receptor.DesiredLRPResponse{
					Action: &models.RunAction{
						Path: "ls",
					},
					Domain:      "tests",
					ProcessGuid: expectedProcessGuid,
					Routes:      cc_messages.NewRoutingInfo(expectedRoutes, expectedContainerPort),
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
				processGuid, routes := table.SetRoutesArgsForCall(0)
				Ω(processGuid).Should(Equal(expectedProcessGuid))
				Ω(routes).Should(Equal(routing_table.Routes{URIs: expectedRoutes, LogGuid: logGuid}))
			})

			It("passes a 'routes registered' counter to Emit", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				_, registerCounter, _ := emitter.EmitArgsForCall(0)
				Expect(string(*registerCounter)).To(Equal("RoutesRegistered"))
			})

			It("passes a 'routes unregistered' counter to Emit", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				_, _, unregisterCounter := emitter.EmitArgsForCall(0)
				Expect(string(*unregisterCounter)).To(Equal("RoutesUnregistered"))
			})

			It("should emit whatever the table tells it to emit", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				messagesToEmit, _, _ := emitter.EmitArgsForCall(0)
				Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
			})
		})

		Context("when a change event occurs", func() {
			BeforeEach(func() {
				table.SetRoutesReturns(dummyMessagesToEmit)

				eventSource := new(fake_receptor.FakeEventSource)
				receptorClient.SubscribeToEventsReturns(eventSource, nil)

				originalDesiredLRP := receptor.DesiredLRPResponse{
					Action: &models.RunAction{
						Path: "ls",
					},
					Domain:      "tests",
					ProcessGuid: expectedProcessGuid,
					LogGuid:     logGuid,
				}
				changedDesiredLRP := receptor.DesiredLRPResponse{
					Action: &models.RunAction{
						Path: "ls",
					},
					Domain:      "tests",
					ProcessGuid: expectedProcessGuid,
					Routes:      cc_messages.NewRoutingInfo(expectedRoutes, expectedContainerPort),
					LogGuid:     logGuid,
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
				processGuid, routes := table.SetRoutesArgsForCall(0)
				Ω(processGuid).Should(Equal(expectedProcessGuid))
				Ω(routes).Should(Equal(routing_table.Routes{URIs: expectedRoutes, LogGuid: logGuid}))
			})

			It("passes a 'routes registered' counter to Emit", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				_, registerCounter, _ := emitter.EmitArgsForCall(0)
				Expect(string(*registerCounter)).To(Equal("RoutesRegistered"))
			})

			It("passes a 'routes unregistered' counter to Emit", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				_, _, unregisterCounter := emitter.EmitArgsForCall(0)
				Expect(string(*unregisterCounter)).To(Equal("RoutesUnregistered"))
			})

			It("should emit whatever the table tells it to emit", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				messagesToEmit, _, _ := emitter.EmitArgsForCall(0)
				Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
			})
		})

		Context("when a delete event occurs", func() {
			BeforeEach(func() {
				table.RemoveRoutesReturns(dummyMessagesToEmit)

				eventSource := new(fake_receptor.FakeEventSource)
				receptorClient.SubscribeToEventsReturns(eventSource, nil)

				desiredLRP := receptor.DesiredLRPResponse{
					Action: &models.RunAction{
						Path: "ls",
					},
					Domain:      "tests",
					ProcessGuid: expectedProcessGuid,
					Routes:      cc_messages.NewRoutingInfo(expectedRoutes, expectedContainerPort),
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
				processGuid := table.RemoveRoutesArgsForCall(0)
				Ω(processGuid).Should(Equal(expectedProcessGuid))
			})

			It("should emit whatever the table tells it to emit", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				messagesToEmit, _, _ := emitter.EmitArgsForCall(0)
				Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
			})
		})
	})

	Describe("Actual LRP changes", func() {
		Context("when a create event occurs", func() {
			Context("when the resulting LRP is in the RUNNING state", func() {
				BeforeEach(func() {
					table.AddOrUpdateContainerReturns(dummyMessagesToEmit)

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
							{ContainerPort: 8080, HostPort: expectedExternalPort},
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

				It("should add/update the container on the table", func() {
					Eventually(table.AddOrUpdateContainerCallCount).Should(Equal(1))
					processGuid, container := table.AddOrUpdateContainerArgsForCall(0)
					Ω(processGuid).Should(Equal(expectedProcessGuid))
					Ω(container).Should(Equal(routing_table.Container{
						InstanceGuid: expectedInstanceGuid,
						Host:         expectedHost,
						Port:         expectedExternalPort,
					}))
				})

				It("should emit whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(1))
					messagesToEmit, _, _ := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})

				It("passes a 'routes registered' counter to Emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(1))
					_, registerCounter, _ := emitter.EmitArgsForCall(0)
					Expect(string(*registerCounter)).To(Equal("RoutesRegistered"))
				})

				It("passes a 'routes unregistered' counter to Emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(1))
					_, _, unregisterCounter := emitter.EmitArgsForCall(0)
					Expect(string(*unregisterCounter)).To(Equal("RoutesUnregistered"))
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
							{ContainerPort: 8080, HostPort: expectedExternalPort},
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

				It("doesn't add/update the container on the table", func() {
					Consistently(table.AddOrUpdateContainerCallCount).Should(Equal(0))
				})

				It("doesn't emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(0))
				})
			})
		})

		Context("when a change event occurs", func() {
			Context("when the resulting LRP is in the RUNNING state", func() {
				BeforeEach(func() {
					table.AddOrUpdateContainerReturns(dummyMessagesToEmit)

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
							{ContainerPort: 8080, HostPort: expectedExternalPort},
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

				It("should add/update the container on the table", func() {
					Eventually(table.AddOrUpdateContainerCallCount).Should(Equal(1))
					processGuid, container := table.AddOrUpdateContainerArgsForCall(0)
					Ω(processGuid).Should(Equal(expectedProcessGuid))
					Ω(container).Should(Equal(routing_table.Container{
						InstanceGuid: expectedInstanceGuid,
						Host:         expectedHost,
						Port:         expectedExternalPort,
					}))
				})

				It("should emit whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(1))
					messagesToEmit, _, _ := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})

				It("passes a 'routes registered' counter to Emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(1))
					_, registerCounter, _ := emitter.EmitArgsForCall(0)
					Expect(string(*registerCounter)).To(Equal("RoutesRegistered"))
				})

				It("passes a 'routes unregistered' counter to Emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(1))
					_, _, unregisterCounter := emitter.EmitArgsForCall(0)
					Expect(string(*unregisterCounter)).To(Equal("RoutesUnregistered"))
				})
			})

			Context("when the resulting LRP transitions away form the RUNNING state", func() {
				BeforeEach(func() {
					table.RemoveContainerReturns(dummyMessagesToEmit)

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
							{ContainerPort: 8080, HostPort: expectedExternalPort},
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

				It("should remove the container from the table", func() {
					Eventually(table.RemoveContainerCallCount).Should(Equal(1))
					processGuid, container := table.RemoveContainerArgsForCall(0)
					Ω(processGuid).Should(Equal(expectedProcessGuid))
					Ω(container).Should(Equal(routing_table.Container{
						InstanceGuid: expectedInstanceGuid,
						Host:         expectedHost,
						Port:         expectedExternalPort,
					}))
				})

				It("should emit whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(1))
					messagesToEmit, _, _ := emitter.EmitArgsForCall(0)
					Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
				})
			})

			Context("when the container neither starts nor ends in the RUNNING state", func() {
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

				It("should not remove the container", func() {
					Consistently(table.RemoveContainerCallCount).Should(BeZero())
				})

				It("should not add or update the container", func() {
					Consistently(table.AddOrUpdateContainerCallCount).Should(BeZero())
				})

				It("should not emit anything", func() {
					Consistently(emitter.EmitCallCount).Should(BeZero())
				})
			})
		})

		Context("when a delete event occurs", func() {
			Context("when the actual is in the RUNNING state", func() {
				BeforeEach(func() {
					table.RemoveContainerReturns(dummyMessagesToEmit)

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
							{ContainerPort: 8080, HostPort: expectedExternalPort},
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

				It("should remove the container from the table", func() {
					Eventually(table.RemoveContainerCallCount).Should(Equal(1))
					processGuid, container := table.RemoveContainerArgsForCall(0)
					Ω(processGuid).Should(Equal(expectedProcessGuid))
					Ω(container).Should(Equal(routing_table.Container{
						InstanceGuid: expectedInstanceGuid,
						Host:         expectedHost,
						Port:         expectedExternalPort,
					}))
				})

				It("should emit whatever the table tells it to emit", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(1))
					messagesToEmit, _, _ := emitter.EmitArgsForCall(0)
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

				It("doesn't remove the container from the table", func() {
					Consistently(table.RemoveContainerCallCount).Should(Equal(0))
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
