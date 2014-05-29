package integration_test

import (
	"encoding/json"
	"time"

	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry/gibson"
	"github.com/cloudfoundry/yagnats"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

var _ = Describe("Integration", func() {
	listenForRoutes := func(subject string) <-chan gibson.RegistryMessage {
		routes := make(chan gibson.RegistryMessage)

		natsRunner.MessageBus.Subscribe(subject, func(msg *yagnats.Message) {
			defer GinkgoRecover()

			var message gibson.RegistryMessage
			err := json.Unmarshal(msg.Payload, &message)
			Ω(err).ShouldNot(HaveOccurred())

			routes <- message
		})

		return routes
	}

	var (
		registeredRoutes   <-chan gibson.RegistryMessage
		unregisteredRoutes <-chan gibson.RegistryMessage
	)

	BeforeEach(func() {
		registeredRoutes = listenForRoutes("router.register")
		unregisteredRoutes = listenForRoutes("router.unregister")

		natsRunner.MessageBus.Subscribe("router.greet", func(msg *yagnats.Message) {
			defer GinkgoRecover()

			greeting := gibson.RouterGreetingMessage{
				MinimumRegisterInterval: 2,
			}

			response, err := json.Marshal(greeting)
			Ω(err).ShouldNot(HaveOccurred())

			err = natsRunner.MessageBus.Publish(msg.ReplyTo, response)
			Ω(err).ShouldNot(HaveOccurred())
		})
	})

	AfterEach(func() {
		Ω(runner.Session).ShouldNot(gexec.Exit(), "Runner should not have exploded!")
	})

	Context("when the emitter is running", func() {
		BeforeEach(func() {
			runner.Start()
		})

		Context("and routes are desired", func() {
			BeforeEach(func() {
				err := bbs.DesireLRP(models.DesiredLRP{
					ProcessGuid: "guid1",
					Routes:      []string{"route-1", "route-2"},
					Instances:   5,
					Stack:       "some-stack",
					MemoryMB:    1024,
					DiskMB:      512,
				})
				Ω(err).ShouldNot(HaveOccurred())
			})

			Context("and an endpoint comes up", func() {
				BeforeEach(func() {
					err := bbs.ReportActualLRPAsRunning(models.ActualLRP{
						ProcessGuid:  "guid1",
						Index:        0,
						InstanceGuid: "iguid1",

						Host: "1.2.3.4",
						Ports: []models.PortMapping{
							{ContainerPort: 8080, HostPort: 65100},
						},
					})
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("emits its routes immediately", func() {
					Eventually(registeredRoutes).Should(Receive(Equal(gibson.RegistryMessage{
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
					ProcessGuid: "guid1",
					Routes:      []string{"route-1", "route-2"},
					Instances:   5,
					Stack:       "some-stack",
					MemoryMB:    1024,
					DiskMB:      512,
				})
				Ω(err).ShouldNot(HaveOccurred())

				err = bbs.ReportActualLRPAsStarting(models.ActualLRP{
					ProcessGuid:  "guid1",
					Index:        0,
					InstanceGuid: "iguid1",
				})
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

					Host: "1.2.3.4",
					Ports: []models.PortMapping{
						{ContainerPort: 8080, HostPort: 65100},
					},
				})
				Ω(err).ShouldNot(HaveOccurred())
			})

			Context("and a route is desired", func() {
				BeforeEach(func() {
					time.Sleep(100 * time.Millisecond)
					err := bbs.DesireLRP(models.DesiredLRP{
						ProcessGuid: "guid1",
						Routes:      []string{"route-1", "route-2"},
						Instances:   5,
						Stack:       "some-stack",
						MemoryMB:    1024,
						DiskMB:      512,
					})
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("emits its routes immediately", func() {
					Eventually(registeredRoutes).Should(Receive(Equal(gibson.RegistryMessage{
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

					Ω(msg2).Should(Equal(msg1))
					Ω(t2.Sub(t1)).Should(BeNumerically("~", 2*time.Second, 500*time.Millisecond))
				})
			})
		})

		Context("and etcd goes away", func() {
			BeforeEach(func() {
				etcdRunner.Stop()
			})

			It("does not explode", func() {
				Consistently(runner.Session, 5).ShouldNot(gexec.Exit())
			})
		})
	})

	Context("when the bbs has routes to emit in /desired and /actual", func() {
		BeforeEach(func() {
			err := bbs.DesireLRP(models.DesiredLRP{
				ProcessGuid: "guid1",
				Routes:      []string{"route-1", "route-2"},
				Instances:   5,
				Stack:       "some-stack",
				MemoryMB:    1024,
				DiskMB:      512,
			})
			Ω(err).ShouldNot(HaveOccurred())

			err = bbs.ReportActualLRPAsRunning(models.ActualLRP{
				ProcessGuid:  "guid1",
				Index:        0,
				InstanceGuid: "iguid1",

				Host: "1.2.3.4",
				Ports: []models.PortMapping{
					{ContainerPort: 8080, HostPort: 65100},
				},
			})
			Ω(err).ShouldNot(HaveOccurred())
		})

		Context("and the emitter is started", func() {
			BeforeEach(func() {
				runner.Start()
			})

			It("immediately emits all routes", func() {
				Eventually(registeredRoutes).Should(Receive(Equal(gibson.RegistryMessage{
					URIs: []string{"route-1", "route-2"},
					Host: "1.2.3.4",
					Port: 65100,
				})))
			})

			Context("and a route is added", func() {
				BeforeEach(func() {
					err := bbs.DesireLRP(models.DesiredLRP{
						ProcessGuid: "guid1",
						Routes:      []string{"route-1", "route-2", "route-3"},
						Instances:   5,
						Stack:       "some-stack",
						MemoryMB:    1024,
						DiskMB:      512,
					})
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("immediately emits router.register", func() {
					Eventually(registeredRoutes).Should(Receive(Equal(gibson.RegistryMessage{
						URIs: []string{"route-3"},
						Host: "1.2.3.4",
						Port: 65100,
					})))
				})
			})

			Context("and a route is removed", func() {
				BeforeEach(func() {
					err := bbs.DesireLRP(models.DesiredLRP{
						ProcessGuid: "guid1",
						Routes:      []string{"route-2"},
						Instances:   5,
						Stack:       "some-stack",
						MemoryMB:    1024,
						DiskMB:      512,
					})
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("immediately emits router.unregister", func() {
					Eventually(unregisteredRoutes).Should(Receive(Equal(gibson.RegistryMessage{
						URIs: []string{"route-1"},
						Host: "1.2.3.4",
						Port: 65100,
					})))
				})
			})
		})
	})
})
