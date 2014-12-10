package watcher_test

import (
	"errors"
	"os"
	"time"

	"github.com/cloudfoundry-incubator/runtime-schema/bbs/fake_bbs"
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

var _ = Describe("Watcher", func() {
	var (
		bbs                 *fake_bbs.FakeRouteEmitterBBS
		table               *fake_routing_table.FakeRoutingTable
		emitter             *fake_nats_emitter.FakeNATSEmitter
		watcher             *Watcher
		process             ifrit.Process
		dummyMessagesToEmit routing_table.MessagesToEmit

		desiredLRPChanges chan<- models.DesiredLRPChange
		desiredLRPErrors  chan<- error

		actualLRPChanges chan<- models.ActualLRPChange
		actualLRPErrors  chan<- error
	)

	BeforeEach(func() {
		bbs = new(fake_bbs.FakeRouteEmitterBBS)
		table = &fake_routing_table.FakeRoutingTable{}
		emitter = &fake_nats_emitter.FakeNATSEmitter{}
		logger := lagertest.NewTestLogger("test")

		dummyContainer := routing_table.Container{Host: "1.1.1.1", Port: 11}
		dummyMessage := routing_table.RegistryMessageFor(dummyContainer, "foo.com", "bar.com")
		dummyMessagesToEmit = routing_table.MessagesToEmit{
			RegistrationMessages: []routing_table.RegistryMessage{dummyMessage},
		}

		desiredChChChChanges := make(chan models.DesiredLRPChange)
		desiredChChChErrors := make(chan error)

		actualChChChChanges := make(chan models.ActualLRPChange)
		actualChChChErrors := make(chan error)

		desiredLRPChanges = desiredChChChChanges
		desiredLRPErrors = desiredChChChErrors

		actualLRPChanges = actualChChChChanges
		actualLRPErrors = actualChChChErrors

		bbs.WatchForDesiredLRPChangesReturns(desiredChChChChanges, nil, desiredChChChErrors)
		bbs.WatchForActualLRPChangesReturns(actualChChChChanges, nil, actualChChChErrors)

		watcher = NewWatcher(bbs, table, emitter, logger)
		process = ifrit.Envoke(watcher)
	})

	AfterEach(func() {
		process.Signal(os.Interrupt)
		Eventually(process.Wait()).Should(Receive())
	})

	Describe("Desired LRP changes", func() {
		Context("when a create/update (includes an after) change arrives", func() {
			BeforeEach(func() {
				desiredChange := models.DesiredLRPChange{
					Before: nil,
					After: &models.DesiredLRP{
						Action: &models.RunAction{
							Path: "ls",
						},
						Domain:      "tests",
						ProcessGuid: "pg",
						Routes:      []string{"route-1", "route-2"},
					},
				}

				table.SetRoutesReturns(dummyMessagesToEmit)

				desiredLRPChanges <- desiredChange
			})

			It("should set the routes on the table", func() {
				Eventually(table.SetRoutesCallCount).Should(Equal(1))
				processGuid, routes := table.SetRoutesArgsForCall(0)
				Ω(processGuid).Should(Equal("pg"))
				Ω(routes).Should(Equal([]string{"route-1", "route-2"}))
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

		Context("when the change is a delete (no after)", func() {
			BeforeEach(func() {
				desiredChange := models.DesiredLRPChange{
					Before: &models.DesiredLRP{
						Action: &models.RunAction{
							Path: "ls",
						},
						Domain:      "tests",
						ProcessGuid: "pg",
						Routes:      []string{"route-1"},
					},
					After: nil,
				}

				table.RemoveRoutesReturns(dummyMessagesToEmit)

				desiredLRPChanges <- desiredChange
			})

			It("should remove the routes from the table", func() {
				Eventually(table.RemoveRoutesCallCount).Should(Equal(1))
				processGuid := table.RemoveRoutesArgsForCall(0)
				Ω(processGuid).Should(Equal("pg"))
			})

			It("should emit whatever the table tells it to emit", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				messagesToEmit, _, _ := emitter.EmitArgsForCall(0)
				Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
			})
		})

		Context("when watching for change fails", func() {
			var errorTime time.Time

			BeforeEach(func() {
				errorTime = time.Now()

				desiredLRPErrors <- errors.New("bbs watch failed")

				desiredChange := models.DesiredLRPChange{
					Before: nil,
					After: &models.DesiredLRP{
						Action: &models.RunAction{
							Path: "ls",
						},
						Domain:      "tests",
						ProcessGuid: "pg",
						Routes:      []string{"route-1", "route-2"},
					},
				}

				desiredLRPChanges <- desiredChange
			})

			It("should retry after 3 seconds", func() {
				Eventually(table.SetRoutesCallCount, 5).Should(Equal(1))
				Ω(time.Since(errorTime)).Should(BeNumerically("~", 3*time.Second, 200*time.Millisecond))
			})

			It("should be possible to SIGINT the route emitter", func() {
				process.Signal(os.Interrupt)
				Eventually(process.Wait()).Should(Receive())
			})
		})
	})

	Describe("Actual LRP changes", func() {
		Context("when a create/update (includes an after) change arrives", func() {
			BeforeEach(func() {
				actualChange := models.ActualLRPChange{
					Before: nil,
					After: &models.ActualLRP{
						ProcessGuid: "pg",
						Host:        "1.1.1.1",
						State:       models.ActualLRPStateRunning,
						Ports: []models.PortMapping{
							{ContainerPort: 8080, HostPort: 11},
						},
					},
				}

				table.AddOrUpdateContainerReturns(dummyMessagesToEmit)

				actualLRPChanges <- actualChange
			})

			It("should add/update the container on the table", func() {
				Eventually(table.AddOrUpdateContainerCallCount).Should(Equal(1))
				processGuid, container := table.AddOrUpdateContainerArgsForCall(0)
				Ω(processGuid).Should(Equal("pg"))
				Ω(container).Should(Equal(routing_table.Container{Host: "1.1.1.1", Port: 11}))
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

		Context("when watching for change fails", func() {
			var errorTime time.Time

			BeforeEach(func() {
				errorTime = time.Now()

				actualLRPErrors <- errors.New("bbs watch failed")

				actualChange := models.ActualLRPChange{
					Before: nil,
					After: &models.ActualLRP{
						ProcessGuid: "pg",
						Host:        "1.1.1.1",
						State:       models.ActualLRPStateRunning,
						Ports: []models.PortMapping{
							{ContainerPort: 8080, HostPort: 11},
						},
					},
				}

				table.AddOrUpdateContainerReturns(dummyMessagesToEmit)

				actualLRPChanges <- actualChange
			})

			It("should retry after 3 seconds", func() {
				Eventually(emitter.EmitCallCount, 5).Should(Equal(1))
				Ω(time.Since(errorTime)).Should(BeNumerically("~", 3*time.Second, 200*time.Millisecond))
			})

			It("should be possible to SIGINT the route emitter", func() {
				process.Signal(os.Interrupt)
				Eventually(process.Wait()).Should(Receive())
			})

		})

		Context("when the change is a delete (no after)", func() {
			BeforeEach(func() {
				actualChange := models.ActualLRPChange{
					Before: &models.ActualLRP{
						ProcessGuid: "pg",
						Host:        "1.1.1.1",
						State:       models.ActualLRPStateRunning,
						Ports: []models.PortMapping{
							{ContainerPort: 8080, HostPort: 11},
						},
					},
					After: nil,
				}

				table.RemoveContainerReturns(dummyMessagesToEmit)

				actualLRPChanges <- actualChange
			})

			It("should remove the container from the table", func() {
				Eventually(table.RemoveContainerCallCount).Should(Equal(1))
				processGuid, container := table.RemoveContainerArgsForCall(0)
				Ω(processGuid).Should(Equal("pg"))
				Ω(container).Should(Equal(routing_table.Container{Host: "1.1.1.1", Port: 11}))
			})

			It("should emit whatever the table tells it to emit", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				messagesToEmit, _, _ := emitter.EmitArgsForCall(0)
				Ω(messagesToEmit).Should(Equal(dummyMessagesToEmit))
			})
		})
	})
})
