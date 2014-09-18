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
	"github.com/cloudfoundry/gibson"
	"github.com/cloudfoundry/yagnats/fakeyagnats"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Syncer", func() {
	var (
		bbs          *fake_bbs.FakeRouteEmitterBBS
		natsClient   *fakeyagnats.FakeApceraWrapper
		emitter      *fake_nats_emitter.FakeNATSEmitter
		table        *fake_routing_table.FakeRoutingTable
		syncer       *Syncer
		process      ifrit.Process
		syncMessages routing_table.MessagesToEmit
		emitMessages routing_table.MessagesToEmit
		syncDuration time.Duration

		routerStartMessages chan<- *nats.Msg
	)

	BeforeEach(func() {
		bbs = fake_bbs.NewFakeRouteEmitterBBS()
		natsClient = fakeyagnats.NewApceraClientWrapper()
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
		dummyMessage := routing_table.RegistryMessageFor(dummyContainer, "foo.com", "bar.com")
		syncMessages = routing_table.MessagesToEmit{
			RegistrationMessages: []gibson.RegistryMessage{dummyMessage},
		}

		dummyContainer = routing_table.Container{Host: "2.2.2.2", Port: 22}
		dummyMessage = routing_table.RegistryMessageFor(dummyContainer, "baz.com")
		emitMessages = routing_table.MessagesToEmit{
			RegistrationMessages: []gibson.RegistryMessage{dummyMessage},
		}

		table.SyncReturns(syncMessages)
		table.MessagesToEmitReturns(emitMessages)

		//Set up some BBS data
		bbs.AllActualLRPs = []models.ActualLRP{
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
		}

		bbs.AllDesiredLRPs = []models.DesiredLRP{
			{
				ProcessGuid: "process-guid-1",
				Routes:      []string{"route-1", "route-2"},
			},
		}
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
			Ω(routes["process-guid-1"]).Should(Equal([]string{"route-1", "route-2"}))
			Ω(containers["process-guid-1"]).Should(Equal([]routing_table.Container{
				{Host: "1.2.3.4", Port: 1234},
			}))

			Ω(emitter.EmitCallCount()).Should(Equal(1))
			Ω(emitter.EmitArgsForCall(0)).Should(Equal(syncMessages))
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
				Ω(emitter.EmitArgsForCall(1)).Should(Equal(emitMessages))
				t1 := time.Now()

				Eventually(emitter.EmitCallCount, 2).Should(Equal(3))
				Ω(emitter.EmitArgsForCall(2)).Should(Equal(emitMessages))
				t2 := time.Now()

				Ω(t2.Sub(t1)).Should(BeNumerically("~", 1*time.Second, 200*time.Millisecond))
			})

			It("should only greet the router once", func() {
				Eventually(greetings).Should(Receive())
				Consistently(greetings, 1).ShouldNot(Receive())
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

				// go natsClient.Subscriptions(msg.Reply)[0].Callback(&nats.Msg{
				// 	Data: []byte(`{"minimumRegisterIntervalInSeconds":1}`),
				// })

				//shold now be emittingn regularly at the specified interval
				Eventually(emitter.EmitCallCount, 2).Should(Equal(2))
				Ω(emitter.EmitArgsForCall(1)).Should(Equal(emitMessages))
				t1 := time.Now()

				Eventually(emitter.EmitCallCount, 2).Should(Equal(3))
				Ω(emitter.EmitArgsForCall(2)).Should(Equal(emitMessages))
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
				Ω(emitter.EmitArgsForCall(1)).Should(Equal(emitMessages))
				t1 := time.Now()

				//subsequent emit should follow the interval
				Eventually(emitter.EmitCallCount, 3).Should(Equal(3))
				Ω(emitter.EmitArgsForCall(2)).Should(Equal(emitMessages))
				t2 := time.Now()
				Ω(t2.Sub(t1)).Should(BeNumerically("~", 2*time.Second, 200*time.Millisecond))
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

			Ω(emitter.EmitArgsForCall(1)).Should(Equal(syncMessages))
			Ω(emitter.EmitArgsForCall(2)).Should(Equal(syncMessages))
			Ω(t2.Sub(t1)).Should(BeNumerically("~", 500*time.Millisecond, 100*time.Millisecond))
		})

		Context("when fetching actuals fails", func() {
			BeforeEach(func() {
				lock := &sync.Mutex{}
				calls := 0
				bbs.WhenGettingRunningActualLRPs = func() ([]models.ActualLRP, error) {
					lock.Lock()
					defer lock.Unlock()
					if calls == 0 {
						calls++
						return nil, errors.New("bam")
					}
					return bbs.AllActualLRPs, nil
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
				bbs.WhenGettingAllDesiredLRPs = func() ([]models.DesiredLRP, error) {
					lock.Lock()
					defer lock.Unlock()
					if calls == 0 {
						calls++
						return nil, errors.New("bam")
					}
					return bbs.AllDesiredLRPs, nil
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
