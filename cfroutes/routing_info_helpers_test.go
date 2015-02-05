package cfroutes_test

import (
	"encoding/json"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("RoutingInfoHelpers", func() {
	var (
		route1 cfroutes.CFRoute
		route2 cfroutes.CFRoute
		route3 cfroutes.CFRoute

		routes cfroutes.CFRoutes
	)

	BeforeEach(func() {
		route1 = cfroutes.CFRoute{
			Hostnames: []string{"foo1.example.com", "bar1.examaple.com"},
			Port:      11111,
		}
		route2 = cfroutes.CFRoute{
			Hostnames: []string{"foo2.example.com", "bar2.examaple.com"},
			Port:      22222,
		}
		route3 = cfroutes.CFRoute{
			Hostnames: []string{"foo3.example.com", "bar3.examaple.com"},
			Port:      33333,
		}

		routes = cfroutes.CFRoutes{route1, route2, route3}
	})

	Describe("RoutingInfo", func() {
		var routingInfo receptor.RoutingInfo

		JustBeforeEach(func() {
			routingInfo = routes.RoutingInfo()
		})

		It("wraps the serialized routes with the correct key", func() {
			expectedBytes, err := json.Marshal(routes)
			Ω(err).ShouldNot(HaveOccurred())

			payload, err := routingInfo[cfroutes.CF_ROUTER].MarshalJSON()
			Ω(err).ShouldNot(HaveOccurred())

			Ω(payload).Should(MatchJSON(expectedBytes))
		})

		Context("when CFRoutes is empty", func() {
			BeforeEach(func() {
				routes = cfroutes.CFRoutes{}
			})

			It("marshals an empty list", func() {
				payload, err := routingInfo[cfroutes.CF_ROUTER].MarshalJSON()
				Ω(err).ShouldNot(HaveOccurred())

				Ω(payload).Should(MatchJSON(`[]`))
			})
		})
	})

	Describe("CFRoutesFromRoutingInfo", func() {
		var (
			routesResult    cfroutes.CFRoutes
			conversionError error

			routingInfo receptor.RoutingInfo
		)

		JustBeforeEach(func() {
			routesResult, conversionError = cfroutes.CFRoutesFromRoutingInfo(routingInfo)
		})

		Context("when CF routes are present in the routing info", func() {
			BeforeEach(func() {
				routingInfo = routes.RoutingInfo()
			})

			It("returns the routes", func() {
				Ω(routes).Should(Equal(routesResult))
			})

			Context("when the CF routes are nil", func() {
				BeforeEach(func() {
					routingInfo = receptor.RoutingInfo{cfroutes.CF_ROUTER: nil}
				})

				It("returns nil routes", func() {
					Ω(conversionError).ShouldNot(HaveOccurred())
					Ω(routesResult).Should(BeNil())
				})
			})
		})

		Context("when CF routes are not present in the routing info", func() {
			BeforeEach(func() {
				routingInfo = receptor.RoutingInfo{}
			})

			It("returns nil routes", func() {
				Ω(conversionError).ShouldNot(HaveOccurred())
				Ω(routesResult).Should(BeNil())
			})
		})

		Context("when the routing info is nil", func() {
			BeforeEach(func() {
				routingInfo = nil
			})

			It("returns nil routes", func() {
				Ω(conversionError).ShouldNot(HaveOccurred())
				Ω(routesResult).Should(BeNil())
			})
		})
	})
})
