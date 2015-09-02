package main_test

import (
	"encoding/json"
	"time"

	"github.com/apcera/nats"
	"github.com/cloudfoundry-incubator/bbs/models"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	. "github.com/cloudfoundry-incubator/route-emitter/routing_table/matchers"
	oldmodels "github.com/cloudfoundry-incubator/runtime-schema/models"
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
			Expect(err).NotTo(HaveOccurred())

			routes <- message
		})

		return routes
	}

	var (
		registeredRoutes   <-chan routing_table.RegistryMessage
		unregisteredRoutes <-chan routing_table.RegistryMessage

		processGuid string
		domain      string
		desiredLRP  *models.DesiredLRP
		index       int32

		lrpKey      models.ActualLRPKey
		instanceKey models.ActualLRPInstanceKey
		netInfo     models.ActualLRPNetInfo

		legacyLRPKey      oldmodels.ActualLRPKey
		legacyInstanceKey oldmodels.ActualLRPInstanceKey

		hostnames     []string
		containerPort uint32
		routes        *models.Routes
	)

	BeforeEach(func() {
		processGuid = "guid1"
		domain = "tests"

		hostnames = []string{"route-1", "route-2"}
		containerPort = 8080
		routes = newRoutes(hostnames, containerPort)

		desiredLRP = &models.DesiredLRP{
			Domain:      domain,
			ProcessGuid: processGuid,
			Ports:       []uint32{containerPort},
			Routes:      routes,
			Instances:   5,
			RootFs:      "some:rootfs",
			MemoryMb:    1024,
			DiskMb:      512,
			LogGuid:     "some-log-guid",
			Action: models.WrapAction(&models.RunAction{
				User: "me",
				Path: "ls",
			}),
		}

		index = 0
		lrpKey = models.NewActualLRPKey(processGuid, index, domain)
		legacyLRPKey = oldmodels.NewActualLRPKey(processGuid, int(index), domain)

		legacyInstanceKey = oldmodels.NewActualLRPInstanceKey("iguid1", "cell-id")
		instanceKey = models.NewActualLRPInstanceKey("iguid1", "cell-id")

		netInfo = models.NewActualLRPNetInfo("1.2.3.4", models.NewPortMapping(65100, 8080))
		registeredRoutes = listenForRoutes("router.register")
		unregisteredRoutes = listenForRoutes("router.unregister")

		natsClient.Subscribe("router.greet", func(msg *nats.Msg) {
			defer GinkgoRecover()

			greeting := routing_table.RouterGreetingMessage{
				MinimumRegisterInterval: 2,
				PruneThresholdInSeconds: 6,
			}

			response, err := json.Marshal(greeting)
			Expect(err).NotTo(HaveOccurred())

			err = natsClient.Publish(msg.Reply, response)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when the emitter is running", func() {
		var emitter ifrit.Process

		BeforeEach(func() {
			runner := createEmitterRunner("emitter1")
			runner.StartCheck = "emitter1.started"
			emitter = ginkgomon.Invoke(runner)
		})

		AfterEach(func() {
			ginkgomon.Interrupt(emitter, emitterInterruptTimeout)
		})

		Context("and an lrp with routes is desired", func() {
			BeforeEach(func() {
				err := bbsClient.DesireLRP(desiredLRP)
				Expect(err).NotTo(HaveOccurred())
			})

			Context("and an instance starts", func() {
				BeforeEach(func() {
					err := bbsClient.StartActualLRP(&lrpKey, &instanceKey, &netInfo)
					Expect(err).NotTo(HaveOccurred())
				})

				It("emits its routes immediately", func() {
					Eventually(registeredRoutes).Should(Receive(MatchRegistryMessage(routing_table.RegistryMessage{
						URIs:              hostnames,
						Host:              netInfo.Address,
						Port:              netInfo.Ports[0].HostPort,
						App:               desiredLRP.LogGuid,
						PrivateInstanceId: legacyInstanceKey.InstanceGuid,
					})))
				})
			})

			Context("and an instance is claimed", func() {
				BeforeEach(func() {
					err := bbsClient.ClaimActualLRP(processGuid, int(index), &instanceKey)
					Expect(err).NotTo(HaveOccurred())
				})

				It("does not emit routes", func() {
					Consistently(registeredRoutes).ShouldNot(Receive())
				})
			})
		})

		Context("an actual lrp starts without a routed desired lrp", func() {
			BeforeEach(func() {
				desiredLRP.Routes = nil
				err := bbsClient.DesireLRP(desiredLRP)
				Expect(err).NotTo(HaveOccurred())

				err = bbsClient.StartActualLRP(&lrpKey, &instanceKey, &netInfo)
				Expect(err).NotTo(HaveOccurred())
			})

			Context("and a route is desired", func() {
				BeforeEach(func() {
					update := &models.DesiredLRPUpdate{
						Routes: routes,
					}
					err := bbsClient.UpdateDesiredLRP(desiredLRP.ProcessGuid, update)
					Expect(err).NotTo(HaveOccurred())
				})

				It("emits its routes immediately", func() {
					Eventually(registeredRoutes).Should(Receive(MatchRegistryMessage(routing_table.RegistryMessage{
						URIs:              hostnames,
						Host:              netInfo.Address,
						Port:              netInfo.Ports[0].HostPort,
						App:               desiredLRP.LogGuid,
						PrivateInstanceId: legacyInstanceKey.InstanceGuid,
					})))
				})

				It("repeats the route message at the interval given by the router", func() {
					var msg1 routing_table.RegistryMessage
					Eventually(registeredRoutes).Should(Receive(&msg1))
					t1 := time.Now()

					var msg2 routing_table.RegistryMessage
					Eventually(registeredRoutes, 5).Should(Receive(&msg2))
					t2 := time.Now()

					Expect(msg2).To(MatchRegistryMessage(msg1))
					Expect(t2.Sub(t1)).To(BeNumerically("~", 2*syncInterval, 500*time.Millisecond))
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
						Expect(msg2).To(MatchRegistryMessage(msg1))
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
				secondRunner = createEmitterRunner("emitter2")
				secondRunner.StartCheck = "lock.acquiring-lock"

				secondEmitter = ginkgomon.Invoke(secondRunner)
			})

			AfterEach(func() {
				Expect(secondEmitter.Wait()).NotTo(Receive(), "Runner should not have exploded!")
				ginkgomon.Interrupt(secondEmitter, emitterInterruptTimeout)
			})

			Describe("the second emitter", func() {
				It("does not become active", func() {
					Consistently(secondRunner.Buffer, 5*time.Second).ShouldNot(gbytes.Say("emitter2.started"))
				})
			})

			Context("and the first emitter goes away", func() {
				BeforeEach(func() {
					ginkgomon.Interrupt(emitter, emitterInterruptTimeout)
				})

				Describe("the second emitter", func() {
					It("becomes active", func() {
						Eventually(secondRunner.Buffer, 10).Should(gbytes.Say("emitter2.started"))
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

	Context("when the legacyBBS has routes to emit in /desired and /actual", func() {
		var emitter ifrit.Process

		BeforeEach(func() {
			err := bbsClient.DesireLRP(desiredLRP)
			Expect(err).NotTo(HaveOccurred())

			err = bbsClient.StartActualLRP(&lrpKey, &instanceKey, &netInfo)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("and the emitter is started", func() {
			BeforeEach(func() {
				emitter = ginkgomon.Invoke(createEmitterRunner("route-emitter"))
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
					Eventually(registeredRoutes).Should(Receive())

					hostnames = []string{"route-1", "route-2", "route-3"}

					updateRequest := &models.DesiredLRPUpdate{
						Routes:     newRoutes(hostnames, containerPort),
						Instances:  &desiredLRP.Instances,
						Annotation: &desiredLRP.Annotation,
					}
					err := bbsClient.UpdateDesiredLRP(processGuid, updateRequest)
					Expect(err).NotTo(HaveOccurred())
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
					Eventually(registeredRoutes).Should(Receive())

					updateRequest := &models.DesiredLRPUpdate{
						Routes:     newRoutes([]string{"route-2"}, containerPort),
						Instances:  &desiredLRP.Instances,
						Annotation: &desiredLRP.Annotation,
					}
					err := bbsClient.UpdateDesiredLRP(processGuid, updateRequest)
					Expect(err).NotTo(HaveOccurred())
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

func newRoutes(hosts []string, port uint32) *models.Routes {
	routingInfoPtr := cfroutes.CFRoutes{
		{Hostnames: hosts, Port: port},
	}.RoutingInfo()

	routes := models.Routes{}

	routingInfo := *routingInfoPtr
	for key, message := range routingInfo {
		routes[key] = message
	}

	return &routes
}
