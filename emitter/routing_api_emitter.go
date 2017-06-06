package emitter

import (
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/endpoint"
	"code.cloudfoundry.org/route-emitter/routingtable/schema/event"
	"code.cloudfoundry.org/routing-api"
	"code.cloudfoundry.org/routing-api/models"
	uaaclient "code.cloudfoundry.org/uaa-go-client"
)

//go:generate counterfeiter -o fakes/fake_routing_api_emitter.go . RoutingAPIEmitter
type RoutingAPIEmitter interface {
	Emit(routingEvents event.RoutingEvents) (int, int, error)
}

type routingAPIEmitter struct {
	logger              lager.Logger
	routingAPIClient    routing_api.Client
	ttl                 int
	uaaClient           uaaclient.Client
	directInstanceRoute bool
}

func NewRoutingAPIEmitter(logger lager.Logger, routingAPIClient routing_api.Client, uaaClient uaaclient.Client, routeTTL int, directInstanceRoute bool) RoutingAPIEmitter {
	return &routingAPIEmitter{
		logger:              logger,
		routingAPIClient:    routingAPIClient,
		ttl:                 routeTTL,
		uaaClient:           uaaClient,
		directInstanceRoute: directInstanceRoute,
	}
}

func (t *routingAPIEmitter) Emit(tcpEvents event.RoutingEvents) (int, int, error) {
	t.logRoutingEvents(tcpEvents)
	defer t.logger.Debug("complete-emit")

	registrationMappingRequests, unregistrationMappingRequests := tcpEvents.ToMappingRequests(t.logger, t.ttl, t.directInstanceRoute)
	err := t.emit(registrationMappingRequests, unregistrationMappingRequests)
	if err != nil {
		return 0, 0, err
	}

	return len(registrationMappingRequests), len(unregistrationMappingRequests), nil
}

func (t *routingAPIEmitter) emit(registrationMappingRequests, unregistrationMappingRequests []models.TcpRouteMapping) error {
	var forceUpdate bool

	for count := 0; count < 2; count++ {
		forceUpdate = count > 0
		token, err := t.uaaClient.FetchToken(forceUpdate)
		if err != nil {
			return err
		}

		t.routingAPIClient.SetToken(token.AccessToken)

		err = t.emitRoutingAPI(registrationMappingRequests, unregistrationMappingRequests)
		if err != nil && count > 0 {
			return err
		} else if err == nil {
			break
		}
	}

	t.logger.Debug("successfully-emitted-events")
	return nil
}

func (t *routingAPIEmitter) emitRoutingAPI(regMsgs, unregMsgs []models.TcpRouteMapping) error {
	if len(regMsgs) > 0 {
		if err := t.routingAPIClient.UpsertTcpRouteMappings(regMsgs); err != nil {
			t.logger.Error("unable-to-upsert", err)
			return err
		}
		t.logger.Debug("successfully-emitted-registration-events",
			lager.Data{"number-of-registration-events": len(regMsgs)})
	}

	if len(unregMsgs) > 0 {
		if err := t.routingAPIClient.DeleteTcpRouteMappings(unregMsgs); err != nil {
			t.logger.Error("unable-to-delete", err)
			return err
		}
		t.logger.Debug("successfully-emitted-unregistration-events",
			lager.Data{"number-of-unregistration-events": len(unregMsgs)})
	}
	return nil
}

func (t *routingAPIEmitter) logRoutingEvents(routingEvents event.RoutingEvents) {
	for _, event := range routingEvents {
		endpoints := make([]endpoint.Endpoint, 0)
		for _, endpoint := range event.Entry.Endpoints {
			endpoints = append(endpoints, endpoint)
		}

		ports := make([]uint32, 0)
		for _, extEndpoint := range event.Entry.ExternalEndpoints {
			ports = append(ports, extEndpoint.Port)
		}
		t.logger.Info("mapped-routes", lager.Data{
			"external_ports": ports,
			"backends":       endpoints})
	}
}
