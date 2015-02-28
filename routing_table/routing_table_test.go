package routing_table_test

import (
	"github.com/cloudfoundry-incubator/receptor"
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

	key := RoutingKey{ProcessGuid: "some-process-guid", ContainerPort: 8080}

	hostname1 := "foo.example.com"
	hostname2 := "bar.example.com"
	hostname3 := "baz.example.com"

	olderTag := receptor.ModificationTag{Epoch: "abc", Index: 0}
	currentTag := receptor.ModificationTag{Epoch: "abc", Index: 1}
	newerTag := receptor.ModificationTag{Epoch: "def", Index: 0}

	endpoint1 := Endpoint{InstanceGuid: "ig-1", Host: "1.1.1.1", Port: 11, ContainerPort: 8080, Evacuating: false, ModificationTag: currentTag}
	endpoint2 := Endpoint{InstanceGuid: "ig-2", Host: "2.2.2.2", Port: 22, ContainerPort: 8080, Evacuating: false, ModificationTag: currentTag}
	endpoint3 := Endpoint{InstanceGuid: "ig-3", Host: "3.3.3.3", Port: 33, ContainerPort: 8080, Evacuating: false, ModificationTag: currentTag}

	evacuating1 := Endpoint{InstanceGuid: "ig-1", Host: "1.1.1.1", Port: 11, ContainerPort: 8080, Evacuating: true, ModificationTag: currentTag}

	logGuid := "some-log-guid"

	BeforeEach(func() {
		table = NewTable()
	})

	Describe("Swap", func() {
		Context("when a new routing key arrives", func() {
			Context("when the routing key has both routes and endpoints", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint1, endpoint2}},
					)

					messagesToEmit = table.Swap(tempTable)
				})

				It("emits registrations for each pairing", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the process only has routes", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}},
						EndpointsByRoutingKey{},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("should not emit a registration", func() {
					Ω(messagesToEmit).Should(BeZero())
				})

				Context("when the endpoints subsequently arrive", func() {
					BeforeEach(func() {
						tempTable := NewTempTable(
							RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}},
							EndpointsByRoutingKey{key: {endpoint1}},
						)
						messagesToEmit = table.Swap(tempTable)
					})

					It("emits registrations for each pairing", func() {
						expected := MessagesToEmit{
							RegistrationMessages: []RegistryMessage{
								RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}),
							},
						}
						Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
					})
				})

				Context("when the routing key subsequently disappears", func() {
					BeforeEach(func() {
						tempTable := NewTempTable(
							RoutesByRoutingKey{},
							EndpointsByRoutingKey{},
						)
						messagesToEmit = table.Swap(tempTable)
					})

					It("emits nothing", func() {
						Ω(messagesToEmit).Should(BeZero())
					})
				})
			})

			Context("when the process only has endpoints", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{},
						EndpointsByRoutingKey{key: {endpoint1}},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("should not emit a registration", func() {
					Ω(messagesToEmit).Should(BeZero())
				})

				Context("when the routes subsequently arrive", func() {
					BeforeEach(func() {
						tempTable := NewTempTable(
							RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}},
							EndpointsByRoutingKey{key: {endpoint1}},
						)
						messagesToEmit = table.Swap(tempTable)
					})

					It("emits registrations for each pairing", func() {
						expected := MessagesToEmit{
							RegistrationMessages: []RegistryMessage{
								RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}),
							},
						}
						Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
					})
				})

				Context("when the endpoint subsequently disappears", func() {
					BeforeEach(func() {
						tempTable := NewTempTable(
							RoutesByRoutingKey{},
							EndpointsByRoutingKey{},
						)
						messagesToEmit = table.Swap(tempTable)
					})

					It("emits nothing", func() {
						Ω(messagesToEmit).Should(BeZero())
					})
				})
			})
		})

		Context("when there is an existing routing key", func() {
			BeforeEach(func() {
				tempTable := NewTempTable(
					RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}},
					EndpointsByRoutingKey{key: {endpoint1, endpoint2}},
				)
				table.Swap(tempTable)
			})

			Context("when nothing changes", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint1, endpoint2}},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("emits all registrations and no unregisration", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the routing key gets new routes", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint1, endpoint2}},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("emits all registrations and no unregisration", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the routing key gets new endpoints", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint1, endpoint2, endpoint3}},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("emits all registrations and no unregisration", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint3, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the routing key gets a new evacuating endpoint", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint1, endpoint2, evacuating1}},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("emits all registrations and no unregisration", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(evacuating1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the routing key has an evacuating and instance endpoint", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint1, endpoint2, evacuating1}},
					)
					table.Swap(tempTable)

					tempTable = NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint2, evacuating1}},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("should not emit an unregistration ", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(evacuating1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the routing key gets new routes and endpoints", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint1, endpoint2, endpoint3}},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("emits all registrations and no unregisration", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint3, Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the routing key loses routes", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint1, endpoint2}},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("emits all registrations and the relevant unregisrations", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}),
						},
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the routing key loses endpoints", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint1}},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("emits all registrations and the relevant unregisrations", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the routing key loses both routes and endpoints", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint1}},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("emits all registrations and the relevant unregisrations", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}),
						},
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the routing key gains routes but loses endpoints", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint1}},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("emits all registrations and the relevant unregisrations", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid}),
						},
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the routing key loses routes but gains endpoints", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}},
						EndpointsByRoutingKey{key: {endpoint1, endpoint2, endpoint3}},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("emits all registrations and the relevant unregisrations", func() {
					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint3, Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}),
						},
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when the routing key disappears entirely", func() {
				BeforeEach(func() {
					tempTable := NewTempTable(
						RoutesByRoutingKey{},
						EndpointsByRoutingKey{},
					)
					messagesToEmit = table.Swap(tempTable)
				})

				It("should unregister the missing guids", func() {
					expected := MessagesToEmit{
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Describe("edge cases", func() {
				Context("when the original registration had no routes, and then the routing key loses endpoints", func() {
					BeforeEach(func() {
						//override previous set up
						tempTable := NewTempTable(
							RoutesByRoutingKey{},
							EndpointsByRoutingKey{key: {endpoint1, endpoint2}},
						)
						table.Swap(tempTable)

						tempTable = NewTempTable(
							RoutesByRoutingKey{key: {}},
							EndpointsByRoutingKey{key: {endpoint1}},
						)
						messagesToEmit = table.Swap(tempTable)
					})

					It("emits nothing", func() {
						Ω(messagesToEmit).Should(BeZero())
					})
				})

				Context("when the original registration had no endpoints, and then the routing key loses a route", func() {
					BeforeEach(func() {
						//override previous set up
						tempTable := NewTempTable(
							RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}},
							EndpointsByRoutingKey{},
						)
						table.Swap(tempTable)

						tempTable = NewTempTable(
							RoutesByRoutingKey{key: Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}},
							EndpointsByRoutingKey{},
						)
						messagesToEmit = table.Swap(tempTable)
					})

					It("emits nothing", func() {
						Ω(messagesToEmit).Should(BeZero())
					})
				})
			})
		})
	})

	Describe("Processing deltas", func() {
		Context("when the table is empty", func() {
			Context("When setting routes", func() {
				It("emits nothing", func() {
					messagesToEmit = table.SetRoutes(key, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid, ModificationTag: currentTag})
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when removing routes", func() {
				It("emits nothing", func() {
					messagesToEmit = table.RemoveRoutes(key, currentTag)
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when adding/updating endpoints", func() {
				It("emits nothing", func() {
					messagesToEmit = table.AddEndpoint(key, endpoint1)
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when removing endpoints", func() {
				It("emits nothing", func() {
					messagesToEmit = table.RemoveEndpoint(key, endpoint1)
					Ω(messagesToEmit).Should(BeZero())
				})
			})
		})

		Context("when there are both endpoints and routes in the table", func() {
			BeforeEach(func() {
				table.SetRoutes(key, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid, ModificationTag: currentTag})
				table.AddEndpoint(key, endpoint1)
				table.AddEndpoint(key, endpoint2)
			})

			Describe("SetRoutes", func() {
				It("emits nothing when the route's hostnames do not change", func() {
					messagesToEmit = table.SetRoutes(key, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid, ModificationTag: currentTag})
					Ω(messagesToEmit).Should(BeZero())
				})

				It("emits nothing when a hostname is added to a route with an older tag", func() {
					messagesToEmit = table.SetRoutes(key, Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid, ModificationTag: olderTag})
					Ω(messagesToEmit).Should(BeZero())
				})

				It("emits registrations when a hostname is added to a route with a newer tag", func() {
					messagesToEmit = table.SetRoutes(key, Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid, ModificationTag: newerTag})

					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2, hostname3}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})

				It("emits nothing when a hostname is removed from a route with an older tag", func() {
					messagesToEmit = table.SetRoutes(key, Routes{Hostnames: []string{hostname1}, LogGuid: logGuid, ModificationTag: olderTag})
					Ω(messagesToEmit).Should(BeZero())
				})

				It("emits registrations and unregistrations when a hostname is removed from a route with a newer tag", func() {
					messagesToEmit = table.SetRoutes(key, Routes{Hostnames: []string{hostname1}, LogGuid: logGuid, ModificationTag: newerTag})

					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1}, LogGuid: logGuid}),
						},
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})

				It("emits nothing when hostnames are added and removed from a route with an older tag", func() {
					messagesToEmit = table.SetRoutes(key, Routes{Hostnames: []string{hostname1, hostname3}, LogGuid: logGuid, ModificationTag: olderTag})
					Ω(messagesToEmit).Should(BeZero())
				})

				It("emits registrations and unregistrations when hostnames are added and removed from a route with a newer tag", func() {
					messagesToEmit = table.SetRoutes(key, Routes{Hostnames: []string{hostname1, hostname3}, LogGuid: logGuid, ModificationTag: newerTag})

					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname3}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname3}, LogGuid: logGuid}),
						},
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("RemoveRoutes", func() {
				It("emits unregistrations with a newer tag", func() {
					messagesToEmit = table.RemoveRoutes(key, newerTag)

					expected := MessagesToEmit{
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})

				It("emits unregistrations with the same tag", func() {
					messagesToEmit = table.RemoveRoutes(key, newerTag)

					expected := MessagesToEmit{
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})

				It("emits nothing when the tag is older", func() {
					messagesToEmit = table.RemoveRoutes(key, olderTag)
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("AddEndpoint", func() {
				It("emits nothing when the tag is the same", func() {
					messagesToEmit = table.AddEndpoint(key, endpoint1)
					Ω(messagesToEmit).Should(BeZero())
				})

				It("emits nothing when updating an endpoint with an older tag", func() {
					updatedEndpoint := endpoint1
					updatedEndpoint.ModificationTag = olderTag

					messagesToEmit = table.AddEndpoint(key, updatedEndpoint)
					Ω(messagesToEmit).Should(BeZero())
				})

				It("emits nothing when updating an endpoint with a newer tag", func() {
					updatedEndpoint := endpoint1
					updatedEndpoint.ModificationTag = newerTag

					messagesToEmit = table.AddEndpoint(key, updatedEndpoint)
					Ω(messagesToEmit).Should(BeZero())
				})

				It("emits registrations when adding an endpoint", func() {
					messagesToEmit = table.AddEndpoint(key, endpoint3)

					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint3, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})

				Context("when an evacuating endpoint is added for an instance that already exists", func() {
					It("emits nothing", func() {
						messagesToEmit = table.AddEndpoint(key, evacuating1)
						Ω(messagesToEmit).Should(BeZero())
					})
				})

				Context("when an instance endpoint is updated for an evacuating that already exists", func() {
					BeforeEach(func() {
						table.AddEndpoint(key, evacuating1)
					})

					It("emits nothing", func() {
						messagesToEmit = table.AddEndpoint(key, endpoint1)
						Ω(messagesToEmit).Should(BeZero())
					})
				})

				Context("when the endpoint does not already exist", func() {
					It("emits registrations", func() {
						messagesToEmit = table.AddEndpoint(key, endpoint3)

						expected := MessagesToEmit{
							RegistrationMessages: []RegistryMessage{
								RegistryMessageFor(endpoint3, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
							},
						}
						Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
					})
				})
			})

			Context("when removing endpoints", func() {
				It("emits unregistrations with the same tag", func() {
					messagesToEmit = table.RemoveEndpoint(key, endpoint2)

					expected := MessagesToEmit{
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})

				It("emits unregistrations when the tag is newer", func() {
					newerEndpoint := endpoint2
					newerEndpoint.ModificationTag = newerTag
					messagesToEmit = table.RemoveEndpoint(key, newerEndpoint)

					expected := MessagesToEmit{
						UnregistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})

				It("emits nothing when the tag is older", func() {
					olderEndpoint := endpoint2
					olderEndpoint.ModificationTag = olderTag
					messagesToEmit = table.RemoveEndpoint(key, olderEndpoint)
					Ω(messagesToEmit).Should(BeZero())
				})

				Context("when an instance endpoint is removed for an instance that already exists", func() {
					BeforeEach(func() {
						table.AddEndpoint(key, evacuating1)
					})

					It("emits nothing", func() {
						messagesToEmit = table.RemoveEndpoint(key, endpoint1)
						Ω(messagesToEmit).Should(BeZero())
					})
				})

				Context("when an evacuating endpoint is removed instance that already exists", func() {
					BeforeEach(func() {
						table.AddEndpoint(key, evacuating1)
					})

					It("emits nothing", func() {
						messagesToEmit = table.AddEndpoint(key, endpoint1)
						Ω(messagesToEmit).Should(BeZero())
					})
				})
			})
		})

		Context("when there are only routes in the table", func() {
			BeforeEach(func() {
				table.SetRoutes(key, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid})
			})

			Context("When setting routes", func() {
				It("emits nothing", func() {
					messagesToEmit = table.SetRoutes(key, Routes{Hostnames: []string{hostname1, hostname3}, LogGuid: logGuid})
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when removing routes", func() {
				It("emits nothing", func() {
					messagesToEmit = table.RemoveRoutes(key, currentTag)
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when adding/updating endpoints", func() {
				It("emits registrations", func() {
					messagesToEmit = table.AddEndpoint(key, endpoint1)

					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})
		})

		Context("when there are only endpoints in the table", func() {
			BeforeEach(func() {
				table.AddEndpoint(key, endpoint1)
				table.AddEndpoint(key, endpoint2)
			})

			Context("When setting routes", func() {
				It("emits registrations", func() {
					messagesToEmit = table.SetRoutes(key, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid, ModificationTag: currentTag})

					expected := MessagesToEmit{
						RegistrationMessages: []RegistryMessage{
							RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
							RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						},
					}
					Ω(messagesToEmit).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when removing routes", func() {
				It("emits nothing", func() {
					messagesToEmit = table.RemoveRoutes(key, currentTag)
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when adding/updating endpoints", func() {
				It("emits nothing", func() {
					messagesToEmit = table.AddEndpoint(key, endpoint2)
					Ω(messagesToEmit).Should(BeZero())
				})
			})

			Context("when removing endpoints", func() {
				It("emits nothing", func() {
					messagesToEmit = table.RemoveEndpoint(key, endpoint1)
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

		Context("when the table has routes but no endpoints", func() {
			BeforeEach(func() {
				table.SetRoutes(key, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid})
			})

			It("should be empty", func() {
				messagesToEmit = table.MessagesToEmit()
				Ω(messagesToEmit).Should(BeZero())
			})
		})

		Context("when the table has endpoints but no routes", func() {
			BeforeEach(func() {
				table.AddEndpoint(key, endpoint1)
				table.AddEndpoint(key, endpoint2)
			})

			It("should be empty", func() {
				messagesToEmit = table.MessagesToEmit()
				Ω(messagesToEmit).Should(BeZero())
			})
		})

		Context("when the table has routes and endpoints", func() {
			BeforeEach(func() {
				table.SetRoutes(key, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid})
				table.AddEndpoint(key, endpoint1)
				table.AddEndpoint(key, endpoint2)
			})

			It("emits the registrations", func() {
				messagesToEmit = table.MessagesToEmit()

				expected := MessagesToEmit{
					RegistrationMessages: []RegistryMessage{
						RegistryMessageFor(endpoint1, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
						RegistryMessageFor(endpoint2, Routes{Hostnames: []string{hostname1, hostname2}, LogGuid: logGuid}),
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

		It("returns 1 after adding a route to a single process", func() {
			table.SetRoutes(RoutingKey{ProcessGuid: "fake-process-guid"}, Routes{Hostnames: []string{"fake-route-url"}, LogGuid: logGuid})

			Expect(table.RouteCount()).To(Equal(1))
		})

		It("returns 2 after associating 2 urls with a single process", func() {
			table.SetRoutes(RoutingKey{ProcessGuid: "fake-process-guid"}, Routes{Hostnames: []string{"fake-route-url-1", "fake-route-url-2"}, LogGuid: logGuid})

			Expect(table.RouteCount()).To(Equal(2))
		})

		It("returns 4 after associating 2 urls with two processes", func() {
			table.SetRoutes(RoutingKey{ProcessGuid: "fake-process-guid-a"}, Routes{Hostnames: []string{"fake-route-url-a-1", "fake-route-url-a-2"}, LogGuid: logGuid})
			table.SetRoutes(RoutingKey{ProcessGuid: "fake-process-guid-b"}, Routes{Hostnames: []string{"fake-route-url-b-1", "fake-route-url-b-2"}, LogGuid: logGuid})

			Expect(table.RouteCount()).To(Equal(4))
		})
	})
})
