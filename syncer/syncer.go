package syncer

import (
	"encoding/json"
	"os"
	"time"

	"github.com/apcera/nats"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/runtime-schema/metric"
	"github.com/cloudfoundry/gunk/diegonats"
	uuid "github.com/nu7hatch/gouuid"
	"github.com/pivotal-golang/clock"
	"github.com/pivotal-golang/lager"
)

var (
	routeSyncDuration = metric.Duration("RouteEmitterSyncDuration")
)

type Syncer struct {
	receptorClient receptor.Client
	natsClient     diegonats.NATSClient
	clock          clock.Clock
	syncInterval   time.Duration
	syncEvents     SyncEvents
	routerGreet    chan time.Duration

	logger lager.Logger
}

func NewSyncer(
	receptorClient receptor.Client,
	clock clock.Clock,
	syncInterval time.Duration,
	natsClient diegonats.NATSClient,
	logger lager.Logger,
) *Syncer {
	return &Syncer{
		receptorClient: receptorClient,
		natsClient:     natsClient,

		clock:        clock,
		syncInterval: syncInterval,
		syncEvents: SyncEvents{
			Begin: make(chan SyncBegin, 1),
			End:   make(chan SyncEnd),
			Emit:  make(chan struct{}, 1),
		},

		routerGreet: make(chan time.Duration),

		logger: logger.Session("syncer"),
	}
}

func (s *Syncer) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	s.logger.Info("starting")
	replyUuid, err := uuid.NewV4()
	if err != nil {
		return err
	}

	err = s.listenForRouter(replyUuid.String())
	if err != nil {
		return err
	}

	close(ready)
	s.logger.Info("started")

	var routerPruneInterval time.Duration
	retryGreetingTicker := s.clock.NewTicker(time.Second)

	//keep trying to greet until we hear from the router
GREET_LOOP:
	for {
		s.logger.Info("greeting-router")
		err := s.greetRouter(replyUuid.String())
		if err != nil {
			s.logger.Error("failed-to-greet-router", err)
			return err
		}

		select {
		case routerPruneInterval = <-s.routerGreet:
			s.logger.Info("received-router-prune-interval", lager.Data{"interval": routerPruneInterval.String()})
			break GREET_LOOP
		case <-retryGreetingTicker.C():
		case <-signals:
			s.logger.Info("stopping")
			return nil
		}
	}
	retryGreetingTicker.Stop()

	s.sync()

	//now keep emitting at the desired interval, syncing with etcd every syncInterval
	syncTicker := s.clock.NewTicker(s.syncInterval)
	routerTicker := s.clock.NewTicker(routerPruneInterval)

	for {
		select {
		case routerPruneInterval = <-s.routerGreet:
			s.logger.Info("received-new-router-prune-interval", lager.Data{"interval": routerPruneInterval.String()})
			routerTicker.Stop()
			routerTicker = s.clock.NewTicker(routerPruneInterval)
			s.emit()
		case <-routerTicker.C():
			s.logger.Info("emitting-routes")
			s.emit()
		case <-syncTicker.C():
			s.logger.Info("syncing")
			s.sync()
		case <-signals:
			s.logger.Info("stopping")
			syncTicker.Stop()
			routerTicker.Stop()
			return nil
		}
	}

	return nil
}

func (s *Syncer) SyncEvents() SyncEvents {
	return s.syncEvents
}

func (s *Syncer) emit() {
	select {
	case s.syncEvents.Emit <- struct{}{}:
	default:
	}
}

func (s *Syncer) sync() {
	ack := make(chan struct{})
	select {
	case s.syncEvents.Begin <- SyncBegin{Ack: ack}:
	default:
		s.logger.Debug("sync-already-in-progress")
	}
	<-ack

	before := s.clock.Now()

	actualLRPResponses, err := s.receptorClient.ActualLRPs()
	if err != nil {
		s.logger.Error("failed-to-get-actual", err)
		return
	}

	desiredLRPResponses, err := s.receptorClient.DesiredLRPs()
	if err != nil {
		s.logger.Error("failed-to-get-desired", err)
		return
	}

	runningActualLRPs := make([]receptor.ActualLRPResponse, 0, len(actualLRPResponses))
	for _, actualLRPResponse := range actualLRPResponses {
		if actualLRPResponse.State == receptor.ActualLRPStateRunning {
			runningActualLRPs = append(runningActualLRPs, actualLRPResponse)
		}
	}

	desiredLRPs := make([]receptor.DesiredLRPResponse, 0, len(desiredLRPResponses))
	for _, desiredLRPResponse := range desiredLRPResponses {
		desiredLRPs = append(desiredLRPs, desiredLRPResponse)
	}

	newTable := routing_table.NewTempTable(
		routing_table.RoutesByRoutingKeyFromDesireds(desiredLRPs),
		routing_table.EndpointsByRoutingKeyFromActuals(runningActualLRPs),
	)

	s.syncEvents.End <- SyncEnd{
		Table: newTable,
		Callback: func(table routing_table.RoutingTable) {
			after := s.clock.Now()
			routeSyncDuration.Send(after.Sub(before))
		},
	}
}

func (s *Syncer) register(desired receptor.DesiredLRPResponse, actual receptor.ActualLRPResponse) error {
	routes, err := cfroutes.CFRoutesFromRoutingInfo(desired.Routes)
	if err != nil || len(routes) == 0 {
		return err
	}
	message := routing_table.RegistryMessage{
		URIs: routes[0].Hostnames,
		Host: actual.Address,
		Port: uint16(actual.Ports[0].HostPort),
	}

	payload, _ := json.Marshal(message)

	return s.natsClient.Publish("router.register", payload)
}

func (s *Syncer) listenForRouter(replyUUID string) error {
	_, err := s.natsClient.Subscribe("router.start", s.handleRouterGreet)
	if err != nil {
		return err
	}

	sub, err := s.natsClient.Subscribe(replyUUID, s.handleRouterGreet)
	if err != nil {
		return err
	}
	sub.AutoUnsubscribe(1)

	return nil
}

func (s *Syncer) greetRouter(replyUUID string) error {
	err := s.natsClient.PublishRequest("router.greet", replyUUID, []byte{})
	if err != nil {
		return err
	}

	return nil
}

func (s *Syncer) handleRouterGreet(msg *nats.Msg) {
	var response routing_table.RouterGreetingMessage

	err := json.Unmarshal(msg.Data, &response)
	if err != nil {
		s.logger.Error("received-invalid-router-start", err, lager.Data{
			"payload": msg.Data,
		})
		return
	}

	greetInterval := response.PruneThresholdInSeconds / 3
	s.routerGreet <- time.Duration(greetInterval) * time.Second
}
