package syncer_test

import (
	"os"
	"time"
	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter/fake_nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table/fake_routing_table"
	. "github.com/cloudfoundry-incubator/route-emitter/syncer"
	"github.com/cloudfoundry-incubator/runtime-schema/bbs/fake_bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry/gibson"
	"github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/yagnats"
	"github.com/cloudfoundry/yagnats/fakeyagnats"
	"github.com/tedsuo/ifrit"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Syncer", func() {
	var (
		bbs          *fake_bbs.FakeLRPRouterBBS
		natsClient   *fakeyagnats.FakeYagnats
		emitter      *fake_nats_emitter.FakeNATSEmitter
		table        *fake_routing_table.FakeRoutingTable
		syncer       *Syncer
		process      ifrit.Process
		syncMessages routing_table.MessagesToEmit
		emitMessages routing_table.MessagesToEmit
		syncDuration time.Duration

		routerStartMessages chan<- *yagnats.Message
	)

	BeforeEach(func() {
		bbs = fake_bbs.NewFakeLRPRouterBBS()
		natsClient = fakeyagnats.New()
		emitter = &fake_nats_emitter.FakeNATSEmitter{}
		table = &fake_routing_table.FakeRoutingTable{}
		syncDuration = 10 * time.Second

		startMessages := make(chan *yagnats.Message)
		routerStartMessages = startMessages

		natsClient.WhenSubscribing("router.start", func(callback yagnats.Callback) error {
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
	})

	JustBeforeEach(func() {
		logger := gosteno.NewLogger("syncer")
		syncer = NewSyncer(bbs, table, emitter, syncDuration, natsClient, logger)
	})

	Describe("when the syncer is started up", func() {
		BeforeEach(func() {
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
			process = ifrit.Envoke(syncer)
		})

		AfterEach(func() {
			process.Signal(os.Interrupt)
			Eventually(process.Wait()).Should(Receive(BeNil()))
		})

		Context("when router.start is received", func() {
			JustBeforeEach(func() {
				routerStartMessages <- &yagnats.Message{
					Payload: []byte(`{"minimumRegisterIntervalInSeconds":1}`),
				}
			})

			It("immediately synces", func() {
				Eventually(table.SyncCallCount).Should(Equal(1))
				routes, containers := table.SyncArgsForCall(0)
				Ω(routes["process-guid-1"]).Should(Equal([]string{"route-1", "route-2"}))
				Ω(containers["process-guid-1"]).Should(Equal([]routing_table.Container{
					{Host: "1.2.3.4", Port: 1234},
				}))

				Eventually(emitter.EmitCallCount).Should(Equal(1))
				Ω(emitter.EmitArgsForCall(0)).Should(Equal(syncMessages))
			})

			It("emits the routes again after the specified interval", func() {
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				t1 := time.Now()

				Eventually(emitter.EmitCallCount, 2).Should(Equal(2))
				t2 := time.Now()

				Ω(emitter.EmitArgsForCall(1)).Should(Equal(emitMessages))

				Ω(t2.Sub(t1)).Should(BeNumerically("~", 1*time.Second, 200*time.Millisecond))
			})
		})

		Context("after greeting with the router", func() {
			BeforeEach(func() {
				natsClient.WhenPublishing("router.greet", func(msg *yagnats.Message) error {
					replySubs := natsClient.Subscriptions(msg.ReplyTo)
					Ω(replySubs).Should(HaveLen(1))

					go replySubs[0].Callback(&yagnats.Message{
						Payload: []byte(`{"minimumRegisterIntervalInSeconds":1}`),
					})

					return nil
				})
			})

			It("immediately registers routes for all LRPs", func() {
				Eventually(table.SyncCallCount).Should(Equal(1))
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				Ω(emitter.EmitArgsForCall(0)).Should(Equal(syncMessages))
			})

			Context("when router.start is received", func() {
				var heartbeatPayload string

				BeforeEach(func() {
					heartbeatPayload = `{"minimumRegisterIntervalInSeconds":2}`
				})

				JustBeforeEach(func() {
					routerStartMessages <- &yagnats.Message{
						Payload: []byte(heartbeatPayload),
					}
				})

				It("immediately registers all routes for all LRPs", func() {
					Eventually(table.SyncCallCount).Should(Equal(1))
					Eventually(emitter.EmitCallCount).Should(Equal(2))
					Ω(emitter.EmitArgsForCall(0)).Should(Equal(syncMessages))
					Ω(emitter.EmitArgsForCall(1)).Should(Equal(emitMessages))
				})

				It("emits the routes again after the specified interval", func() {
					Eventually(emitter.EmitCallCount).Should(Equal(2))
					t1 := time.Now()

					Eventually(emitter.EmitCallCount, 3).Should(Equal(3))
					t2 := time.Now()

					Ω(table.SyncCallCount()).Should(Equal(1))
					Ω(emitter.EmitArgsForCall(0)).Should(Equal(syncMessages))
					Ω(emitter.EmitArgsForCall(1)).Should(Equal(emitMessages))
					Ω(emitter.EmitArgsForCall(2)).Should(Equal(emitMessages))

					Ω(t2.Sub(t1)).Should(BeNumerically("~", 2*time.Second, 200*time.Millisecond))
				})

				Context("and the payload is invalid", func() {
					BeforeEach(func() {
						heartbeatPayload = "ß"
					})

					It("does not update the interval", func() {
						Eventually(emitter.EmitCallCount).Should(Equal(1))
						t1 := time.Now()

						Eventually(emitter.EmitCallCount, 2).Should(Equal(2))
						t2 := time.Now()

						Ω(emitter.EmitArgsForCall(0)).Should(Equal(syncMessages))
						Ω(emitter.EmitArgsForCall(1)).Should(Equal(emitMessages))

						Ω(t2.Sub(t1)).Should(BeNumerically("~", 1*time.Second, 200*time.Millisecond))
					})
				})
			})
		})

		Context("after the sync duration elapses", func() {
			BeforeEach(func() {
				syncDuration = time.Second
			})

			JustBeforeEach(func() {
				routerStartMessages <- &yagnats.Message{
					Payload: []byte(`{"minimumRegisterIntervalInSeconds":10}`),
				}
			})

			It("should resync via the BBS and emit routes", func() {
				Eventually(table.SyncCallCount).Should(Equal(1))
				Eventually(emitter.EmitCallCount).Should(Equal(1))
				t1 := time.Now()

				Eventually(table.SyncCallCount, 2).Should(Equal(2))
				Eventually(emitter.EmitCallCount, 2).Should(Equal(2))
				t2 := time.Now()

				Ω(emitter.EmitArgsForCall(0)).Should(Equal(syncMessages))
				Ω(emitter.EmitArgsForCall(1)).Should(Equal(syncMessages))
				Ω(t2.Sub(t1)).Should(BeNumerically("~", time.Second, 200*time.Millisecond))
			})
		})
	})
})
