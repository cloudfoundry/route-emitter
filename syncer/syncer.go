package syncer

import (
	"encoding/json"
	"os"
	"time"

	"github.com/apcera/nats"
	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/metric"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry/gunk/diegonats"
	uuid "github.com/nu7hatch/gouuid"
	"github.com/pivotal-golang/lager"
)

var (
	routesTotal  = metric.Metric("RoutesTotal")
	routesSynced = metric.Counter("RoutesSynced")
)

type Syncer struct {
	bbs               bbs.RouteEmitterBBS
	natsClient        diegonats.NATSClient
	logger            lager.Logger
	table             routing_table.RoutingTableInterface
	emitter           nats_emitter.NATSEmitterInterface
	syncDuration      time.Duration
	heartbeatInterval chan time.Duration
}

func NewSyncer(
	bbs bbs.RouteEmitterBBS,
	table routing_table.RoutingTableInterface,
	emitter nats_emitter.NATSEmitterInterface,
	syncDuration time.Duration,
	natsClient diegonats.NATSClient,
	logger lager.Logger,
) *Syncer {
	return &Syncer{
		bbs:        bbs,
		table:      table,
		emitter:    emitter,
		natsClient: natsClient,
		logger:     logger.Session("syncer"),

		syncDuration:      syncDuration,
		heartbeatInterval: make(chan time.Duration),
	}
}

func (syncer *Syncer) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
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

	err := syncer.emitter.Emit(messagesToEmit, &routesSynced, nil)
	if err != nil {
		syncer.logger.Error("failed-to-emit-routes", err)
	}

	routesTotal.Send(syncer.table.RouteCount())
}

func (syncer *Syncer) syncAndEmit() {
	allRunningActuals, err := syncer.bbs.RunningActualLRPs()
	if err != nil {
		syncer.logger.Error("failed-to-get-actual", err)
		return
	}

	allDesired, err := syncer.bbs.DesiredLRPs()
	if err != nil {
		syncer.logger.Error("failed-to-get-desired", err)
		return
	}

	routesToEmit := syncer.table.Sync(
		routing_table.RoutesByProcessGuidFromDesireds(allDesired),
		routing_table.ContainersByProcessGuidFromActuals(allRunningActuals),
	)

	err = syncer.emitter.Emit(routesToEmit, &routesSynced, nil)
	if err != nil {
		syncer.logger.Error("failed-to-emit-synced", err)
	}

	routesTotal.Send(syncer.table.RouteCount())
}

func (syncer *Syncer) register(desired models.DesiredLRP, actual models.ActualLRP) error {
	message := routing_table.RegistryMessage{
		URIs: desired.Routes,
		Host: actual.Host,
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
