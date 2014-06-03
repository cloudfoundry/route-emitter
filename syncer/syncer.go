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
	bbs        bbs.LRPRouterBBS
	natsClient yagnats.NATSClient
	logger     *gosteno.Logger
	table      routing_table.RoutingTableInterface
	emitter    nats_emitter.NATSEmitterInterface

	heartbeatInterval chan time.Duration
}

func NewSyncer(bbs bbs.LRPRouterBBS, table routing_table.RoutingTableInterface, emitter nats_emitter.NATSEmitterInterface, natsClient yagnats.NATSClient, logger *gosteno.Logger) *Syncer {
	return &Syncer{
		bbs:        bbs,
		table:      table,
		emitter:    emitter,
		natsClient: natsClient,
		logger:     logger,

		heartbeatInterval: make(chan time.Duration),
	}
}

func (syncer *Syncer) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	err := syncer.greetWithRouter()
	if err != nil {
		return err
	}

	close(ready)

	heartbeatInterval := <-syncer.heartbeatInterval

	for {
		allRunningActuals, err := syncer.bbs.GetRunningActualLRPs()
		if err != nil {
			syncer.logger.Warnd(map[string]interface{}{
				"error": err.Error(),
			}, "syncer.get-actual.failed")
		}

		allDesired, err := syncer.bbs.GetAllDesiredLRPs()
		if err != nil {
			syncer.logger.Warnd(map[string]interface{}{
				"error": err.Error(),
			}, "syncer.get-desired.failed")
		}

		routesToEmit := syncer.table.Sync(
			routing_table.RoutesByProcessGuidFromDesireds(allDesired),
			routing_table.ContainersByProcessGuidFromActuals(allRunningActuals),
		)

		err = syncer.emitter.Emit(routesToEmit)
		if err != nil {
			syncer.logger.Warnd(map[string]interface{}{
				"error": err.Error(),
			}, "syncer.emit-routes.failed")
		}

		select {
		case heartbeatInterval = <-syncer.heartbeatInterval:
		case <-time.After(heartbeatInterval):
		case <-signals:
			syncer.logger.Info("route-emitter.syncer.stopping")
			return nil
		}
	}

	return nil
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

func (syncer *Syncer) greetWithRouter() error {
	replyUuid, err := uuid.NewV4()
	if err != nil {
		return err
	}

	var subscription int64
	subscription, err = syncer.natsClient.Subscribe(replyUuid.String(), func(msg *yagnats.Message) {
		syncer.gotRouterHeartbeatInterval(msg)
		syncer.natsClient.Unsubscribe(subscription)
	})
	if err != nil {
		return err
	}

	_, err = syncer.natsClient.Subscribe("router.start", syncer.gotRouterHeartbeatInterval)
	if err != nil {
		return err
	}

	err = syncer.natsClient.PublishWithReplyTo("router.greet", replyUuid.String(), []byte{})
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
