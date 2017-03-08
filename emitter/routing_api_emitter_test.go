package emitter_test

import (
	"errors"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/route-emitter/emitter"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/event"
	"code.cloudfoundry.org/routing-api/fake_routing_api"
	apimodels "code.cloudfoundry.org/routing-api/models"
	testUaaClient "code.cloudfoundry.org/uaa-go-client/fakes"
	uaa "code.cloudfoundry.org/uaa-go-client/schema"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("RoutingAPIEmitter", func() {

	var (
		routingApiClient        *fake_routing_api.FakeClient
		routingEvents           event.RoutingEvents
		routingKey1             endpoint.RoutingKey
		routableEndpoints1      endpoint.RoutableEndpoints
		routingAPIEmitter       emitter.RoutingAPIEmitter
		logger                  lager.Logger
		ttl                     int
		expectedMappingRequests []apimodels.TcpRouteMapping
	)

	BeforeEach(func() {
		routingApiClient = new(fake_routing_api.FakeClient)
		ttl = 60
		logger = lagertest.NewTestLogger("test")
		uaaClient := &testUaaClient.FakeClient{}
		uaaClient.FetchTokenReturns(&uaa.Token{AccessToken: "some-token", ExpiresIn: 1234}, nil)
		routingAPIEmitter = emitter.NewRoutingAPIEmitter(logger, routingApiClient, uaaClient, ttl)

		logGuid := "log-guid-1"
		modificationTag := models.ModificationTag{Epoch: "abc", Index: 0}

		endpoints1 := map[endpoint.EndpointKey]endpoint.Endpoint{
			endpoint.NewEndpointKey("instance-guid-1", false): endpoint.NewEndpoint(
				"instance-guid-1", false, "some-ip-1", 62003, 5222, &modificationTag),
		}

		extenralEndpointInfo1 := endpoint.NewExternalEndpointInfo("123", 61000)
		routableEndpoints1 = endpoint.NewRoutableEndpoints(
			endpoint.ExternalEndpointInfos{extenralEndpointInfo1}, endpoints1, logGuid, &modificationTag)

		routingKey1 = endpoint.NewRoutingKey("process-guid-1", 5222)

		routingEvents = event.RoutingEvents{
			event.RoutingEvent{
				EventType: event.RouteRegistrationEvent,
				Key:       routingKey1,
				Entry:     routableEndpoints1,
			},
		}
		expectedMappingRequests = []apimodels.TcpRouteMapping{
			apimodels.NewTcpRouteMapping("123", 61000, "some-ip-1", 62003, int(ttl)),
		}
	})

	Context("when valid routing events are provided", func() {

		Context("when router API returns no errors", func() {

			Context("and there are registration events", func() {
				It("emits valid upsert tcp routes", func() {
					err := routingAPIEmitter.Emit(routingEvents)
					Expect(err).ShouldNot(HaveOccurred())
					Expect(routingApiClient.UpsertTcpRouteMappingsCallCount()).To(Equal(1))
					// Expect(uaaClient.FetchTokenCallCount()).To(Equal(1))
					// Expect(routingApiClient.SetTokenCallCount()).To(Equal(1))
					// Expect(routingApiClient.SetTokenArgsForCall(0)).To(Equal("some-token"))
					mappingRequests := routingApiClient.UpsertTcpRouteMappingsArgsForCall(0)
					Expect(mappingRequests).To(ConsistOf(expectedMappingRequests))
				})

			})

			Context("and there are unregistration events", func() {
				BeforeEach(func() {
					routingEvents = event.RoutingEvents{
						event.RoutingEvent{
							EventType: event.RouteUnregistrationEvent,
							Key:       routingKey1,
							Entry:     routableEndpoints1,
						},
					}
				})

				It("emits valid delete tcp routes", func() {
					err := routingAPIEmitter.Emit(routingEvents)
					Expect(err).ShouldNot(HaveOccurred())
					Expect(routingApiClient.DeleteTcpRouteMappingsCallCount()).To(Equal(1))
					// Expect(uaaClient.FetchTokenCallCount()).To(Equal(1))
					// Expect(routingApiClient.SetTokenCallCount()).To(Equal(1))
					// Expect(routingApiClient.SetTokenArgsForCall(0)).To(Equal("some-token"))
					mappingRequests := routingApiClient.DeleteTcpRouteMappingsArgsForCall(0)
					Expect(mappingRequests).To(ConsistOf(expectedMappingRequests))
				})
			})
		})
		Context("when routing API Upsert returns an error other than unauthorized", func() {
			BeforeEach(func() {
				routingApiClient.UpsertTcpRouteMappingsReturns(errors.New("kabooom"))
			})

			It("logs the error", func() {
				err := routingAPIEmitter.Emit(routingEvents)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(logger).To(gbytes.Say("test.unable-to-upsert.*kabooom"))
			})
		})

		Context("when routing API Upsert returns an unauthorized error", func() {
			BeforeEach(func() {
				routingApiClient.UpsertTcpRouteMappingsReturns(errors.New("unauthorized"))
			})

			It("retries once and logs the error", func() {
				err := routingAPIEmitter.Emit(routingEvents)
				Expect(err).ShouldNot(HaveOccurred())
				// Expect(routingApiClient.UpsertTcpRouteMappingsCallCount()).To(Equal(2))
				// Expect(uaaClient.FetchTokenCallCount()).To(Equal(2))
				// Expect(routingApiClient.SetTokenCallCount()).To(Equal(2))
				Expect(logger).To(gbytes.Say("test.unable-to-upsert.*unauthorized"))
			})
		})

		Context("when routing API Delete returns an error other than unauthorized", func() {
			BeforeEach(func() {
				routingApiClient.DeleteTcpRouteMappingsReturns(errors.New("kabooom"))
				routingEvents = event.RoutingEvents{
					event.RoutingEvent{
						EventType: event.RouteUnregistrationEvent,
						Key:       routingKey1,
						Entry:     routableEndpoints1,
					},
				}
			})

			It("logs error", func() {
				err := routingAPIEmitter.Emit(routingEvents)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(logger).To(gbytes.Say("test.unable-to-delete.*kabooom"))
			})
		})

		Context("when routing API Delete returns an error other than unauthorized", func() {
			BeforeEach(func() {
				routingApiClient.DeleteTcpRouteMappingsReturns(errors.New("unauthorized"))
				routingEvents = event.RoutingEvents{
					event.RoutingEvent{
						EventType: event.RouteUnregistrationEvent,
						Key:       routingKey1,
						Entry:     routableEndpoints1,
					},
				}
			})

			It("logs error", func() {
				err := routingAPIEmitter.Emit(routingEvents)
				Expect(err).ShouldNot(HaveOccurred())
				// Expect(routingApiClient.DeleteTcpRouteMappingsCallCount()).To(Equal(2))
				// Expect(uaaClient.FetchTokenCallCount()).To(Equal(2))
				// Expect(routingApiClient.SetTokenCallCount()).To(Equal(2))
				Expect(logger).To(gbytes.Say("test.unable-to-delete.*unauthorized"))
			})
		})

		// Context("when token fetcher returns error", func() {
		// 	BeforeEach(func() {
		// 		uaaClient.FetchTokenReturns(nil, errors.New("kabooom"))
		// 	})

		// 	It("returns error", func() {
		// 		err := emitter.Emit(routingEvents)
		// 		Expect(err).Should(HaveOccurred())
		// 		Expect(err.Error()).Should(Equal("kabooom"))
		// 		Expect(logger).To(gbytes.Say("test.unable-to-get-token"))
		// 	})
		// })
	})

	Context("when invalid routing events are provided", func() {
		BeforeEach(func() {
			logGuid := "log-guid-1"
			modificationTag := models.ModificationTag{Epoch: "abc", Index: 0}

			endpoints1 := map[endpoint.EndpointKey]endpoint.Endpoint{
				endpoint.NewEndpointKey("instance-guid-1", false): endpoint.NewEndpoint(
					"instance-guid-1", false, "some-ip-1", 62003, 5222, &modificationTag),
			}

			routingKey1 := endpoint.NewRoutingKey("process-guid-1", 5222)

			extenralEndpointInfo1 := endpoint.NewExternalEndpointInfo("123", 0)

			routableEndpoints1 := endpoint.NewRoutableEndpoints(
				endpoint.ExternalEndpointInfos{extenralEndpointInfo1}, endpoints1, logGuid, &modificationTag)

			routingEvents = event.RoutingEvents{
				event.RoutingEvent{
					EventType: event.RouteRegistrationEvent,
					Key:       routingKey1,
					Entry:     routableEndpoints1,
				},
			}
		})

		It("returns \"Unable to build mapping request\" error", func() {
			err := routingAPIEmitter.Emit(routingEvents)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(logger).To(gbytes.Say("test.invalid-routing-event"))
		})
	})
})
