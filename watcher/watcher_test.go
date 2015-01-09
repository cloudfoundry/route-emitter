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

const logGuid = "some-log-guid"

var _ = Describe("Watcher", func() {
	const (
		expectedProcessGuid  = "process-guid"
		expectedInstanceGuid = "instance-guid"
		expectedHost         = "1.1.1.1"
		expectedExternalPort = 11000
	)
	var expectedRoutes = []string{"route-1", "route-2"}

	var (
		bbs                 *fake_bbs.FakeRouteEmitterBBS
		table               *fake_routing_table.FakeRoutingTable
		emitter             *fake_nats_emitter.FakeNATSEmitter
		watcher             *Watcher
		process             ifrit.Process
		dummyMessagesToEmit routing_table.MessagesToEmit

		desiredLRPCreateOrUpdates chan models.DesiredLRP
		desiredLRPDeletes         chan models.DesiredLRP
		desiredLRPErrors          chan error

		actualLRPCreateOrUpdates chan models.ActualLRP
		actualLRPDeletes         chan models.ActualLRP
		actualLRPErrors          chan error
	)

	BeforeEach(func() {
		bbs = new(fake_bbs.FakeRouteEmitterBBS)
		table = &fake_routing_table.FakeRoutingTable{}
		emitter = &fake_nats_emitter.FakeNATSEmitter{}
		logger := lagertest.NewTestLogger("test")

		dummyContainer := routing_table.Container{InstanceGuid: "instance-guid", Host: "1.1.1.1", Port: 11}
		dummyMessage := routing_table.RegistryMessageFor(dummyContainer, routing_table.Routes{URIs: []string{"foo.com", "bar.com"}, LogGuid: logGuid})
		dummyMessagesToEmit = routing_table.MessagesToEmit{
			RegistrationMessages: []routing_table.RegistryMessage{dummyMessage},
		}

		desiredLRPCreateOrUpdates = make(chan models.DesiredLRP)
		desiredLRPDeletes = make(chan models.DesiredLRP)
		desiredLRPErrors = make(chan error)

		actualLRPCreateOrUpdates = make(chan models.ActualLRP)
		actualLRPDeletes = make(chan models.ActualLRP)
		actualLRPErrors = make(chan error)

		bbs.WatchForDesiredLRPChangesReturns(desiredLRPCreateOrUpdates, desiredLRPDeletes, desiredLRPErrors)
		bbs.WatchForActualLRPChangesReturns(actualLRPCreateOrUpdates, actualLRPDeletes, actualLRPErrors)

		watcher = NewWatcher(bbs, table, emitter, logger)
		process = ifrit.Envoke(watcher)
	})

	AfterEach(func() {
		process.Signal(os.Interrupt)
		Eventually(process.Wait()).Should(Receive())
	})

	Describe("Desired LRP changes", func() {
		var desiredLRP models.DesiredLRP

		BeforeEach(func() {
			desiredLRP = models.DesiredLRP{
				Action: &models.RunAction{
					Path: "ls",
				},
				Domain:      "tests",
				ProcessGuid: expectedProcessGuid,
				Routes:      expectedRoutes,
				LogGuid:     logGuid,
			}
		})

		Context("when a create/update (includes an after) change arrives", func() {
			BeforeEach(func() {
				table.SetRoutesReturns(dummyMessagesToEmit)

				desiredLRPCreateOrUpdates <- desiredLRP
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

		Context("when the change is a delete (no after)", func() {
			BeforeEach(func() {
				table.RemoveRoutesReturns(dummyMessagesToEmit)

				desiredLRPDeletes <- desiredLRP
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

		Context("when watching for change fails", func() {
			var errorTime time.Time

			BeforeEach(func() {
				errorTime = time.Now()

				desiredLRPErrors <- errors.New("bbs watch failed")

				desiredLRPCreateOrUpdates <- desiredLRP
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
		var actualLRP models.ActualLRP

		BeforeEach(func() {
			actualLRP = models.ActualLRP{
				ActualLRPKey:          models.NewActualLRPKey(expectedProcessGuid, 1, "domain"),
				ActualLRPContainerKey: models.NewActualLRPContainerKey(expectedInstanceGuid, "cell-id"),
				ActualLRPNetInfo: models.NewActualLRPNetInfo(expectedHost, []models.PortMapping{
					{ContainerPort: 8080, HostPort: expectedExternalPort},
				}),
				State: models.ActualLRPStateRunning,
			}
		})
		Context("when a create/update (includes an after) change arrives", func() {
			BeforeEach(func() {
				table.AddOrUpdateContainerReturns(dummyMessagesToEmit)

				actualLRPCreateOrUpdates <- actualLRP
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

		Context("when watching for change fails", func() {
			var errorTime time.Time

			BeforeEach(func() {
				errorTime = time.Now()

				actualLRPErrors <- errors.New("bbs watch failed")

				table.AddOrUpdateContainerReturns(dummyMessagesToEmit)

				actualLRPCreateOrUpdates <- actualLRP
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
				table.RemoveContainerReturns(dummyMessagesToEmit)

				actualLRPDeletes <- actualLRP
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
	})
})
