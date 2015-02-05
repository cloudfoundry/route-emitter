package main_test

import (
	"encoding/json"
	"time"

	"github.com/apcera/nats"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	. "github.com/cloudfoundry-incubator/route-emitter/routing_table/matchers"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

const emitterInterruptTimeout = 5 * time.Second

var _ = Describe("Route Emitter", func() {
	listenForRoutes := func(subject string) <-chan routing_table.RegistryMessage {
		routes := make(chan routing_table.RegistryMessage)

		natsClient.Subscribe(subject, func(msg *nats.Msg) {
			defer GinkgoRecover()

			var message routing_table.RegistryMessage
			err := json.Unmarshal(msg.Data, &message)
			Ω(err).ShouldNot(HaveOccurred())

			routes <- message
		})

		return routes
	}

	var (
		registeredRoutes   <-chan routing_table.RegistryMessage
		unregisteredRoutes <-chan routing_table.RegistryMessage

		processGuid string
		domain      string
		desiredLRP  models.DesiredLRP

		lrpKey       models.ActualLRPKey
		containerKey models.ActualLRPContainerKey
		netInfo      models.ActualLRPNetInfo

		hostnames     []string
		containerPort uint16
		routes        map[string]*json.RawMessage
	)

	BeforeEach(func() {
		processGuid = "guid1"
		domain = "tests"

		hostnames = []string{"route-1", "route-2"}
		containerPort = 8080
		routes = newRoutes(hostnames, containerPort)

		desiredLRP = models.DesiredLRP{
			Domain:      domain,
			ProcessGuid: processGuid,
			Ports:       []uint16{containerPort},
			Routes:      routes,
			Instances:   5,
			Stack:       "some-stack",
			MemoryMB:    1024,
			DiskMB:      512,
			LogGuid:     "some-log-guid",
			Action: &models.RunAction{
				Path: "ls",
			},
		}

		lrpKey = models.NewActualLRPKey(processGuid, 0, domain)
		containerKey = models.NewActualLRPContainerKey("iguid1", "cell-id")
		netInfo = models.NewActualLRPNetInfo("1.2.3.4", []models.PortMapping{
			{ContainerPort: 8080, HostPort: 65100},
		})

		registeredRoutes = listenForRoutes("router.register")
		unregisteredRoutes = listenForRoutes("router.unregister")

		natsClient.Subscribe("router.greet", func(msg *nats.Msg) {
			defer GinkgoRecover()

			greeting := routing_table.RouterGreetingMessage{
				MinimumRegisterInterval: 2,
			}

			response, err := json.Marshal(greeting)
			Ω(err).ShouldNot(HaveOccurred())

			err = natsClient.Publish(msg.Reply, response)
			Ω(err).ShouldNot(HaveOccurred())
		})
	})

	Context("when the emitter is running", func() {
		var emitter ifrit.Process

		BeforeEach(func() {
			emitter = ginkgomon.Invoke(createEmitterRunner())
		})

		AfterEach(func() {
			ginkgomon.Interrupt(emitter, emitterInterruptTimeout)
		})

		Context("and an lrp with routes is desired", func() {
			BeforeEach(func() {
				err := bbs.DesireLRP(logger, desiredLRP)
				Ω(err).ShouldNot(HaveOccurred())
			})

			Context("and an instance starts", func() {
				BeforeEach(func() {
					err := bbs.StartActualLRP(lrpKey, containerKey, netInfo, logger)
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("emits its routes immediately", func() {
					Eventually(registeredRoutes).Should(Receive(MatchRegistryMessage(routing_table.RegistryMessage{
						URIs:              hostnames,
						Host:              netInfo.Address,
						Port:              uint16(netInfo.Ports[0].HostPort),
						App:               desiredLRP.LogGuid,
						PrivateInstanceId: containerKey.InstanceGuid,
					})))
				})
			})

			Context("and an instance is claimed", func() {
				BeforeEach(func() {
					err := bbs.ClaimActualLRP(lrpKey, containerKey, logger)
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("does not emit routes", func() {
					Consistently(registeredRoutes).ShouldNot(Receive())
				})
			})
		})

		Context("an actual lrp starts without a routed desried lrp", func() {
			BeforeEach(func() {
				desiredLRP.Routes = nil
				err := bbs.DesireLRP(logger, desiredLRP)
				Ω(err).ShouldNot(HaveOccurred())

				err = bbs.StartActualLRP(lrpKey, containerKey, netInfo, logger)
				Ω(err).ShouldNot(HaveOccurred())
			})

			Context("and a route is desired", func() {
				BeforeEach(func() {
					time.Sleep(100 * time.Millisecond)
					update := models.DesiredLRPUpdate{
						Routes: routes,
					}
					err := bbs.UpdateDesiredLRP(logger, desiredLRP.ProcessGuid, update)
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("emits its routes immediately", func() {
					Eventually(registeredRoutes).Should(Receive(MatchRegistryMessage(routing_table.RegistryMessage{
						URIs:              hostnames,
						Host:              netInfo.Address,
						Port:              uint16(netInfo.Ports[0].HostPort),
						App:               desiredLRP.LogGuid,
						PrivateInstanceId: containerKey.InstanceGuid,
					})))
				})

				It("repeats the route message at the interval given by the router", func() {
					var msg1 routing_table.RegistryMessage
					Eventually(registeredRoutes).Should(Receive(&msg1))
					t1 := time.Now()

					var msg2 routing_table.RegistryMessage
					Eventually(registeredRoutes, 5).Should(Receive(&msg2))
					t2 := time.Now()

					Ω(msg2).Should(MatchRegistryMessage(msg1))
					Ω(t2.Sub(t1)).Should(BeNumerically("~", 2*time.Second, 500*time.Millisecond))
				})

				Context("when etcd goes away", func() {
					var msg1 routing_table.RegistryMessage
					var msg2 routing_table.RegistryMessage

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
				Ω(secondEmitter.Wait()).ShouldNot(Receive(), "Runner should not have exploded!")
				ginkgomon.Interrupt(secondEmitter, emitterInterruptTimeout)
			})

			Describe("the second emitter", func() {
				It("does not become active", func() {
					Consistently(secondRunner.Buffer, 5*time.Second).ShouldNot(gbytes.Say("route-emitter.started"))
				})
			})

			Context("and the first emitter goes away", func() {
				BeforeEach(func() {
					ginkgomon.Interrupt(emitter, emitterInterruptTimeout)
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
		var emitter ifrit.Process

		BeforeEach(func() {
			err := bbs.DesireLRP(logger, desiredLRP)
			Ω(err).ShouldNot(HaveOccurred())

			err = bbs.StartActualLRP(lrpKey, containerKey, netInfo, logger)
			Ω(err).ShouldNot(HaveOccurred())
		})

		Context("and the emitter is started", func() {
			BeforeEach(func() {
				emitter = ginkgomon.Invoke(createEmitterRunner())
			})

			AfterEach(func() {
				ginkgomon.Interrupt(emitter, emitterInterruptTimeout)
			})

			It("immediately emits all routes", func() {
				Eventually(registeredRoutes).Should(Receive(MatchRegistryMessage(routing_table.RegistryMessage{
					URIs:              []string{"route-1", "route-2"},
					Host:              "1.2.3.4",
					Port:              65100,
					App:               "some-log-guid",
					PrivateInstanceId: "iguid1",
				})))
			})

			Context("and a route is added", func() {
				BeforeEach(func() {
					hostnames = []string{"route-1", "route-2", "route-3"}

					updateRequest := models.DesiredLRPUpdate{
						Routes:     newRoutes(hostnames, containerPort),
						Instances:  &desiredLRP.Instances,
						Annotation: &desiredLRP.Annotation,
					}
					err := bbs.UpdateDesiredLRP(logger, processGuid, updateRequest)
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("immediately emits router.register", func() {
					Eventually(registeredRoutes).Should(Receive(MatchRegistryMessage(routing_table.RegistryMessage{
						URIs:              hostnames,
						Host:              "1.2.3.4",
						Port:              65100,
						App:               "some-log-guid",
						PrivateInstanceId: "iguid1",
					})))
				})
			})

			Context("and a route is removed", func() {
				BeforeEach(func() {
					updateRequest := models.DesiredLRPUpdate{
						Routes:     newRoutes([]string{"route-2"}, containerPort),
						Instances:  &desiredLRP.Instances,
						Annotation: &desiredLRP.Annotation,
					}
					err := bbs.UpdateDesiredLRP(logger, processGuid, updateRequest)
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("immediately emits router.unregister", func() {
					Eventually(unregisteredRoutes).Should(Receive(MatchRegistryMessage(routing_table.RegistryMessage{
						URIs:              []string{"route-1"},
						Host:              "1.2.3.4",
						Port:              65100,
						App:               "some-log-guid",
						PrivateInstanceId: "iguid1",
					})))
				})
			})
		})
	})
})

func newRoutes(hosts []string, port uint16) map[string]*json.RawMessage {
	routingInfo := cfroutes.CFRoutes{
		{Hostnames: hosts, Port: port},
	}.RoutingInfo()

	routes := map[string]*json.RawMessage{}

	for key, message := range routingInfo {
		routes[key] = message
	}

	return routes
}
