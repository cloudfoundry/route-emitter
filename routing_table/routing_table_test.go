package routing_table_test

import (
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/bbs/models/test/model_helpers"
	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/route-emitter/routing_table"
	"code.cloudfoundry.org/routing-info/cfroutes"
	"code.cloudfoundry.org/routing-info/tcp_routes"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = FDescribe("RoutingTable", func() {
	var (
		table                    routing_table.RoutingTable
		tcpAndHTTPSchedulingInfo models.DesiredLRPSchedulingInfo
	)

	BeforeEach(func() {
		httpRoute := cfroutes.CFRoutes{
			{
				Hostnames:       []string{"hostname-1", "hostname-2"},
				Port:            1234,
				RouteServiceUrl: "https://example.com",
			},
		}.RoutingInfo()
		tcpRoute := tcp_routes.TCPRoutes{
			{
				RouterGroupGuid: "router-group-guid",
				ExternalPort:    5524,
				ContainerPort:   60000,
			},
		}.RoutingInfo()

		tcpAndHTTPRoutes := tcpRoute
		(*tcpAndHTTPRoutes)[cfroutes.CF_ROUTER] = httpRoute[cfroutes.CF_ROUTER]

		desiredLRP := model_helpers.NewValidDesiredLRP("test-guid")
		desiredLRP.Routes = tcpAndHTTPRoutes
		tcpAndHTTPSchedulingInfo = desiredLRP.DesiredLRPSchedulingInfo()

		logger := lagertest.NewTestLogger("routing-table")
		table = routing_table.NewRoutingTable(logger)
	})

	Describe("AddRoutes", func() {
		Context("when the table is empty", func() {
			It("emits nothing", func() {
				events := table.AddRoutes(tcpAndHTTPSchedulingInfo)
				Expect(events).To(BeZero())
			})
		})
	})
})
