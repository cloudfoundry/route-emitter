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
			lrpKey1 := models.NewActualLRPKey("abc", 1, "domain")
			lrpKey2 := models.NewActualLRPKey("def", 1, "domain")
			containers := ContainersByProcessGuidFromActuals([]models.ActualLRP{
				{ActualLRPKey: lrpKey1, ActualLRPNetInfo: models.NewActualLRPNetInfo("1.1.1.1", []models.PortMapping{{HostPort: 11}})},
				{ActualLRPKey: lrpKey1, ActualLRPNetInfo: models.NewActualLRPNetInfo("2.2.2.2", []models.PortMapping{{HostPort: 22}})},
				{ActualLRPKey: lrpKey2, ActualLRPNetInfo: models.NewActualLRPNetInfo("3.3.3.3", []models.PortMapping{{HostPort: 33}})},
				{ActualLRPKey: lrpKey2, ActualLRPNetInfo: models.NewActualLRPNetInfo("4.4.4.4", nil)},
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
