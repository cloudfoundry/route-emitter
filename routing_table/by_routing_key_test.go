package routing_table_test

import (
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ByRoutingKey", func() {
	Describe("RoutesByRoutingKeyFromDesireds", func() {
		It("should build a map of routes", func() {
			abcRoutes := cfroutes.CFRoutes{
				{Hostnames: []string{"foo.com", "bar.com"}, Port: 8080},
				{Hostnames: []string{"foo.example.com"}, Port: 9090},
			}
			defRoutes := cfroutes.CFRoutes{
				{Hostnames: []string{"baz.com"}, Port: 8080},
			}

			routes := routing_table.RoutesByRoutingKeyFromDesireds([]receptor.DesiredLRPResponse{
				{Domain: "tests", ProcessGuid: "abc", Routes: abcRoutes.RoutingInfo(), LogGuid: "abc-guid"},
				{Domain: "tests", ProcessGuid: "def", Routes: defRoutes.RoutingInfo(), LogGuid: "def-guid"},
			})

			Ω(routes).Should(HaveLen(3))
			Ω(routes[routing_table.RoutingKey{ProcessGuid: "abc", ContainerPort: 8080}].Hostnames).Should(Equal([]string{"foo.com", "bar.com"}))
			Ω(routes[routing_table.RoutingKey{ProcessGuid: "abc", ContainerPort: 8080}].LogGuid).Should(Equal("abc-guid"))

			Ω(routes[routing_table.RoutingKey{ProcessGuid: "abc", ContainerPort: 9090}].Hostnames).Should(Equal([]string{"foo.example.com"}))
			Ω(routes[routing_table.RoutingKey{ProcessGuid: "abc", ContainerPort: 9090}].LogGuid).Should(Equal("abc-guid"))

			Ω(routes[routing_table.RoutingKey{ProcessGuid: "def", ContainerPort: 8080}].Hostnames).Should(Equal([]string{"baz.com"}))
			Ω(routes[routing_table.RoutingKey{ProcessGuid: "def", ContainerPort: 8080}].LogGuid).Should(Equal("def-guid"))
		})

		Context("when the routing info is nil", func() {
			It("should not be included in the results", func() {
				routes := routing_table.RoutesByRoutingKeyFromDesireds([]receptor.DesiredLRPResponse{
					{Domain: "tests", ProcessGuid: "abc", Routes: nil, LogGuid: "abc-guid"},
				})
				Ω(routes).Should(HaveLen(0))
			})
		})
	})

	Describe("EndpointsByRoutingKeyFromActuals", func() {
		It("should build a map of endpoints, ignoring those without ports", func() {
			endpoints := routing_table.EndpointsByRoutingKeyFromActuals([]receptor.ActualLRPResponse{
				{ProcessGuid: "abc", Index: 0, Domain: "domain", Address: "1.1.1.1", Ports: []receptor.PortMapping{
					{HostPort: 11, ContainerPort: 44},
					{HostPort: 66, ContainerPort: 99},
				}},
				{ProcessGuid: "abc", Index: 1, Domain: "domain", Address: "2.2.2.2", Ports: []receptor.PortMapping{
					{HostPort: 22, ContainerPort: 44},
					{HostPort: 88, ContainerPort: 99},
				}},
				{ProcessGuid: "def", Index: 0, Domain: "domain", Address: "3.3.3.3", Ports: []receptor.PortMapping{
					{HostPort: 33, ContainerPort: 55},
				}},
				{ProcessGuid: "def", Index: 1, Domain: "domain", Address: "4.4.4.4", Ports: nil},
			})

			Ω(endpoints).Should(HaveLen(3))
			Ω(endpoints[routing_table.RoutingKey{ProcessGuid: "abc", ContainerPort: 44}]).Should(HaveLen(2))
			Ω(endpoints[routing_table.RoutingKey{ProcessGuid: "abc", ContainerPort: 44}]).Should(ContainElement(routing_table.Endpoint{Host: "1.1.1.1", Port: 11, ContainerPort: 44}))
			Ω(endpoints[routing_table.RoutingKey{ProcessGuid: "abc", ContainerPort: 44}]).Should(ContainElement(routing_table.Endpoint{Host: "2.2.2.2", Port: 22, ContainerPort: 44}))

			Ω(endpoints[routing_table.RoutingKey{ProcessGuid: "abc", ContainerPort: 99}]).Should(HaveLen(2))
			Ω(endpoints[routing_table.RoutingKey{ProcessGuid: "abc", ContainerPort: 99}]).Should(ContainElement(routing_table.Endpoint{Host: "1.1.1.1", Port: 66, ContainerPort: 99}))
			Ω(endpoints[routing_table.RoutingKey{ProcessGuid: "abc", ContainerPort: 99}]).Should(ContainElement(routing_table.Endpoint{Host: "2.2.2.2", Port: 88, ContainerPort: 99}))

			Ω(endpoints[routing_table.RoutingKey{ProcessGuid: "def", ContainerPort: 55}]).Should(HaveLen(1))
			Ω(endpoints[routing_table.RoutingKey{ProcessGuid: "def", ContainerPort: 55}]).Should(ContainElement(routing_table.Endpoint{Host: "3.3.3.3", Port: 33, ContainerPort: 55}))
		})
	})

	Describe("EndpointsFromActual", func() {
		It("builds a map of container port to endpoint", func() {
			endpoints, err := routing_table.EndpointsFromActual(receptor.ActualLRPResponse{
				ProcessGuid:  "process-guid",
				InstanceGuid: "instance-guid",
				Index:        0,
				Domain:       "domain",
				Address:      "1.1.1.1",
				Ports: []receptor.PortMapping{
					{HostPort: 11, ContainerPort: 44},
					{HostPort: 66, ContainerPort: 99},
				},
				Evacuating: true,
			})
			Ω(err).ShouldNot(HaveOccurred())

			Ω(endpoints).Should(ConsistOf([]routing_table.Endpoint{
				routing_table.Endpoint{Host: "1.1.1.1", Port: 11, InstanceGuid: "instance-guid", ContainerPort: 44, Evacuating: true},
				routing_table.Endpoint{Host: "1.1.1.1", Port: 66, InstanceGuid: "instance-guid", ContainerPort: 99, Evacuating: true},
			}))
		})
	})

	Describe("RoutingKeysFromActual", func() {
		It("creates a list of keys for an actual LRP", func() {
			keys := routing_table.RoutingKeysFromActual(receptor.ActualLRPResponse{
				ProcessGuid:  "process-guid",
				InstanceGuid: "instance-guid",
				Index:        0,
				Domain:       "domain",
				Address:      "1.1.1.1",
				Ports: []receptor.PortMapping{
					{HostPort: 11, ContainerPort: 44},
					{HostPort: 66, ContainerPort: 99},
				},
			})

			Ω(keys).Should(HaveLen(2))
			Ω(keys).Should(ContainElement(routing_table.RoutingKey{ProcessGuid: "process-guid", ContainerPort: 44}))
			Ω(keys).Should(ContainElement(routing_table.RoutingKey{ProcessGuid: "process-guid", ContainerPort: 99}))
		})

		Context("when the actual lrp has no port mappings", func() {
			It("returns no keys", func() {
				keys := routing_table.RoutingKeysFromActual(receptor.ActualLRPResponse{
					ProcessGuid:  "process-guid",
					InstanceGuid: "instance-guid",
					Index:        0,
					Domain:       "domain",
					Address:      "1.1.1.1",
				})

				Ω(keys).Should(HaveLen(0))
			})
		})
	})

	Describe("RoutingKeysFromDesired", func() {
		It("creates a list of keys for an actual LRP", func() {
			routes := cfroutes.CFRoutes{
				{Hostnames: []string{"foo.com", "bar.com"}, Port: 8080},
				{Hostnames: []string{"foo.example.com"}, Port: 9090},
			}

			desired := receptor.DesiredLRPResponse{
				Domain:      "tests",
				ProcessGuid: "process-guid",
				Ports:       []uint16{8080, 9090},
				Routes:      routes.RoutingInfo(),
				LogGuid:     "abc-guid",
			}

			keys := routing_table.RoutingKeysFromDesired(desired)

			Ω(keys).Should(HaveLen(2))
			Ω(keys).Should(ContainElement(routing_table.RoutingKey{ProcessGuid: "process-guid", ContainerPort: 8080}))
			Ω(keys).Should(ContainElement(routing_table.RoutingKey{ProcessGuid: "process-guid", ContainerPort: 9090}))
		})

		Context("when the desired LRP does not define any container ports", func() {
			It("returns no keys", func() {
				desired := receptor.DesiredLRPResponse{
					Domain:      "tests",
					ProcessGuid: "process-guid",
					Routes:      cfroutes.CFRoutes{{Hostnames: []string{"foo.com", "bar.com"}, Port: 8080}}.RoutingInfo(),
					LogGuid:     "abc-guid",
				}

				keys := routing_table.RoutingKeysFromDesired(desired)
				Ω(keys).Should(HaveLen(0))
			})
		})
	})
})
