package syncer

import (
	"encoding/json"
	"os"
	"time"

	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry/gibson"
	"github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/yagnats"
	"github.com/nu7hatch/gouuid"
)

type Syncer struct {
	bbs               bbs.LRPRouterBBS
	natsClient        yagnats.NATSClient
	logger            *gosteno.Logger
	table             routing_table.RoutingTableInterface
	emitter           nats_emitter.NATSEmitterInterface
	syncDuration      time.Duration
	heartbeatInterval chan time.Duration
}

func NewSyncer(bbs bbs.LRPRouterBBS, table routing_table.RoutingTableInterface, emitter nats_emitter.NATSEmitterInterface, syncDuration time.Duration, natsClient yagnats.NATSClient, logger *gosteno.Logger) *Syncer {
	return &Syncer{
		bbs:        bbs,
		table:      table,
		emitter:    emitter,
		natsClient: natsClient,
		logger:     logger,

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
		syncer.logger.Info("route-emitter.syncer.greeting-router")
		err := syncer.greetRouter(replyUuid.String())
		if err != nil {
			return err
		}

		select {
		case heartbeatInterval = <-syncer.heartbeatInterval:
			break GREET_LOOP
		case <-retryGreetingTicker.C:
		case <-signals:
			syncer.logger.Info("route-emitter.syncer.stopping")
			return nil
		}
	}
	retryGreetingTicker.Stop()

	//now keep emitting at the desired interval, syncing with etcd every syncDuration
	syncTicker := time.NewTicker(syncer.syncDuration)
	for {
		select {
		case heartbeatInterval = <-syncer.heartbeatInterval:
			syncer.emit()
		case <-time.After(heartbeatInterval):
			syncer.emit()
		case <-syncTicker.C:
			//we decouple syncing the routing table (via etcd) from emitting the routes
			//since the watcher is receiving deltas our internal cache should be generally up-to-date
			syncer.syncAndEmit()
		case <-signals:
			syncer.logger.Info("route-emitter.syncer.stopping")
			return nil
		}
	}

	return nil
}

func (syncer *Syncer) emit() {
	messagesToEmit := syncer.table.MessagesToEmit()

	err := syncer.emitter.Emit(messagesToEmit)
	if err != nil {
		syncer.logger.Warnd(map[string]interface{}{
			"error": err.Error(),
		}, "syncer.emit-routes.failed")
	}
}

func (syncer *Syncer) syncAndEmit() {
	allRunningActuals, err := syncer.bbs.GetRunningActualLRPs()
	if err != nil {
		syncer.logger.Warnd(map[string]interface{}{
			"error": err.Error(),
		}, "syncer.get-actual.failed")
		return
	}

	allDesired, err := syncer.bbs.GetAllDesiredLRPs()
	if err != nil {
		syncer.logger.Warnd(map[string]interface{}{
			"error": err.Error(),
		}, "syncer.get-desired.failed")
		return
	}

	routesToEmit := syncer.table.Sync(
		routing_table.RoutesByProcessGuidFromDesireds(allDesired),
		routing_table.ContainersByProcessGuidFromActuals(allRunningActuals),
	)

	err = syncer.emitter.Emit(routesToEmit)
	if err != nil {
		syncer.logger.Warnd(map[string]interface{}{
			"error": err.Error(),
		}, "syncer.sync-and-emit-routes.failed-to-emit")
	}
}

func (syncer *Syncer) register(desired models.DesiredLRP, actual models.ActualLRP) error {
	message := gibson.RegistryMessage{
		URIs: desired.Routes,
		Host: actual.Host,
		Port: int(actual.Ports[0].HostPort),
	}

	payload, err := json.Marshal(message)
	if err != nil {
		panic(err)
	}

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
	err := syncer.natsClient.PublishWithReplyTo("router.greet", replyUUID, []byte{})
	if err != nil {
		return err
	}

	return nil
}

func (syncer *Syncer) gotRouterHeartbeatInterval(msg *yagnats.Message) {
	var response gibson.RouterGreetingMessage

	err := json.Unmarshal(msg.Payload, &response)
	if err != nil {
		syncer.logger.Warnd(map[string]interface{}{
			"error":   err.Error(),
			"payload": msg.Payload,
		}, "syncer.invalid-router-start.received")
		return
	}

	syncer.heartbeatInterval <- time.Duration(response.MinimumRegisterInterval) * time.Second
}
