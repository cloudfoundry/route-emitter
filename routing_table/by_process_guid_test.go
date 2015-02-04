package routing_table_test

import (
	"github.com/cloudfoundry-incubator/receptor"
	. "github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/runtime-schema/cc_messages"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ByProcessGuid", func() {
	Describe("RoutesByProcessGuidFromDesireds", func() {
		It("should build a map of routes", func() {
			abcRoutes := cc_messages.NewRoutingInfo([]string{"foo.com", "bar.com"}, 8080)
			defRoutes := cc_messages.NewRoutingInfo([]string{"baz.com"}, 8080)

			routes := RoutesByProcessGuidFromDesireds([]receptor.DesiredLRPResponse{
				{Domain: "tests", ProcessGuid: "abc", Routes: abcRoutes, LogGuid: "abc-guid"},
				{Domain: "tests", ProcessGuid: "def", Routes: defRoutes, LogGuid: "def-guid"},
			})

			Ω(routes).Should(HaveLen(2))
			Ω(routes["abc"].URIs).Should(Equal([]string{"foo.com", "bar.com"}))
			Ω(routes["def"].URIs).Should(Equal([]string{"baz.com"}))
			Ω(routes["abc"].LogGuid).Should(Equal("abc-guid"))
			Ω(routes["def"].LogGuid).Should(Equal("def-guid"))
		})

		Context("when the routing info is nil", func() {
			It("should return an empty hostname list", func() {
				routes := RoutesByProcessGuidFromDesireds([]receptor.DesiredLRPResponse{
					{Domain: "tests", ProcessGuid: "abc", Routes: nil, LogGuid: "abc-guid"},
				})
				Ω(routes).Should(HaveLen(1))
				Ω(routes["abc"].URIs).Should(BeNil())
			})
		})
	})

	Describe("ContainersByProcessGuidFromActuals", func() {
		It("should build a map of containers, ignoring those without ports", func() {
			containers := ContainersByProcessGuidFromActuals([]receptor.ActualLRPResponse{
				{ProcessGuid: "abc", Index: 1, Domain: "domain", Address: "1.1.1.1", Ports: []receptor.PortMapping{{HostPort: 11}}},
				{ProcessGuid: "abc", Index: 1, Domain: "domain", Address: "2.2.2.2", Ports: []receptor.PortMapping{{HostPort: 22}}},
				{ProcessGuid: "def", Index: 1, Domain: "domain", Address: "3.3.3.3", Ports: []receptor.PortMapping{{HostPort: 33}}},
				{ProcessGuid: "def", Index: 1, Domain: "domain", Address: "4.4.4.4", Ports: nil},
			})

			Ω(containers).Should(HaveLen(2))
			Ω(containers["abc"]).Should(HaveLen(2))
			Ω(containers["abc"]).Should(ContainElement(Container{Host: "1.1.1.1", Port: 11}))
			Ω(containers["abc"]).Should(ContainElement(Container{Host: "2.2.2.2", Port: 22}))
			Ω(containers["def"]).Should(HaveLen(1))
			Ω(containers["def"]).Should(ContainElement(Container{Host: "3.3.3.3", Port: 33}))
		})
	})
})
