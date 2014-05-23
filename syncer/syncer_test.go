package syncer_test

import (
	"errors"
	"os"
	"time"
	. "github.com/cloudfoundry-incubator/route-emitter/syncer"
	"github.com/cloudfoundry-incubator/runtime-schema/bbs/fake_bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/yagnats"
	"github.com/cloudfoundry/yagnats/fakeyagnats"
	"github.com/tedsuo/ifrit"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Syncer", func() {
	var (
		bbs        *fake_bbs.FakeLRPRouterBBS
		natsClient *fakeyagnats.FakeYagnats
		syncer     *Syncer
		process    ifrit.Process

		routerStartMessages chan<- *yagnats.Message
	)

	BeforeEach(func() {
		bbs = fake_bbs.NewFakeLRPRouterBBS()
		natsClient = fakeyagnats.New()
		logger := gosteno.NewLogger("syncer")
		syncer = NewSyncer(bbs, natsClient, logger)

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
	})

	Describe("when the syncer is started up", func() {
		BeforeEach(func() {
			bbs.AllActualLRPs = []models.LRP{
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

			It("immediately registers all routes for all LRPs", func() {
				Eventually(func() interface{} {
					return natsClient.PublishedMessages("router.register")
				}).Should(HaveLen(1))

				Ω(natsClient.PublishedMessages("router.register")[0].Payload).Should(MatchJSON(`
					{
						"uris":["route-1","route-2"],
						"host":"1.2.3.4",
						"port":1234
					}
				`))
			})

			It("emits the routes again after the specified interval", func() {
				Eventually(func() interface{} {
					return natsClient.PublishedMessages("router.register")
				}).Should(HaveLen(1))
				t1 := time.Now()

				Eventually(func() interface{} {
					return natsClient.PublishedMessages("router.register")
				}, 2).Should(HaveLen(2))
				t2 := time.Now()

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
				Eventually(func() interface{} {
					return natsClient.PublishedMessages("router.register")
				}).Should(HaveLen(1))

				Ω(natsClient.PublishedMessages("router.register")[0].Payload).Should(MatchJSON(`
					{
						"uris":["route-1","route-2"],
						"host":"1.2.3.4",
						"port":1234
					}
				`))
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
					Eventually(func() interface{} {
						return natsClient.PublishedMessages("router.register")
					}).Should(HaveLen(2))

					Ω(natsClient.PublishedMessages("router.register")[0].Payload).Should(MatchJSON(`
						{
							"uris": ["route-1","route-2"],
							"host": "1.2.3.4",
							"port": 1234
						}
					`))
				})

				It("emits the routes again after the specified interval", func() {
					Eventually(func() interface{} {
						return natsClient.PublishedMessages("router.register")
					}).Should(HaveLen(2))
					t1 := time.Now()

					Eventually(func() interface{} {
						return natsClient.PublishedMessages("router.register")
					}, 3).Should(HaveLen(3))
					t2 := time.Now()

					Ω(t2.Sub(t1)).Should(BeNumerically("~", 2*time.Second, 200*time.Millisecond))
				})

				Context("and the payload is invalid", func() {
					BeforeEach(func() {
						heartbeatPayload = "ß"
					})

					It("does not update the interval", func() {
						Eventually(func() interface{} {
							return natsClient.PublishedMessages("router.register")
						}).Should(HaveLen(1))
						t1 := time.Now()

						Eventually(func() interface{} {
							return natsClient.PublishedMessages("router.register")
						}, 2).Should(HaveLen(2))
						t2 := time.Now()

						Ω(t2.Sub(t1)).Should(BeNumerically("~", 1*time.Second, 200*time.Millisecond))
					})
				})
			})

			Context("when getting all actual LRPs fails", func() {
				BeforeEach(func() {
					firstTime := true
					bbs.WhenGettingRunningActualLRPs = func() ([]models.LRP, error) {
						if firstTime {
							firstTime = false
							return []models.LRP{}, errors.New("NO")
						} else {
							return bbs.AllActualLRPs, nil
						}
					}
				})

				It("keeps on truckin'", func() {
					Eventually(func() interface{} {
						return natsClient.PublishedMessages("router.register")
					}, 2).Should(HaveLen(1))

					Ω(natsClient.PublishedMessages("router.register")[0].Payload).Should(MatchJSON(`
						{
							"uris":["route-1","route-2"],
							"host":"1.2.3.4",
							"port":1234
						}
					`))
				})
			})

			Context("when getting all desired LRPs fails", func() {
				BeforeEach(func() {
					firstTime := true
					bbs.WhenGettingAllDesiredLRPs = func() ([]models.DesiredLRP, error) {
						if firstTime {
							firstTime = false
							return []models.DesiredLRP{}, errors.New("NO")
						} else {
							return bbs.AllDesiredLRPs, nil
						}
					}
				})

				It("keeps on truckin'", func() {
					Eventually(func() interface{} {
						return natsClient.PublishedMessages("router.register")
					}, 2).Should(HaveLen(1))

					Ω(natsClient.PublishedMessages("router.register")[0].Payload).Should(MatchJSON(`
						{
							"uris":["route-1","route-2"],
							"host":"1.2.3.4",
							"port":1234
						}
					`))
				})
			})
		})
	})
})
