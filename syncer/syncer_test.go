package syncer_test

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/apcera/nats"
	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter/fake_nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table/fake_routing_table"
	. "github.com/cloudfoundry-incubator/route-emitter/syncer"
	"github.com/cloudfoundry-incubator/runtime-schema/bbs/fake_bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	fake_metrics_sender "github.com/cloudfoundry/dropsonde/metric_sender/fake"
	"github.com/cloudfoundry/dropsonde/metrics"
	"github.com/cloudfoundry/gunk/diegonats"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const logGuid = "some-log-guid"

var _ = Describe("Syncer", func() {
	var (
		bbs            *fake_bbs.FakeRouteEmitterBBS
		natsClient     *diegonats.FakeNATSClient
		emitter        *fake_nats_emitter.FakeNATSEmitter
		table          *fake_routing_table.FakeRoutingTable
		syncer         *Syncer
		process        ifrit.Process
		syncMessages   routing_table.MessagesToEmit
		messagesToEmit routing_table.MessagesToEmit
		syncDuration   time.Duration

		routerStartMessages chan<- *nats.Msg
		fakeMetricSender    *fake_metrics_sender.FakeMetricSender
	)

	BeforeEach(func() {
		bbs = new(fake_bbs.FakeRouteEmitterBBS)
		natsClient = diegonats.NewFakeClient()
		emitter = &fake_nats_emitter.FakeNATSEmitter{}
		table = &fake_routing_table.FakeRoutingTable{}
		syncDuration = 10 * time.Second

		startMessages := make(chan *nats.Msg)
		routerStartMessages = startMessages

		natsClient.WhenSubscribing("router.start", func(callback nats.MsgHandler) error {
			go func() {
				for msg := range startMessages {
					callback(msg)
				}
			}()

			return nil
		})

		//what follows is fake data to distinguish between
		//the "sync" and "emit" codepaths
		dummyContainer := routing_table.Container{Host: "1.1.1.1", Port: 11}
		dummyMessage := routing_table.RegistryMessageFor(dummyContainer, routing_table.Routes{URIs: []string{"foo.com", "bar.com"}, LogGuid: logGuid})
		syncMessages = routing_table.MessagesToEmit{
			RegistrationMessages: []routing_table.RegistryMessage{dummyMessage},
		}

		dummyContainer = routing_table.Container{Host: "2.2.2.2", Port: 22}
		dummyMessage = routing_table.RegistryMessageFor(dummyContainer, routing_table.Routes{URIs: []string{"baz.com"}, LogGuid: logGuid})
		messagesToEmit = routing_table.MessagesToEmit{
			RegistrationMessages: []routing_table.RegistryMessage{dummyMessage},
		}

		table.SyncReturns(syncMessages)
		table.MessagesToEmitReturns(messagesToEmit)

		//Set up some BBS data
		bbs.RunningActualLRPsReturns([]models.ActualLRP{
			{
				ProcessGuid:  "process-guid-1",
				Index:        0,
				InstanceGuid: "instance-guid-1",
				Host:         "1.2.3.4",
				Ports: []models.PortMapping{
					{
						HostPort:      1234,
						ContainerPort: 5678,
					},
				},
			},
		}, nil)

		bbs.DesiredLRPsReturns([]models.DesiredLRP{
			{
				ProcessGuid: "process-guid-1",
				Routes:      []string{"route-1", "route-2"},
				LogGuid:     logGuid,
			},
		}, nil)

		fakeMetricSender = fake_metrics_sender.NewFakeMetricSender()
		metrics.Initialize(fakeMetricSender)
		table.RouteCountReturns(123)
	})

	JustBeforeEach(func() {
		logger := lagertest.NewTestLogger("test")
		syncer = NewSyncer(bbs, table, emitter, syncDuration, natsClient, logger)
		process = ifrit.Envoke(syncer)
	})

	AfterEach(func() {
		process.Signal(os.Interrupt)
		Eventually(process.Wait()).Should(Receive(BeNil()))
		close(routerStartMessages)
	})

	Describe("on startup", func() {
		It("should sync the table", func() {
			Ω(table.SyncCallCount()).Should(Equal(1))

			routes, containers := table.SyncArgsForCall(0)
			Ω(routes["process-guid-1"]).Should(Equal(routing_table.Routes{
				URIs:    []string{"route-1", "route-2"},
				LogGuid: logGuid,
			}))
			Ω(containers["process-guid-1"]).Should(Equal([]routing_table.Container{
				{Host: "1.2.3.4", Port: 1234},
			}))

			Ω(emitter.EmitCallCount()).Should(Equal(1))
			emittedMessages, _, _ := emitter.EmitArgsForCall(0)
			Ω(emittedMessages).Should(Equal(syncMessages))
		})
	})

	Describe("getting the heartbeat interval from the router", func() {
		var greetings chan *nats.Msg
		BeforeEach(func() {
			greetings = make(chan *nats.Msg, 3)
			natsClient.WhenPublishing("router.greet", func(msg *nats.Msg) error {
				greetings <- msg
				return nil
			})
		})

		Context("when the router emits a router.start", func() {
			JustBeforeEach(func() {
				routerStartMessages <- &nats.Msg{
					Data: []byte(`{"minimumRegisterIntervalInSeconds":1}`),
				}
			})

			It("should emit routes with the frequency of the passed-in-interval", func() {
				Eventually(emitter.EmitCallCount, 2).Should(Equal(2))
				emittedMessages, _, _ := emitter.EmitArgsForCall(1)
				Ω(emittedMessages).Should(Equal(messagesToEmit))
				t1 := time.Now()

				Eventually(emitter.EmitCallCount, 2).Should(Equal(3))
				emittedMessages, _, _ = emitter.EmitArgsForCall(2)
				Ω(emittedMessages).Should(Equal(messagesToEmit))
				t2 := time.Now()

				Ω(t2.Sub(t1)).Should(BeNumerically("~", 1*time.Second, 200*time.Millisecond))
			})

			It("passes a 'synced routes' counter to Emit", func() {
				Eventually(emitter.EmitCallCount, 2).Should(Equal(2))
				_, registerCounter, _ := emitter.EmitArgsForCall(1)
				Expect(string(*registerCounter)).To(Equal("RoutesSynced"))
			})

			It("should only greet the router once", func() {
				Eventually(greetings).Should(Receive())
				Consistently(greetings, 1).ShouldNot(Receive())
			})

			It("sends a 'routes total' metric", func() {
				Eventually(func() float64 {
					return fakeMetricSender.GetValue("RoutesTotal").Value
				}, 2).Should(BeEquivalentTo(123))
			})
		})

		Context("when the router does not emit a router.start", func() {
			It("should keep greeting the router until it gets an interval", func() {
				//get the first greeting
				Eventually(greetings, 2).Should(Receive())

				//get the second greeting, and respond
				var msg *nats.Msg
				Eventually(greetings, 2).Should(Receive(&msg))
				go natsClient.Publish(msg.Reply, []byte(`{"minimumRegisterIntervalInSeconds":1}`))

				//should now be emitting regularly at the specified interval
				Eventually(emitter.EmitCallCount, 2).Should(Equal(2))
				emittedMessages, _, _ := emitter.EmitArgsForCall(1)
				Ω(emittedMessages).Should(Equal(messagesToEmit))
				t1 := time.Now()

				Eventually(emitter.EmitCallCount, 2).Should(Equal(3))
				emittedMessages, _, _ = emitter.EmitArgsForCall(2)
				Ω(emittedMessages).Should(Equal(messagesToEmit))
				t2 := time.Now()

				Ω(t2.Sub(t1)).Should(BeNumerically("~", 1*time.Second, 200*time.Millisecond))

				//should no longer be greeting the router
				Consistently(greetings).ShouldNot(Receive())
			})
		})

		Context("after getting the first interval, when a second interval arrives", func() {
			JustBeforeEach(func() {
				routerStartMessages <- &nats.Msg{
					Data: []byte(`{"minimumRegisterIntervalInSeconds":1}`),
				}
			})

			It("should modify its update rate", func() {
				routerStartMessages <- &nats.Msg{
					Data: []byte(`{"minimumRegisterIntervalInSeconds":2}`),
				}

				//first emit should be pretty quick, it is in response to the incoming heartbeat interval
				Eventually(emitter.EmitCallCount, 0.2).Should(Equal(2))
				emittedMessages, _, _ := emitter.EmitArgsForCall(1)
				Ω(emittedMessages).Should(Equal(messagesToEmit))
				t1 := time.Now()

				//subsequent emit should follow the interval
				Eventually(emitter.EmitCallCount, 3).Should(Equal(3))
				emittedMessages, _, _ = emitter.EmitArgsForCall(2)
				Ω(emittedMessages).Should(Equal(messagesToEmit))
				t2 := time.Now()
				Ω(t2.Sub(t1)).Should(BeNumerically("~", 2*time.Second, 200*time.Millisecond))
			})

			It("sends a 'routes total' metric", func() {
				Eventually(func() float64 {
					return fakeMetricSender.GetValue("RoutesTotal").Value
				}, 2*time.Second).Should(BeEquivalentTo(123))
			})
		})

		Context("if it never hears anything from a router anywhere", func() {
			It("should still be able to shutdown", func() {
				process.Signal(os.Interrupt)
				Eventually(process.Wait()).Should(Receive(BeNil()))
			})
		})
	})

	Describe("syncing", func() {
		BeforeEach(func() {
			syncDuration = 500 * time.Millisecond
		})

		It("should sync on the specified interval", func() {
			//we set the emit interval real high to avoid colliding with our sync interval
			routerStartMessages <- &nats.Msg{
				Data: []byte(`{"minimumRegisterIntervalInSeconds":10}`),
			}

			Eventually(table.SyncCallCount).Should(Equal(2))
			Eventually(emitter.EmitCallCount).Should(Equal(2))
			t1 := time.Now()

			Eventually(table.SyncCallCount).Should(Equal(3))
			Eventually(emitter.EmitCallCount).Should(Equal(3))
			t2 := time.Now()

			emittedMessages, _, _ := emitter.EmitArgsForCall(1)
			Ω(emittedMessages).Should(Equal(syncMessages))

			emittedMessages, _, _ = emitter.EmitArgsForCall(2)
			Ω(emittedMessages).Should(Equal(syncMessages))

			Ω(t2.Sub(t1)).Should(BeNumerically("~", 500*time.Millisecond, 100*time.Millisecond))
		})

		It("sends a 'routes total' metric", func() {
			Eventually(func() float64 {
				return fakeMetricSender.GetValue("RoutesTotal").Value
			}).Should(BeEquivalentTo(123))
		})

		It("passes a 'synced routes' counter to Emit", func() {
			Eventually(emitter.EmitCallCount).Should(Equal(1))
			_, registerCounter, _ := emitter.EmitArgsForCall(0)
			Expect(string(*registerCounter)).To(Equal("RoutesSynced"))
		})

		Context("when fetching actuals fails", func() {
			BeforeEach(func() {
				lock := &sync.Mutex{}
				calls := 0

				bbs.RunningActualLRPsStub = func() ([]models.ActualLRP, error) {
					lock.Lock()
					defer lock.Unlock()
					if calls == 0 {
						calls++
						return nil, errors.New("bam")
					}

					return []models.ActualLRP{}, nil
				}
			})

			It("should not call sync until the error resolves", func() {
				Ω(table.SyncCallCount()).Should(Equal(0))

				routerStartMessages <- &nats.Msg{
					Data: []byte(`{"minimumRegisterIntervalInSeconds":10}`),
				}

				Eventually(table.SyncCallCount).Should(Equal(1))
			})
		})

		Context("when fetching desireds fails", func() {
			BeforeEach(func() {
				lock := &sync.Mutex{}
				calls := 0
				bbs.DesiredLRPsStub = func() ([]models.DesiredLRP, error) {
					lock.Lock()
					defer lock.Unlock()
					if calls == 0 {
						calls++
						return nil, errors.New("bam")
					}

					return []models.DesiredLRP{}, nil
				}
			})

			It("should not call sync until the error resolves", func() {
				Ω(table.SyncCallCount()).Should(Equal(0))

				routerStartMessages <- &nats.Msg{
					Data: []byte(`{"minimumRegisterIntervalInSeconds":10}`),
				}

				Eventually(table.SyncCallCount).Should(Equal(1))
			})
		})
	})
})
