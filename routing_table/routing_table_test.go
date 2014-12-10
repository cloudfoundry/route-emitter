package routing_table_test

import (
	. "github.com/cloudfoundry-incubator/route-emitter/routing_table"
	. "github.com/cloudfoundry-incubator/route-emitter/routing_table/matchers"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("RoutingTable", func() {
	var (
		table          RoutingTable
		messagesToEmit MessagesToEmit
	)

	pg := "some-process-guid"
	route1 := "foo.com"
	route2 := "bar.com"
	route3 := "baz.com"
	container1 := Container{"ig-1", "1.1.1.1", 11}
	container2 := Container{"ig-2", "2.2.2.2", 22}
	container3 := Container{"ig-3", "3.3.3.3", 33}
	logGuid := "some-log-guid"

	BeforeEach(func() {
		table = New()
	})

	Describe("When syncing", func() {
		Context("when a new process guid arrives", func() {
			Context("when the process guid has both routes and containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: Routes{URIs: []string{route1, route2}, LogGuid: logGuid}},
						ContainersByProcessGuid{pg: {container1, container2}},
					)
				})

				It("should emit registrations for each pairing", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							// do not use the RegistryMessageFor helper here; if the helper
							// is borked the tests hide it.
							//
							// we still use it for convenience in later tests.
							{URIs: []string{route1, route2}, Host: "1.1.1.1", Port: 11, App: logGuid, PrivateInstanceId: "ig-1"},
							{URIs: []string{route1, route2}, Host: "2.2.2.2", Port: 22, App: logGuid, PrivateInstanceId: "ig-2"},
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the process only has routes", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: Routes{URIs: []string{route1}, LogGuid: logGuid}},
						ContainersByProcessGuid{},
					)
				})

				It("should not emit a registration", func() {
					Ω(messagesToEmit).Should(BeZero())
				})

				Context("when the containers subsequently arrive", func() {
					BeforeEach(func() {
						messagesToEmit = table.Sync(
							RoutesByProcessGuid{pg: Routes{URIs: []string{route1}, LogGuid: logGuid}},
							ContainersByProcessGuid{pg: {container1}},
						)
					})

					It("should emit registrations for each pairing", func() {
						expected := MessagesToEmit{
							RegistrationMessages: []RegistryMessage{
								RegistryMessageFor(container1, Routes{URIs: []string{route1}, LogGuid: logGuid}),
							},
						}
						Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
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
						Ω(messagesToEmit).Should(BeZero())
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
					Ω(messagesToEmit).Should(BeZero())
				})

				Context("when the routes subsequently arrive", func() {
					BeforeEach(func() {
						messagesToEmit = table.Sync(
							RoutesByProcessGuid{pg: Routes{URIs: []string{route1}, LogGuid: logGuid}},
							ContainersByProcessGuid{pg: {container1}},
						)
					})

					It("should emit registrations for each pairing", func() {
						expected := MessagesToEmit{
							RegistrationMessages: []RegistryMessage{
								RegistryMessageFor(container1, Routes{URIs: []string{route1}, LogGuid: logGuid}),
							},
						}
						Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
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
						Ω(messagesToEmit).Should(BeZero())
					})
				})
			})
		})

		Context("when there is an existing process guid", func() {
			BeforeEach(func() {
				table.Sync(
					RoutesByProcessGuid{pg: Routes{URIs: []string{route1, route2}, LogGuid: logGuid}},
					ContainersByProcessGuid{pg: {container1, container2}},
				)
			})

			Context("when nothing changes", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: Routes{URIs: []string{route1, route2}, LogGuid: logGuid}},
						ContainersByProcessGuid{pg: {container1, container2}},
					)
				})

				It("should emit all registrations and no unregisration", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
							RegistryMessageFor(container2, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the process guid gets new routes", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: Routes{URIs: []string{route1, route2, route3}, LogGuid: logGuid}},
						ContainersByProcessGuid{pg: {container1, container2}},
					)
				})

				It("should emit all registrations and no unregisration", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1, route2, route3}, LogGuid: logGuid}),
							RegistryMessageFor(container2, Routes{URIs: []string{route1, route2, route3}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the process guid gets new containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: Routes{URIs: []string{route1, route2}, LogGuid: logGuid}},
						ContainersByProcessGuid{pg: {container1, container2, container3}},
					)
				})

				It("should emit all registrations and no unregisration", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
							RegistryMessageFor(container2, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
							RegistryMessageFor(container3, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the process guid gets new routes and containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: Routes{URIs: []string{route1, route2, route3}, LogGuid: logGuid}},
						ContainersByProcessGuid{pg: {container1, container2, container3}},
					)
				})

				It("should emit all registrations and no unregisration", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1, route2, route3}, LogGuid: logGuid}),
							RegistryMessageFor(container2, Routes{URIs: []string{route1, route2, route3}, LogGuid: logGuid}),
							RegistryMessageFor(container3, Routes{URIs: []string{route1, route2, route3}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the process guid loses routes", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: Routes{URIs: []string{route1}, LogGuid: logGuid}},
						ContainersByProcessGuid{pg: {container1, container2}},
					)
				})

				It("should emit all registrations and the relevant unregisrations", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1}, LogGuid: logGuid}),
							RegistryMessageFor(container2, Routes{URIs: []string{route1}, LogGuid: logGuid}),
						},
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route2}, LogGuid: logGuid}),
							RegistryMessageFor(container2, Routes{URIs: []string{route2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the process guid loses containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: Routes{URIs: []string{route1, route2}, LogGuid: logGuid}},
						ContainersByProcessGuid{pg: {container1}},
					)
				})

				It("should emit all registrations and the relevant unregisrations", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
						},
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(container2, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the process guid loses both routes and containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: Routes{URIs: []string{route1}, LogGuid: logGuid}},
						ContainersByProcessGuid{pg: {container1}},
					)
				})

				It("should emit all registrations and the relevant unregisrations", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1}, LogGuid: logGuid}),
						},
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route2}, LogGuid: logGuid}),
							RegistryMessageFor(container2, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the process guid gains routes but loses containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: Routes{URIs: []string{route1, route2, route3}, LogGuid: logGuid}},
						ContainersByProcessGuid{pg: {container1}},
					)
				})

				It("should emit all registrations and the relevant unregisrations", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1, route2, route3}, LogGuid: logGuid}),
						},
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(container2, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the process guid loses routes but gains containers", func() {
				BeforeEach(func() {
					messagesToEmit = table.Sync(
						RoutesByProcessGuid{pg: Routes{URIs: []string{route1}, LogGuid: logGuid}},
						ContainersByProcessGuid{pg: {container1, container2, container3}},
					)
				})

				It("should emit all registrations and the relevant unregisrations", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1}, LogGuid: logGuid}),
							RegistryMessageFor(container2, Routes{URIs: []string{route1}, LogGuid: logGuid}),
							RegistryMessageFor(container3, Routes{URIs: []string{route1}, LogGuid: logGuid}),
						},
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route2}, LogGuid: logGuid}),
							RegistryMessageFor(container2, Routes{URIs: []string{route2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
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
					expected := MessagesToEmit{
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
							RegistryMessageFor(container2, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
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
						Ω(messagesToEmit).Should(BeZero())
					})
				})

				Context("when the original registration had no containers, and then the process guid loses a route", func() {
					BeforeEach(func() {
						//override previous set up
						table.Sync(
							RoutesByProcessGuid{pg: Routes{URIs: []string{route1, route2}, LogGuid: logGuid}},
							ContainersByProcessGuid{},
						)

						messagesToEmit = table.Sync(
							RoutesByProcessGuid{pg: Routes{URIs: []string{route1}, LogGuid: logGuid}},
							ContainersByProcessGuid{},
						)
					})

					It("should emit nothing", func() {
						Ω(messagesToEmit).Should(BeZero())
					})
				})
			})
		})
	})

	Describe("Processing deltas", func() {
		Context("when the table is empty", func() {
			Context("When setting routes", func() {
				It("should not emit anything", func() {
					messagesToEmit = table.SetRoutes(pg, Routes{URIs: []string{route1, route2}, LogGuid: logGuid})
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when removing routes", func() {
				It("should not emit anything", func() {
					messagesToEmit = table.RemoveRoutes(pg)
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when adding/updating containers", func() {
				It("should not emit anything", func() {
					messagesToEmit = table.AddOrUpdateContainer(pg, container1)
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when removing containers", func() {
				It("should not emit anything", func() {
					messagesToEmit = table.RemoveContainer(pg, container1)
					Ω(messagesToEmit).Should(BeZero())
				})
			})
		})

		Context("when there are both containers and routes in the table", func() {
			BeforeEach(func() {
				table.SetRoutes(pg, Routes{URIs: []string{route1, route2}, LogGuid: logGuid})
				table.AddOrUpdateContainer(pg, container1)
				table.AddOrUpdateContainer(pg, container2)
			})

			Context("When setting routes", func() {
				Context("when the routes do not change", func() {
					It("should emit nothing", func() {
						messagesToEmit = table.SetRoutes(pg, Routes{URIs: []string{route1, route2}, LogGuid: logGuid})
						Ω(messagesToEmit).Should(BeZero())
					})
				})

				Context("when routes are added", func() {
					It("should emit registrations", func() {
						messagesToEmit = table.SetRoutes(pg, Routes{URIs: []string{route1, route2, route3}, LogGuid: logGuid})

						expected := MessagesToEmit{
							RegistrationMessages: []RegistryMessage{
								RegistryMessageFor(container1, Routes{URIs: []string{route1, route2, route3}, LogGuid: logGuid}),
								RegistryMessageFor(container2, Routes{URIs: []string{route1, route2, route3}, LogGuid: logGuid}),
							},
						}
						Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
					})
				})

				Context("when routes are removed", func() {
					It("should emit unregistrations and registrations", func() {
						messagesToEmit = table.SetRoutes(pg, Routes{URIs: []string{route1}, LogGuid: logGuid})

						expected := MessagesToEmit{
							RegistrationMessages: []RegistryMessage{
								RegistryMessageFor(container1, Routes{URIs: []string{route1}, LogGuid: logGuid}),
								RegistryMessageFor(container2, Routes{URIs: []string{route1}, LogGuid: logGuid}),
							},
							UnregistrationMessages: []RegistryMessage{
								RegistryMessageFor(container1, Routes{URIs: []string{route2}, LogGuid: logGuid}),
								RegistryMessageFor(container2, Routes{URIs: []string{route2}, LogGuid: logGuid}),
							},
						}
						Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
					})
				})

				Context("when routes are added and removed", func() {
					It("should emit registrations and unregistrations", func() {
						messagesToEmit = table.SetRoutes(pg, Routes{URIs: []string{route1, route3}, LogGuid: logGuid})

						expected := MessagesToEmit{
							RegistrationMessages: []RegistryMessage{
								RegistryMessageFor(container1, Routes{URIs: []string{route1, route3}, LogGuid: logGuid}),
								RegistryMessageFor(container2, Routes{URIs: []string{route1, route3}, LogGuid: logGuid}),
							},
							UnregistrationMessages: []RegistryMessage{
								RegistryMessageFor(container1, Routes{URIs: []string{route2}, LogGuid: logGuid}),
								RegistryMessageFor(container2, Routes{URIs: []string{route2}, LogGuid: logGuid}),
							},
						}
						Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
					})
				})
			})

			Context("when removing routes", func() {
				It("should emit unregistrations", func() {
					messagesToEmit = table.RemoveRoutes(pg)

					expected := MessagesToEmit{
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
							RegistryMessageFor(container2, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when adding/updating containers", func() {
				Context("when the container already exists", func() {
					It("should emit nothing", func() {
						messagesToEmit = table.AddOrUpdateContainer(pg, container1)
						Ω(messagesToEmit).Should(BeZero())
					})
				})

				Context("when the container does not already exist", func() {
					It("should emit registrations", func() {
						messagesToEmit = table.AddOrUpdateContainer(pg, container3)

						expected := MessagesToEmit{
							RegistrationMessages: []RegistryMessage{
								RegistryMessageFor(container3, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
							},
						}
						Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
					})
				})
			})

			Context("when removing containers", func() {
				It("should emit unregistrations", func() {
					messagesToEmit = table.RemoveContainer(pg, container2)

					expected := MessagesToEmit{
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(container2, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})
		})

		Context("when there are only routes in the table", func() {
			BeforeEach(func() {
				table.SetRoutes(pg, Routes{URIs: []string{route1, route2}, LogGuid: logGuid})
			})

			Context("When setting routes", func() {
				It("should emit nothing", func() {
					messagesToEmit = table.SetRoutes(pg, Routes{URIs: []string{route1, route3}, LogGuid: logGuid})
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when removing routes", func() {
				It("should emit nothing", func() {
					messagesToEmit = table.RemoveRoutes(pg)
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when adding/updating containers", func() {
				It("should emit registrations", func() {
					messagesToEmit = table.AddOrUpdateContainer(pg, container1)

					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})
		})

		Context("when there are only containers in the table", func() {
			BeforeEach(func() {
				table.AddOrUpdateContainer(pg, container1)
				table.AddOrUpdateContainer(pg, container2)
			})

			Context("When setting routes", func() {
				It("should emit registrations", func() {
					messagesToEmit = table.SetRoutes(pg, Routes{URIs: []string{route1, route2}, LogGuid: logGuid})

					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(container1, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
							RegistryMessageFor(container2, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when removing routes", func() {
				It("should emit nothing", func() {
					messagesToEmit = table.RemoveRoutes(pg)
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when adding/updating containers", func() {
				It("should emit nothing", func() {
					messagesToEmit = table.AddOrUpdateContainer(pg, container2)
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when removing containers", func() {
				It("should emit nothing", func() {
					messagesToEmit = table.RemoveContainer(pg, container1)
					Ω(messagesToEmit).Should(BeZero())
				})
			})
		})
	})

	Describe("MessagesToEmit", func() {
		Context("when the table is empty", func() {
			It("should be empty", func() {
				messagesToEmit = table.MessagesToEmit()
				Ω(messagesToEmit).Should(BeZero())
			})
		})

		Context("when the table has routes but no containers", func() {
			BeforeEach(func() {
				table.SetRoutes(pg, Routes{URIs: []string{route1, route2}, LogGuid: logGuid})
			})

			It("should be empty", func() {
				messagesToEmit = table.MessagesToEmit()
				Ω(messagesToEmit).Should(BeZero())
			})
		})

		Context("when the table has containers but no routes", func() {
			BeforeEach(func() {
				table.AddOrUpdateContainer(pg, container1)
				table.AddOrUpdateContainer(pg, container2)
			})

			It("should be empty", func() {
				messagesToEmit = table.MessagesToEmit()
				Ω(messagesToEmit).Should(BeZero())
			})
		})

		Context("when the table has routes and containers", func() {
			BeforeEach(func() {
				table.SetRoutes(pg, Routes{URIs: []string{route1, route2}, LogGuid: logGuid})
				table.AddOrUpdateContainer(pg, container1)
				table.AddOrUpdateContainer(pg, container2)
			})

			It("should emit the registrations", func() {
				messagesToEmit = table.MessagesToEmit()

				expected := MessagesToEmit{
					RegistrationMessages: []RegistryMessage{
						RegistryMessageFor(container1, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
						RegistryMessageFor(container2, Routes{URIs: []string{route1, route2}, LogGuid: logGuid}),
					},
				}
				Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
			})
		})
	})

	Describe("RouteCount", func() {
		It("returns 0 on a new routing table", func() {
			Expect(table.RouteCount()).To(Equal(0))
		})

		It("returns 1 after adding a route", func() {
			table.SetRoutes("fake-process-guid", Routes{URIs: []string{"fake-route-url"}, LogGuid: logGuid})
			Expect(table.RouteCount()).To(Equal(1))
		})

		It("returns 2 after associating 2 urls with a process", func() {
			table.SetRoutes("fake-process-guid", Routes{URIs: []string{"fake-route-url-1", "fake-route-url-2"}, LogGuid: logGuid})

			Expect(table.RouteCount()).To(Equal(2))
		})

		It("returns 2 after associating 2 urls with a process", func() {
			table.SetRoutes("fake-process-guid-a", Routes{URIs: []string{"fake-route-url-a-1", "fake-route-url-a-2"}, LogGuid: logGuid})
			table.SetRoutes("fake-process-guid-b", Routes{URIs: []string{"fake-route-url-b-1", "fake-route-url-b-2"}, LogGuid: logGuid})

			Expect(table.RouteCount()).To(Equal(4))
		})
	})
})
