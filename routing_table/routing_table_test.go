package routing_table_test

import (
	. "github.com/cloudfoundry-incubator/route-emitter/routing_table"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("RoutingTable", func() {
	var (
		table          *RoutingTable
		messagesToEmit MessagesToEmit
	)

	pg := "some-process-guid"
	route1 := "foo.com"
	route2 := "bar.com"
	route3 := "baz.com"
	container1 := Container{"1.1.1.1", 11}
	container2 := Container{"2.2.2.2", 22}
	container3 := Container{"3.3.3.3", 33}

	BeforeEach(func() {
		table = New()
	})

	Describe("When syncing", func() {
		Context("when a new process guid arrives", func() {
			Context("when the process guid has both routes and containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: {route1, route2}},
						ContainersByProcessGuid{pg: {container1, container2}},
					)
				})

				It("should emit registrations for each pairing", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(HaveLen(2))
					Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())

					message := RegistryMessageFor(container1, route1, route2)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route1, route2)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))
				})
			})

			Context("when the process only has routes", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: {route1}},
						ContainersByProcessGuid{},
					)
				})

				It("should not emit a registration", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(BeEmpty())
					Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())
				})

				Context("when the containers subsequently arrive", func() {
					BeforeEach(func() {
						messagesToEmit = table.Sync(
							RoutesByProcessGuid{pg: {route1}},
							ContainersByProcessGuid{pg: {container1}},
						)
					})

					It("should emit registrations for each pairing", func() {
						Ω(messagesToEmit.RegistrationMessages).Should(HaveLen(1))
						Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())

						message := RegistryMessageFor(container1, route1)
						Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))
					})
				})

				Context("when the process guid subsequently disappears", func() {
					BeforeEach(func() {
						messagesToEmit = table.Sync(
							RoutesByProcessGuid{},
							ContainersByProcessGuid{},
						)
					})

					It("should emit nothing", func() {
						Ω(messagesToEmit.RegistrationMessages).Should(BeEmpty())
						Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())
					})
				})
			})

			Context("when the process only has containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{},
						ContainersByProcessGuid{pg: {container1}},
					)
				})

				It("should not emit a registration", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(BeEmpty())
					Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())
				})

				Context("when the routes subsequently arrive", func() {
					BeforeEach(func() {
						messagesToEmit = table.Sync(
							RoutesByProcessGuid{pg: {route1}},
							ContainersByProcessGuid{pg: {container1}},
						)
					})

					It("should emit registrations for each pairing", func() {
						Ω(messagesToEmit.RegistrationMessages).Should(HaveLen(1))
						Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())

						message := RegistryMessageFor(container1, route1)
						Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))
					})
				})

				Context("when the process guid subsequently disappears", func() {
					BeforeEach(func() {
						messagesToEmit = table.Sync(
							RoutesByProcessGuid{},
							ContainersByProcessGuid{},
						)
					})

					It("should emit nothing", func() {
						Ω(messagesToEmit.RegistrationMessages).Should(BeEmpty())
						Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())
					})
				})
			})
		})

		Context("when there is an existing process guid", func() {
			BeforeEach(func() {
				table.Sync(
					RoutesByProcessGuid{pg: {route1, route2}},
					ContainersByProcessGuid{pg: {container1, container2}},
				)
			})

			Context("when nothing changes", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: {route1, route2}},
						ContainersByProcessGuid{pg: {container1, container2}},
					)
				})

				It("should emit all registrations and no unregisration", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(HaveLen(2))
					Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())

					message := RegistryMessageFor(container1, route1, route2)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route1, route2)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))
				})
			})

			Context("when the process guid gets new routes", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: {route1, route2, route3}},
						ContainersByProcessGuid{pg: {container1, container2}},
					)
				})

				It("should emit all registrations and no unregisration", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(HaveLen(2))
					Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())

					message := RegistryMessageFor(container1, route1, route2, route3)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route1, route2, route3)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))
				})
			})

			Context("when the process guid gets new containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: {route1, route2}},
						ContainersByProcessGuid{pg: {container1, container2, container3}},
					)
				})

				It("should emit all registrations and no unregisration", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(HaveLen(3))
					Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())

					message := RegistryMessageFor(container1, route1, route2)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route1, route2)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container3, route1, route2)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))
				})
			})

			Context("when the process guid gets new routes and containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: {route1, route2, route3}},
						ContainersByProcessGuid{pg: {container1, container2, container3}},
					)
				})

				It("should emit all registrations and no unregisration", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(HaveLen(3))
					Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())

					message := RegistryMessageFor(container1, route1, route2, route3)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route1, route2, route3)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container3, route1, route2, route3)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))
				})
			})

			Context("when the process guid loses routes", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: {route1}},
						ContainersByProcessGuid{pg: {container1, container2}},
					)
				})

				It("should emit all registrations and the relevant unregisrations", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(HaveLen(2))
					Ω(messagesToEmit.UnregistrationMessages).Should(HaveLen(2))

					message := RegistryMessageFor(container1, route1)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route1)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container1, route2)
					Ω(messagesToEmit.UnregistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route2)
					Ω(messagesToEmit.UnregistrationMessages).Should(ContainElement(message))
				})
			})

			Context("when the process guid loses containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: {route1, route2}},
						ContainersByProcessGuid{pg: {container1}},
					)
				})

				It("should emit all registrations and the relevant unregisrations", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(HaveLen(1))
					Ω(messagesToEmit.UnregistrationMessages).Should(HaveLen(1))

					message := RegistryMessageFor(container1, route1, route2)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route1, route2)
					Ω(messagesToEmit.UnregistrationMessages).Should(ContainElement(message))
				})
			})

			Context("when the process guid loses both routes and containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: {route1}},
						ContainersByProcessGuid{pg: {container1}},
					)
				})

				It("should emit all registrations and the relevant unregisrations", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(HaveLen(1))
					Ω(messagesToEmit.UnregistrationMessages).Should(HaveLen(2))

					message := RegistryMessageFor(container1, route1)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container1, route2)
					Ω(messagesToEmit.UnregistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route1, route2)
					Ω(messagesToEmit.UnregistrationMessages).Should(ContainElement(message))
				})
			})

			Context("when the process guid gains routes but loses containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: {route1, route2, route3}},
						ContainersByProcessGuid{pg: {container1}},
					)
				})

				It("should emit all registrations and the relevant unregisrations", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(HaveLen(1))
					Ω(messagesToEmit.UnregistrationMessages).Should(HaveLen(1))

					message := RegistryMessageFor(container1, route1, route2, route3)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route1, route2)
					Ω(messagesToEmit.UnregistrationMessages).Should(ContainElement(message))
				})
			})

			Context("when the process guid loses routes but gains containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: {route1}},
						ContainersByProcessGuid{pg: {container1, container2, container3}},
					)
				})

				It("should emit all registrations and the relevant unregisrations", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(HaveLen(3))
					Ω(messagesToEmit.UnregistrationMessages).Should(HaveLen(2))

					message := RegistryMessageFor(container1, route1)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route1)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container3, route1)
					Ω(messagesToEmit.RegistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container1, route2)
					Ω(messagesToEmit.UnregistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route2)
					Ω(messagesToEmit.UnregistrationMessages).Should(ContainElement(message))
				})
			})

			Context("when the process guid disappears entirely", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{},
						ContainersByProcessGuid{},
					)
				})

				It("should unregister the missing guids", func() {
					Ω(messagesToEmit.RegistrationMessages).Should(BeEmpty())
					Ω(messagesToEmit.UnregistrationMessages).Should(HaveLen(2))

					message := RegistryMessageFor(container1, route1, route2)
					Ω(messagesToEmit.UnregistrationMessages).Should(ContainElement(message))

					message = RegistryMessageFor(container2, route1, route2)
					Ω(messagesToEmit.UnregistrationMessages).Should(ContainElement(message))
				})
			})

			Describe("edge cases", func() {
				Context("when the original registration had no routes, and then the process guid loses containers", func() {
					BeforeEach(func() {
						//override previous set up
						table.Sync(
							RoutesByProcessGuid{},
							ContainersByProcessGuid{pg: {container1, container2}},
						)

						messagesToEmit = table.Sync(
							RoutesByProcessGuid{pg: {}},
							ContainersByProcessGuid{pg: {container1}},
						)
					})

					It("should emit nothing", func() {
						Ω(messagesToEmit.RegistrationMessages).Should(BeEmpty())
						Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())
					})
				})

				Context("when the original registration had no containers, and then the process guid loses a route", func() {
					BeforeEach(func() {
						//override previous set up
						table.Sync(
							RoutesByProcessGuid{pg: {route1, route2}},
							ContainersByProcessGuid{},
						)

						messagesToEmit = table.Sync(
							RoutesByProcessGuid{pg: {route1}},
							ContainersByProcessGuid{},
						)
					})

					It("should emit nothing", func() {
						Ω(messagesToEmit.RegistrationMessages).Should(BeEmpty())
						Ω(messagesToEmit.UnregistrationMessages).Should(BeEmpty())
					})
				})
			})
		})
	})
})
