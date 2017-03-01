package emitter

import (
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/route-emitter/routing_table/schema/endpoint"
	"code.cloudfoundry.org/route-emitter/routing_table/schema/event"
	"code.cloudfoundry.org/routing-api"
	"code.cloudfoundry.org/routing-api/models"
	uaaclient "code.cloudfoundry.org/uaa-go-client"
)

//go:generate counterfeiter -o fakes/fake_tcp_emitter.go . Emitter
type Emitter interface {
	Emit(routingEvents event.RoutingEvents) error
}

type TCPEmitter struct {
	logger           lager.Logger
	routingAPIClient routing_api.Client
	ttl              int
	// uaaClient        uaaclient.Client
}

//TODO: we need the uaaClient in the future
func NewTCPEmitter(logger lager.Logger, routingAPIClient routing_api.Client, uaaClient uaaclient.Client, routeTTL int) Emitter {
	return &TCPEmitter{
		logger:           logger,
		routingAPIClient: routingAPIClient,
		ttl:              routeTTL,
		// uaaClient:        uaaClient,
	}
}

func (t *TCPEmitter) Emit(tcpEvents event.RoutingEvents) error {
	t.logRoutingEvents(tcpEvents)
	defer t.logger.Debug("complete-emit")

	registrationMappingRequests, unregistrationMappingRequests := tcpEvents.ToMappingRequests(t.logger, t.ttl)
	// useCachedToken := true
	// for count := 0; count < 2; count++ {
	// 	token, err := t.uaaClient.FetchToken(!useCachedToken)
	// 	if err != nil {
	// 		t.logger.Error("unable-to-get-token", err)
	// 		return err
	// 	}
	// 	t.routingAPIClient.SetToken(token.AccessToken)
	t.emit(registrationMappingRequests, unregistrationMappingRequests)
	// if err != nil && err.Error() == "unauthorized" {
	// 	useCachedToken = false
	// 	t.logger.Info("retrying-emit")
	// } else {
	// 	break
	// }
	// }

	return nil
}

func (t *TCPEmitter) emit(registrationMappingRequests, unregistrationMappingRequests []models.TcpRouteMapping) error {
	emitted := true
	if len(registrationMappingRequests) > 0 {
		if err := t.routingAPIClient.UpsertTcpRouteMappings(registrationMappingRequests); err != nil {
			emitted = false
			t.logger.Error("unable-to-upsert", err)
			return err
		}
		t.logger.Debug("successfully-emitted-registration-events",
			lager.Data{"number-of-registration-events": len(registrationMappingRequests)})

	}

	if len(unregistrationMappingRequests) > 0 {
		if err := t.routingAPIClient.DeleteTcpRouteMappings(unregistrationMappingRequests); err != nil {
			emitted = false
			t.logger.Error("unable-to-delete", err)
			return err
		}
		t.logger.Debug("successfully-emitted-unregistration-events",
			lager.Data{"number-of-unregistration-events": len(unregistrationMappingRequests)})

	}

	if emitted {
		t.logger.Debug("successfully-emitted-events")
	}
	return nil
}

func (t *TCPEmitter) logRoutingEvents(routingEvents event.RoutingEvents) {
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
