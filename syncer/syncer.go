package syncer

import (
	"encoding/json"
	"os"
	"time"

	"github.com/apcera/nats"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/runtime-schema/metric"
	"github.com/cloudfoundry/gunk/diegonats"
	uuid "github.com/nu7hatch/gouuid"
	"github.com/pivotal-golang/lager"
)

var (
	routesTotal       = metric.Metric("RoutesTotal")
	routesSynced      = metric.Counter("RoutesSynced")
	routeSyncDuration = metric.Duration("RouteEmitterSyncDuration")
)

type Syncer struct {
	receptorClient    receptor.Client
	natsClient        diegonats.NATSClient
	logger            lager.Logger
	table             routing_table.RoutingTable
	emitter           nats_emitter.NATSEmitterInterface
	syncDuration      time.Duration
	heartbeatInterval chan time.Duration
}

func NewSyncer(
	receptorClient receptor.Client,
	table routing_table.RoutingTable,
	emitter nats_emitter.NATSEmitterInterface,
	syncDuration time.Duration,
	natsClient diegonats.NATSClient,
	logger lager.Logger,
) *Syncer {
	return &Syncer{
		receptorClient: receptorClient,
		table:          table,
		emitter:        emitter,
		natsClient:     natsClient,
		logger:         logger.Session("syncer"),

		syncDuration:      syncDuration,
		heartbeatInterval: make(chan time.Duration),
	}
}

func (syncer *Syncer) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	syncer.logger.Info("starting")
	replyUuid, err := uuid.NewV4()
	if err != nil {
		return err
	}

	err = syncer.listenForHeartbeatInterval(replyUuid.String())
	if err != nil {
		return err
	}

	syncer.syncAndEmit()
	close(ready)
	syncer.logger.Info("started")

	var heartbeatInterval time.Duration
	retryGreetingTicker := time.NewTicker(time.Second)

	//keep trying to greet until we hear from the router
GREET_LOOP:
	for {
		syncer.logger.Info("greeting-router")
		err := syncer.greetRouter(replyUuid.String())
		if err != nil {
			syncer.logger.Error("failed-to-greet-router", err)
			return err
		}

		select {
		case heartbeatInterval = <-syncer.heartbeatInterval:
			syncer.logger.Info("received-heartbeat-interval")
			break GREET_LOOP
		case <-retryGreetingTicker.C:
		case <-signals:
			syncer.logger.Info("stopping")
			return nil
		}
	}
	retryGreetingTicker.Stop()

	//now keep emitting at the desired interval, syncing with etcd every syncDuration
	syncTicker := time.NewTicker(syncer.syncDuration)
	for {
		select {
		case heartbeatInterval = <-syncer.heartbeatInterval:
			syncer.logger.Info("received-new-heartbeat-interval")
			syncer.emit()
		case <-time.After(heartbeatInterval):
			syncer.logger.Info("emitting-routes")
			syncer.emit()
		case <-syncTicker.C:
			//we decouple syncing the routing table (via etcd) from emitting the routes
			//since the watcher is receiving deltas our internal cache should be generally up-to-date
			syncer.logger.Info("syncing")
			syncer.syncAndEmit()
		case <-signals:
			syncer.logger.Info("stopping")
			return nil
		}
	}

	return nil
}

func (syncer *Syncer) emit() {
	messagesToEmit := syncer.table.MessagesToEmit()

	syncer.logger.Info("emitting-messages", lager.Data{"messages": messagesToEmit})
	err := syncer.emitter.Emit(messagesToEmit, &routesSynced, nil)
	if err != nil {
		syncer.logger.Error("failed-to-emit-routes", err)
	}

	routesTotal.Send(syncer.table.RouteCount())
}

func (syncer *Syncer) syncAndEmit() {
	before := time.Now()
	defer func() {
		after := time.Now()
		routeSyncDuration.Send(after.Sub(before))
	}()

	actualLRPResponses, err := syncer.receptorClient.ActualLRPs()
	if err != nil {
		syncer.logger.Error("failed-to-get-actual", err)
		return
	}

	desiredLRPResponses, err := syncer.receptorClient.DesiredLRPs()
	if err != nil {
		syncer.logger.Error("failed-to-get-desired", err)
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

	routesToEmit := syncer.table.Sync(
		routing_table.RoutesByProcessGuidFromDesireds(desiredLRPs),
		routing_table.ContainersByProcessGuidFromActuals(runningActualLRPs),
	)

	syncer.logger.Info("emitting-routes-after-syncing", lager.Data{"routes": routesToEmit})
	err = syncer.emitter.Emit(routesToEmit, &routesSynced, nil)
	if err != nil {
		syncer.logger.Error("failed-to-emit-synced", err)
	}

	routesTotal.Send(syncer.table.RouteCount())
}

func (syncer *Syncer) register(desired receptor.DesiredLRPResponse, actual receptor.ActualLRPResponse) error {
	message := routing_table.RegistryMessage{
		URIs: desired.Routes.CFRoutes[0].Hostnames,
		Host: actual.Address,
		Port: uint16(actual.Ports[0].HostPort),
	}

	payload, _ := json.Marshal(message)

	return syncer.natsClient.Publish("router.register", payload)
}

func (syncer *Syncer) listenForHeartbeatInterval(replyUUID string) error {
	_, err := syncer.natsClient.Subscribe("router.start", syncer.gotRouterHeartbeatInterval)
	if err != nil {
		return err
	}

	_, err = syncer.natsClient.Subscribe(replyUUID, syncer.gotRouterHeartbeatInterval)
	if err != nil {
		return err
	}

	return nil
}

func (syncer *Syncer) greetRouter(replyUUID string) error {
	err := syncer.natsClient.PublishRequest("router.greet", replyUUID, []byte{})
	if err != nil {
		return err
	}

	return nil
}

func (syncer *Syncer) gotRouterHeartbeatInterval(msg *nats.Msg) {
	var response routing_table.RouterGreetingMessage

	err := json.Unmarshal(msg.Data, &response)
	if err != nil {
		syncer.logger.Error("received-invalid-router-start", err, lager.Data{
			"payload": msg.Data,
		})
		return
	}

	syncer.heartbeatInterval <- time.Duration(response.MinimumRegisterInterval) * time.Second
}
