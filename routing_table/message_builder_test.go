package routing_table_test

import (
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	. "github.com/cloudfoundry-incubator/route-emitter/routing_table/matchers"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("MessagesToEmitBuilder", func() {
	var builder routing_table.MessagesToEmitBuilder
	var existingEntry *routing_table.RoutableEndpoints
	var newEntry *routing_table.RoutableEndpoints
	var messages routing_table.MessagesToEmit

	hostname1 := "foo.example.com"
	hostname2 := "bar.example.com"

	currentTag := receptor.ModificationTag{Epoch: "abc", Index: 1}
	endpoint1 := routing_table.Endpoint{InstanceGuid: "ig-1", Host: "1.1.1.1", Port: 11, ContainerPort: 8080, Evacuating: false, ModificationTag: currentTag}
	endpoint2 := routing_table.Endpoint{InstanceGuid: "ig-2", Host: "2.2.2.2", Port: 22, ContainerPort: 8080, Evacuating: false, ModificationTag: currentTag}

	BeforeEach(func() {
		builder = routing_table.MessagesToEmitBuilder{}
	})

	Describe("RegistrationsFor", func() {
		BeforeEach(func() {
			existingEntry = nil

			newEntry = &routing_table.RoutableEndpoints{
				Hostnames: map[string]struct{}{hostname1: struct{}{}},
				Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
			}
		})

		JustBeforeEach(func() {
			messages = builder.RegistrationsFor(existingEntry, newEntry)
		})

		Context("when no existing entry", func() {
			It("emits a registration", func() {
				expected := routing_table.MessagesToEmit{
					RegistrationMessages: []routing_table.RegistryMessage{
						routing_table.RegistryMessageFor(endpoint1, routing_table.Routes{Hostnames: []string{hostname1}}),
					},
				}
				Ω(messages).Should(MatchMessagesToEmit(expected))
			})
		})

		Context("when new entry has no hostnames", func() {
			BeforeEach(func() {
				newEntry.Hostnames = make(map[string]struct{})
			})

			It("emits nothing", func() {
				Ω(messages).Should(BeZero())
			})
		})

		Context("when we have an existing entry", func() {
			Context("when existing == new", func() {
				BeforeEach(func() {
					existingEntry = newEntry
				})

				It("emits nothing", func() {
					Ω(messages).Should(BeZero())
				})
			})

			Context("when hostnames change", func() {
				BeforeEach(func() {
					existingEntry = &routing_table.RoutableEndpoints{
						Hostnames: map[string]struct{}{hostname2: struct{}{}},
						Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
					}
				})

				It("emits a registration", func() {
					expected := routing_table.MessagesToEmit{
						RegistrationMessages: []routing_table.RegistryMessage{
							routing_table.RegistryMessageFor(endpoint1, routing_table.Routes{Hostnames: []string{hostname1}}),
						},
					}
					Ω(messages).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when endpoints are changed", func() {
				Context("when endpoints are added", func() {
					BeforeEach(func() {
						existingEntry = &routing_table.RoutableEndpoints{
							Hostnames: map[string]struct{}{hostname1: struct{}{}},
							Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
						}

						newEntry.Endpoints = routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1, endpoint2})
					})

					It("emits a registration", func() {
						expected := routing_table.MessagesToEmit{
							RegistrationMessages: []routing_table.RegistryMessage{
								routing_table.RegistryMessageFor(endpoint2, routing_table.Routes{Hostnames: []string{hostname1}}),
							},
						}
						Ω(messages).Should(MatchMessagesToEmit(expected))
					})
				})

				Context("when endpoints are removed", func() {
					BeforeEach(func() {
						existingEntry = &routing_table.RoutableEndpoints{
							Hostnames: map[string]struct{}{hostname1: struct{}{}},
							Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1, endpoint2}),
						}

						newEntry.Endpoints = routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1})
					})

					It("emits nothing", func() {
						Ω(messages).Should(BeZero())
					})
				})
			})
		})
	})

	Describe("UnregistrationsFor", func() {
		JustBeforeEach(func() {
			messages = builder.UnregistrationsFor(existingEntry, newEntry)
		})

		Context("when there are no hostnames in the existing", func() {
			BeforeEach(func() {
				existingEntry = &routing_table.RoutableEndpoints{
					Hostnames: map[string]struct{}{},
					Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
				}

				newEntry = &routing_table.RoutableEndpoints{
					Hostnames: map[string]struct{}{hostname1: struct{}{}},
					Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
				}
			})

			It("emits nothing", func() {
				Ω(messages).Should(BeZero())
			})
		})

		Context("when hostnames change", func() {
			Context("when a hostname removed", func() {
				BeforeEach(func() {
					existingEntry = &routing_table.RoutableEndpoints{
						Hostnames: map[string]struct{}{hostname1: struct{}{}, hostname2: struct{}{}},
						Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
					}

					newEntry = &routing_table.RoutableEndpoints{
						Hostnames: map[string]struct{}{},
						Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
					}
				})

				It("emits an unregistration", func() {
					expected := routing_table.MessagesToEmit{
						UnregistrationMessages: []routing_table.RegistryMessage{
							routing_table.RegistryMessageFor(endpoint1, routing_table.Routes{Hostnames: []string{hostname1, hostname2}}),
						},
					}
					Ω(messages).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when a hostname has been added", func() {
				BeforeEach(func() {
					existingEntry = &routing_table.RoutableEndpoints{
						Hostnames: map[string]struct{}{hostname1: struct{}{}},
						Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
					}

					newEntry = &routing_table.RoutableEndpoints{
						Hostnames: map[string]struct{}{hostname1: struct{}{}, hostname2: struct{}{}},
						Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
					}
				})

				It("emits nothing", func() {
					Ω(messages).Should(BeZero())
				})
			})

			Context("when a hostname has not changed", func() {
				BeforeEach(func() {
					existingEntry = &routing_table.RoutableEndpoints{
						Hostnames: map[string]struct{}{hostname1: struct{}{}},
						Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
					}

					newEntry = existingEntry
				})

				It("emits nothing", func() {
					Ω(messages).Should(BeZero())
				})
			})
		})

		Context("when endpoints change", func() {
			Context("when an endpoint is removed", func() {
				BeforeEach(func() {
					existingEntry = &routing_table.RoutableEndpoints{
						Hostnames: map[string]struct{}{hostname1: struct{}{}, hostname2: struct{}{}},
						Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
					}

					newEntry = &routing_table.RoutableEndpoints{
						Hostnames: map[string]struct{}{hostname1: struct{}{}, hostname2: struct{}{}},
						Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{}),
					}
				})

				It("emits an unregistration", func() {
					expected := routing_table.MessagesToEmit{
						UnregistrationMessages: []routing_table.RegistryMessage{
							routing_table.RegistryMessageFor(endpoint1, routing_table.Routes{Hostnames: []string{hostname1, hostname2}}),
						},
					}
					Ω(messages).Should(MatchMessagesToEmit(expected))
				})
			})

			Context("when an endpoint has been added", func() {
				BeforeEach(func() {
					existingEntry = &routing_table.RoutableEndpoints{
						Hostnames: map[string]struct{}{hostname1: struct{}{}, hostname2: struct{}{}},
						Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
					}

					newEntry = &routing_table.RoutableEndpoints{
						Hostnames: map[string]struct{}{hostname1: struct{}{}, hostname2: struct{}{}},
						Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1, endpoint2}),
					}
				})

				It("emits nothing", func() {
					Ω(messages).Should(BeZero())
				})
			})

			Context("when endpoints have not changed", func() {
				BeforeEach(func() {
					existingEntry = &routing_table.RoutableEndpoints{
						Hostnames: map[string]struct{}{hostname1: struct{}{}},
						Endpoints: routing_table.EndpointsAsMap([]routing_table.Endpoint{endpoint1}),
					}

					newEntry = existingEntry
				})

				It("emits nothing", func() {
					Ω(messages).Should(BeZero())
				})
			})
		})

	})
})
