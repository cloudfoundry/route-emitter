package watcher_test

import (
	"errors"
	"os"

	"github.com/cloudfoundry-incubator/runtime-schema/bbs/fake_bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/yagnats/fakeyagnats"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"

	. "github.com/cloudfoundry-incubator/route-emitter/watcher"
)

var _ = Describe("Watcher", func() {
	var (
		bbs        *fake_bbs.FakeLRPRouterBBS
		natsClient *fakeyagnats.FakeYagnats
		watcher    *Watcher
		process    ifrit.Process
	)

	BeforeEach(func() {
		bbs = fake_bbs.NewFakeLRPRouterBBS()
		natsClient = fakeyagnats.New()
		logger := gosteno.NewLogger("watcher")

		watcher = NewWatcher(bbs, natsClient, logger)
	})

	Describe("when the watcher is started up", func() {
		JustBeforeEach(func() {
			process = ifrit.Envoke(watcher)
		})

		AfterEach(func() {
			process.Signal(os.Interrupt)
			Eventually(process.Wait()).Should(Receive(BeNil()))
		})

		Context("when a desired LRP change comes in", func() {
			desiredChange := models.DesiredLRPChange{
				Before: &models.DesiredLRP{
					Routes: []string{"route-1"},
				},
				After: &models.DesiredLRP{
					Routes: []string{"route-2"},
				},
			}

			JustBeforeEach(func() {
				bbs.DesiredLRPChangeChan <- desiredChange
			})

			Context("and getting the actual LRPs fails", func() {
				var calledAgain chan bool

				BeforeEach(func() {
					calledAgain = make(chan bool)

					called := false

					bbs.WhenGettingActualLRPsByProcessGuid = func(guid string) ([]models.ActualLRP, error) {
						if called {
							calledAgain <- true
							return nil, nil
						} else {
							called = true
							return nil, errors.New("nope!")
						}
					}
				})

				It("keeps calm and carries on", func() {
					bbs.DesiredLRPChangeChan <- desiredChange

					Eventually(calledAgain).Should(Receive())
				})
			})
		})

		Context("when a desired LRP change comes in", func() {
			actualChange := models.ActualLRPChange{
				Before: nil,
				After: &models.ActualLRP{
					Host:  "1.2.3.4",
					State: models.ActualLRPStateRunning,
					Ports: []models.PortMapping{
						{ContainerPort: 8080, HostPort: 1234},
					},
				},
			}

			JustBeforeEach(func() {
				bbs.ActualLRPChangeChan <- actualChange
			})

			Context("and getting the actual LRPs fails", func() {
				var calledAgain chan bool

				BeforeEach(func() {
					calledAgain = make(chan bool)

					called := false

					bbs.WhenGettingDesiredLRPByProcessGuid = func(guid string) (models.DesiredLRP, error) {
						if called {
							calledAgain <- true
							return models.DesiredLRP{}, nil
						} else {
							called = true
							return models.DesiredLRP{}, errors.New("nope!")
						}
					}
				})

				It("keeps calm and carries on", func() {
					bbs.ActualLRPChangeChan <- actualChange

					Eventually(calledAgain).Should(Receive())
				})
			})
		})
	})
})
