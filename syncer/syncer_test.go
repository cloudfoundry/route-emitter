package syncer_test

import (
	"errors"
	"os"
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
	)

	BeforeEach(func() {
		bbs = fake_bbs.NewFakeLRPRouterBBS()
		natsClient = fakeyagnats.New()
		logger := gosteno.NewLogger("syncer")
		syncer = NewSyncer(bbs, natsClient, logger)
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
		})

		Context("after greeting with the router", func() {
			BeforeEach(func() {
				natsClient.WhenPublishing("router.greet", func(msg *yagnats.Message) error {
					replySubs := natsClient.Subscriptions(msg.ReplyTo)
					Ω(replySubs).Should(HaveLen(1))

					replySubs[0].Callback(&yagnats.Message{
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

			Context("when getting all actual LRPs fails", func() {
				BeforeEach(func() {
					firstTime := true
					bbs.WhenGettingAllActualLongRunningProcesses = func() ([]models.LRP, error) {
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
					}).Should(HaveLen(1))

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
					bbs.WhenGettingAllDesiredLongRunningProcesses = func() ([]models.DesiredLRP, error) {
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
					}).Should(HaveLen(1))

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
