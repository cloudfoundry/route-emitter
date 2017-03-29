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
	fakeuaa "code.cloudfoundry.org/uaa-go-client/fakes"
	"code.cloudfoundry.org/uaa-go-client/schema"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("RoutingAPIEmitter", func() {

	var (
		routingApiClient        *fake_routing_api.FakeClient
		uaaClient               *fakeuaa.FakeClient
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
		uaaClient = &fakeuaa.FakeClient{}
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

		token := &schema.Token{
			AccessToken: "accesstoken",
		}
		uaaClient.FetchTokenReturns(token, nil)
	})

	It("fetches a client token from UAA", func() {
		err := routingAPIEmitter.Emit(routingEvents)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(uaaClient.FetchTokenCallCount()).To(Equal(1))
	})

	It("uses a cached token if available", func() {
		err := routingAPIEmitter.Emit(routingEvents)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(uaaClient.FetchTokenCallCount()).To(Equal(1))
		Expect(uaaClient.FetchTokenArgsForCall(0)).To(BeFalse())
	})

	It("authorizes the routing API call with its bearer token", func() {
		err := routingAPIEmitter.Emit(routingEvents)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(routingApiClient.SetTokenCallCount()).To(Equal(1))
	})

	Context("when UAA communication fails", func() {
		BeforeEach(func() {
			uaaClient.FetchTokenReturns(nil, errors.New("blam"))
		})

		It("returns an error and emits nothing", func() {
			err := routingAPIEmitter.Emit(routingEvents)
			Expect(err).Should(HaveOccurred())
			Expect(routingApiClient.UpsertTcpRouteMappingsCallCount()).To(Equal(0))
		})
	})

	Context("when valid routing events are provided", func() {

		Context("when router API returns no errors", func() {

			Context("and there are registration events", func() {
				It("emits valid upsert tcp routes", func() {
					err := routingAPIEmitter.Emit(routingEvents)
					Expect(err).ShouldNot(HaveOccurred())
					Expect(routingApiClient.UpsertTcpRouteMappingsCallCount()).To(Equal(1))
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
					mappingRequests := routingApiClient.DeleteTcpRouteMappingsArgsForCall(0)
					Expect(mappingRequests).To(ConsistOf(expectedMappingRequests))
				})
			})
		})

		Context("when routing API Upsert returns an error", func() {
			BeforeEach(func() {
				routingApiClient.UpsertTcpRouteMappingsReturns(errors.New("unauthorized"))
			})

			It("retries once and logs the error", func() {
				err := routingAPIEmitter.Emit(routingEvents)
				Expect(err).To(HaveOccurred())
				Expect(logger).To(gbytes.Say("test.unable-to-upsert.*unauthorized"))
			})

			It("refreshes the cached UAA token", func() {
				err := routingAPIEmitter.Emit(routingEvents)
				Expect(err).To(HaveOccurred())

				Expect(uaaClient.FetchTokenCallCount()).To(Equal(2))
				Expect(uaaClient.FetchTokenArgsForCall(1)).To(BeTrue())
			})

			Context("when refreshing the cached token authorizes the emitter", func() {
				BeforeEach(func() {
					var count uint
					routingApiClient.UpsertTcpRouteMappingsStub = func([]apimodels.TcpRouteMapping) error {
						defer func() {
							count += 1
						}()
						if count > 0 {
							return nil
						}

						return errors.New("unauthorized")
					}
				})

				It("succeeds in emitting routes", func() {
					err := routingAPIEmitter.Emit(routingEvents)
					Expect(err).ToNot(HaveOccurred())

					Expect(uaaClient.FetchTokenCallCount()).To(Equal(2))
					Expect(routingApiClient.UpsertTcpRouteMappingsCallCount()).To(Equal(2))
				})
			})
		})

		Context("when routing API Delete returns an error", func() {
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
				Expect(err).To(HaveOccurred())
				Expect(logger).To(gbytes.Say("test.unable-to-delete.*unauthorized"))
			})

			It("refreshes the cached UAA token", func() {
				err := routingAPIEmitter.Emit(routingEvents)
				Expect(err).To(HaveOccurred())

				Expect(uaaClient.FetchTokenCallCount()).To(Equal(2))
				Expect(uaaClient.FetchTokenArgsForCall(1)).To(BeTrue())
			})

			Context("when refreshing the cached token authorizes the emitter", func() {
				BeforeEach(func() {
					var count uint
					routingApiClient.DeleteTcpRouteMappingsStub = func([]apimodels.TcpRouteMapping) error {
						defer func() {
							count += 1
						}()
						if count > 0 {
							return nil
						}

						return errors.New("unauthorized")
					}
				})

				It("succeeds in emitting routes", func() {
					err := routingAPIEmitter.Emit(routingEvents)
					Expect(err).ToNot(HaveOccurred())

					Expect(uaaClient.FetchTokenCallCount()).To(Equal(2))
					Expect(routingApiClient.DeleteTcpRouteMappingsCallCount()).To(Equal(2))
				})
			})
		})
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
			Expect(err).NotTo(HaveOccurred())
			Expect(logger).To(gbytes.Say("test.invalid-routing-event"))
		})
	})
})
