package syncer

import (
	"encoding/json"
	"os"
	"time"

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
}

func NewSyncer(bbs bbs.LRPRouterBBS, natsClient yagnats.NATSClient, logger *gosteno.Logger) *Syncer {
	return &Syncer{
		bbs:        bbs,
		natsClient: natsClient,
		logger:     logger,
	}
}

func (syncer *Syncer) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	close(ready)

	routerHeartbeatInterval, err := syncer.getRouterHeartbeatInterval()
	if err != nil {
		panic(err)
	}

	for {
		allActual, err := syncer.bbs.GetAllActualLongRunningProcesses()
		if err != nil {
			syncer.logger.Warnd(map[string]interface{}{
				"error": err.Error(),
			}, "syncer.get-actual.failed")
			continue
		}

		allDesired, err := syncer.bbs.GetAllDesiredLongRunningProcesses()
		if err != nil {
			syncer.logger.Warnd(map[string]interface{}{
				"error": err.Error(),
			}, "syncer.get-desired.failed")
			continue
		}

		for _, actual := range allActual {
			for _, desired := range allDesired {
				if desired.ProcessGuid == actual.ProcessGuid {
					syncer.register(desired, actual)
				}
			}
		}

		time.Sleep(routerHeartbeatInterval)
	}

	return nil
}

func (syncer *Syncer) register(desired models.DesiredLRP, actual models.LRP) error {
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

func (syncer *Syncer) getRouterHeartbeatInterval() (time.Duration, error) {
	replyUuid, err := uuid.NewV4()
	if err != nil {
		return 0, err
	}

	routerHeartbeatInterval := make(chan time.Duration, 1)
	var subscription int64
	subscription, err = syncer.natsClient.Subscribe(replyUuid.String(), func(msg *yagnats.Message) {
		response := gibson.RouterGreetingMessage{}
		json.Unmarshal(msg.Payload, &response)
		routerHeartbeatInterval <- time.Duration(response.MinimumRegisterInterval) * time.Second
		syncer.natsClient.Unsubscribe(subscription)
	})
	if err != nil {
		return 0, err
	}

	err = syncer.natsClient.PublishWithReplyTo("router.greet", replyUuid.String(), []byte{})
	if err != nil {
		return 0, err
	}

	return <-routerHeartbeatInterval, nil
}
