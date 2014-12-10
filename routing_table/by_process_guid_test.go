package routing_table_test

import (
	. "github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/runtime-schema/models"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ByProcessGuid", func() {
	Describe("RoutesByProcessGuidFromDesireds", func() {
		It("should build a map of routes", func() {
			routes := RoutesByProcessGuidFromDesireds([]models.DesiredLRP{
				{Domain: "tests", ProcessGuid: "abc", Routes: []string{"foo.com", "bar.com"}, LogGuid: "abc-guid"},
				{Domain: "tests", ProcessGuid: "def", Routes: []string{"baz.com"}, LogGuid: "def-guid"},
			})

			Ω(routes).Should(HaveLen(2))
			Ω(routes["abc"].URIs).Should(Equal([]string{"foo.com", "bar.com"}))
			Ω(routes["def"].URIs).Should(Equal([]string{"baz.com"}))
			Ω(routes["abc"].LogGuid).Should(Equal("abc-guid"))
			Ω(routes["def"].LogGuid).Should(Equal("def-guid"))
		})
	})

	Describe("ContainersByProcessGuidFromActuals", func() {
		It("should build a map of containers, ignoring those without ports", func() {
			containers := ContainersByProcessGuidFromActuals([]models.ActualLRP{
				{ProcessGuid: "abc", Host: "1.1.1.1", Ports: []models.PortMapping{{HostPort: 11}}},
				{ProcessGuid: "abc", Host: "2.2.2.2", Ports: []models.PortMapping{{HostPort: 22}}},
				{ProcessGuid: "def", Host: "3.3.3.3", Ports: []models.PortMapping{{HostPort: 33}}},
				{ProcessGuid: "def", Host: "4.4.4.4"},
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
