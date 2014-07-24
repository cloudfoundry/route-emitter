package nats_emitter

import (
	"encoding/json"

	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry/gibson"
	"github.com/cloudfoundry/yagnats"
	"github.com/pivotal-golang/lager"
)

type NATSEmitterInterface interface {
	Emit(messagesToEmit routing_table.MessagesToEmit) error
}

type NATSEmitter struct {
	natsClient yagnats.NATSClient
	logger     lager.Logger
}

func New(natsClient yagnats.NATSClient, logger lager.Logger) *NATSEmitter {
	return &NATSEmitter{
		natsClient: natsClient,
		logger:     logger.Session("nats-emitter"),
	}
}

func (n *NATSEmitter) Emit(messagesToEmit routing_table.MessagesToEmit) error {
	errors := make(chan error)
	for _, message := range messagesToEmit.RegistrationMessages {
		go n.emit("router.register", message, errors)
	}
	for _, message := range messagesToEmit.UnregistrationMessages {
		go n.emit("router.unregister", message, errors)
	}

	var finalError error
	for i := 0; i < len(messagesToEmit.RegistrationMessages)+len(messagesToEmit.UnregistrationMessages); i++ {
		err := <-errors
		if err != nil && finalError == nil {
			finalError = err
		}
	}
	return finalError
}

func (n *NATSEmitter) emit(subject string, message gibson.RegistryMessage, errors chan<- error) {
	var err error
	defer func() {
		errors <- err
	}()

	n.logger.Info("emit", lager.Data{
		"subject": subject,
		"message": message,
	})

	payload, err := json.Marshal(message)
	if err != nil {
		n.logger.Error("failed-to-marshal", err, lager.Data{
			"message": message,
			"subject": subject,
		})
		return
	}

	err = n.natsClient.Publish(subject, payload)
	if err != nil {
		n.logger.Error("failed-to-publish", err, lager.Data{
			"message": message,
			"subject": subject,
		})
		return
	}
}
