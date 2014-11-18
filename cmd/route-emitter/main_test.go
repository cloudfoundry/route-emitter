package main_test

import (
	"encoding/json"
	"os"
	"time"

	"github.com/apcera/nats"
	. "github.com/cloudfoundry-incubator/route-emitter/routing_table/matchers"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry/gibson"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

var _ = Describe("Route Emitter", func() {
	listenForRoutes := func(subject string) <-chan gibson.RegistryMessage {
		routes := make(chan gibson.RegistryMessage)

		natsClient.Subscribe(subject, func(msg *nats.Msg) {
			defer GinkgoRecover()

			var message gibson.RegistryMessage
			err := json.Unmarshal(msg.Data, &message)
			Ω(err).ShouldNot(HaveOccurred())

			routes <- message
		})

		return routes
	}

	var (
		runner  *ginkgomon.Runner
		emitter ifrit.Process

		registeredRoutes   <-chan gibson.RegistryMessage
		unregisteredRoutes <-chan gibson.RegistryMessage
	)

	BeforeEach(func() {
		emitter = nil

		registeredRoutes = listenForRoutes("router.register")
		unregisteredRoutes = listenForRoutes("router.unregister")

		natsClient.Subscribe("router.greet", func(msg *nats.Msg) {
			defer GinkgoRecover()

			greeting := gibson.RouterGreetingMessage{
				MinimumRegisterInterval: 2,
			}

			response, err := json.Marshal(greeting)
			Ω(err).ShouldNot(HaveOccurred())

			err = natsClient.Publish(msg.Reply, response)
			Ω(err).ShouldNot(HaveOccurred())
		})

		runner = createEmitterRunner()
	})

	AfterEach(func() {
		if emitter != nil {
			Ω(emitter.Wait()).ShouldNot(Receive(), "Runner should not have exploded!")
		}

		emitter.Signal(os.Interrupt)

		Eventually(emitter.Wait(), 5*time.Second).Should(Receive())
	})

	Context("when the emitter is running", func() {
		BeforeEach(func() {
			emitter = ginkgomon.Invoke(runner)
		})

		Context("and routes are desired", func() {
			BeforeEach(func() {
				err := bbs.DesireLRP(models.DesiredLRP{
					Domain:      "tests",
					ProcessGuid: "guid1",
					Routes:      []string{"route-1", "route-2"},
					Instances:   5,
					Stack:       "some-stack",
					MemoryMB:    1024,
					DiskMB:      512,
					Action: &models.RunAction{
						Path: "ls",
					},
				})
				Ω(err).ShouldNot(HaveOccurred())
			})

			Context("and an endpoint comes up", func() {
				BeforeEach(func() {
					err := bbs.ReportActualLRPAsRunning(models.ActualLRP{
						ProcessGuid:  "guid1",
						Index:        0,
						InstanceGuid: "iguid1",
						Domain:       "tests",

						Host: "1.2.3.4",
						Ports: []models.PortMapping{
							{ContainerPort: 8080, HostPort: 65100},
						},
					}, "cell-id")
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("emits its routes immediately", func() {
					Eventually(registeredRoutes).Should(Receive(MatchRegistryMessage(gibson.RegistryMessage{
						URIs: []string{"route-1", "route-2"},
						Host: "1.2.3.4",
						Port: 65100,
					})))
				})
			})
		})

		Context("and an instance starts running", func() {
			BeforeEach(func() {
				err := bbs.DesireLRP(models.DesiredLRP{
					Domain:      "tests",
					ProcessGuid: "guid1",
					Routes:      []string{"route-1", "route-2"},
					Instances:   5,
					Stack:       "some-stack",
					MemoryMB:    1024,
					DiskMB:      512,
					Action: &models.RunAction{
						Path: "ls",
					},
				})
				Ω(err).ShouldNot(HaveOccurred())

				_, err = bbs.ReportActualLRPAsStarting("guid1", "iguid1", "cell-id", "some-domain", 0)
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("does not emit routes", func() {
				Consistently(registeredRoutes).ShouldNot(Receive())
			})
		})

		Context("and an endpoint comes up", func() {
			BeforeEach(func() {
				err := bbs.ReportActualLRPAsRunning(models.ActualLRP{
					ProcessGuid:  "guid1",
					Index:        0,
					InstanceGuid: "iguid1",
					Domain:       "tests",

					Host: "1.2.3.4",
					Ports: []models.PortMapping{
						{ContainerPort: 8080, HostPort: 65100},
					},
				}, "cell-id")
				Ω(err).ShouldNot(HaveOccurred())
			})

			Context("and a route is desired", func() {
				BeforeEach(func() {
					time.Sleep(100 * time.Millisecond)
					err := bbs.DesireLRP(models.DesiredLRP{
						Domain:      "tests",
						ProcessGuid: "guid1",
						Routes:      []string{"route-1", "route-2"},
						Instances:   5,
						Stack:       "some-stack",
						MemoryMB:    1024,
						DiskMB:      512,
						Action: &models.RunAction{
							Path: "ls",
						},
					})
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("emits its routes immediately", func() {
					Eventually(registeredRoutes).Should(Receive(MatchRegistryMessage(gibson.RegistryMessage{
						URIs: []string{"route-1", "route-2"},
						Host: "1.2.3.4",
						Port: 65100,
					})))
				})

				It("repeats the route message at the interval given by the router", func() {
					var msg1 gibson.RegistryMessage
					Eventually(registeredRoutes).Should(Receive(&msg1))
					t1 := time.Now()

					var msg2 gibson.RegistryMessage
					Eventually(registeredRoutes, 5).Should(Receive(&msg2))
					t2 := time.Now()

					Ω(msg2).Should(MatchRegistryMessage(msg1))
					Ω(t2.Sub(t1)).Should(BeNumerically("~", 2*time.Second, 500*time.Millisecond))
				})

				Context("when etcd goes away", func() {
					var msg1 gibson.RegistryMessage
					var msg2 gibson.RegistryMessage

					BeforeEach(func() {
						// ensure it's seen the route at least once
						Eventually(registeredRoutes).Should(Receive(&msg1))

						etcdRunner.Stop()
					})

					It("continues to broadcast routes", func() {
						Eventually(registeredRoutes, 5).Should(Receive(&msg2))
						Ω(msg2).Should(MatchRegistryMessage(msg1))
					})
				})
			})
		})

		Context("and another emitter starts", func() {
			var (
				secondRunner  *ginkgomon.Runner
				secondEmitter ifrit.Process
			)

			BeforeEach(func() {
				secondRunner = createEmitterRunner()
				secondRunner.StartCheck = ""

				secondEmitter = ginkgomon.Invoke(secondRunner)
			})

			AfterEach(func() {
				if secondEmitter != nil {
					Ω(secondEmitter.Wait()).ShouldNot(Receive(), "Runner should not have exploded!")
				}

				secondEmitter.Signal(os.Interrupt)

				Eventually(secondEmitter.Wait(), 5*time.Second).Should(Receive())
			})

			Describe("the second emitter", func() {
				It("does not become active", func() {
					Consistently(secondRunner.Buffer, 5*time.Second).ShouldNot(gbytes.Say("route-emitter.started"))
				})
			})

			Context("and the first emitter goes away", func() {
				BeforeEach(func() {
					emitter.Signal(os.Interrupt)
					Eventually(emitter.Wait(), 5*time.Second).Should(Receive())
				})

				Describe("the second emitter", func() {
					It("becomes active", func() {
						Eventually(secondRunner.Buffer, 5*time.Second).Should(gbytes.Say("route-emitter.started"))
					})
				})
			})
		})

		Context("and etcd goes away", func() {
			BeforeEach(func() {
				etcdRunner.Stop()
			})

			It("does not explode", func() {
				Consistently(emitter.Wait(), 5).ShouldNot(Receive())
			})
		})
	})

	Context("when the bbs has routes to emit in /desired and /actual", func() {
		var lrp models.DesiredLRP

		BeforeEach(func() {
			lrp = models.DesiredLRP{
				Domain:      "tests",
				ProcessGuid: "guid1",
				Routes:      []string{"route-1", "route-2"},
				Instances:   5,
				Stack:       "some-stack",
				MemoryMB:    1024,
				DiskMB:      512,
				Action: &models.RunAction{
					Path: "ls",
				},
			}
			err := bbs.DesireLRP(lrp)
			Ω(err).ShouldNot(HaveOccurred())

			err = bbs.ReportActualLRPAsRunning(models.ActualLRP{
				ProcessGuid:  "guid1",
				Index:        0,
				InstanceGuid: "iguid1",
				Domain:       "tests",

				Host: "1.2.3.4",
				Ports: []models.PortMapping{
					{ContainerPort: 8080, HostPort: 65100},
				},
			}, "cell-id")
			Ω(err).ShouldNot(HaveOccurred())
		})

		Context("and the emitter is started", func() {
			BeforeEach(func() {
				emitter = ginkgomon.Invoke(runner)
			})

			It("immediately emits all routes", func() {
				Eventually(registeredRoutes).Should(Receive(MatchRegistryMessage(gibson.RegistryMessage{
					URIs: []string{"route-1", "route-2"},
					Host: "1.2.3.4",
					Port: 65100,
				})))
			})

			Context("and a route is added", func() {
				BeforeEach(func() {
					err := bbs.ChangeDesiredLRP(models.DesiredLRPChange{
						Before: &lrp,
						After: &models.DesiredLRP{
							Domain:      "tests",
							ProcessGuid: "guid1",
							Routes:      []string{"route-1", "route-2", "route-3"},
							Instances:   5,
							Stack:       "some-stack",
							MemoryMB:    1024,
							DiskMB:      512,
							Action: &models.RunAction{
								Path: "ls",
							},
						},
					})
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("immediately emits router.register", func() {
					Eventually(registeredRoutes).Should(Receive(MatchRegistryMessage(gibson.RegistryMessage{
						URIs: []string{"route-1", "route-2", "route-3"},
						Host: "1.2.3.4",
						Port: 65100,
					})))
				})
			})

			Context("and a route is removed", func() {
				BeforeEach(func() {
					err := bbs.ChangeDesiredLRP(models.DesiredLRPChange{
						Before: &lrp,
						After: &models.DesiredLRP{
							Domain:      "tests",
							ProcessGuid: "guid1",
							Routes:      []string{"route-2"},
							Instances:   5,
							Stack:       "some-stack",
							MemoryMB:    1024,
							DiskMB:      512,
							Action: &models.RunAction{
								Path: "ls",
							},
						},
					})
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("immediately emits router.unregister", func() {
					Eventually(unregisteredRoutes).Should(Receive(MatchRegistryMessage(gibson.RegistryMessage{
						URIs: []string{"route-1"},
						Host: "1.2.3.4",
						Port: 65100,
					})))
				})
			})
		})
	})
})
