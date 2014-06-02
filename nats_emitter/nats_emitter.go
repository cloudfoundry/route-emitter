package nats_emitter

import (
	"encoding/json"

	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry/gibson"
	"github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/yagnats"
)

type NATSEmitterInterface interface {
	Emit(messagesToEmit routing_table.MessagesToEmit) error
}

type NATSEmitter struct {
	natsClient yagnats.NATSClient
	logger     *gosteno.Logger
}

func New(natsClient yagnats.NATSClient, logger *gosteno.Logger) *NATSEmitter {
	return &NATSEmitter{
		natsClient: natsClient,
		logger:     logger,
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

	n.logger.Infod(map[string]interface{}{
		"subject": subject,
		"message": message,
	}, "route-emitter.emit")

	payload, err := json.Marshal(message)
	if err != nil {
		n.logger.Errord(map[string]interface{}{
			"error":   err.Error(),
			"message": message,
			"subject": subject,
		}, "route-emitter.emit.json-marshal-failed")
		return
	}

	err = n.natsClient.Publish(subject, payload)
	if err != nil {
		n.logger.Errord(map[string]interface{}{
			"error":   err.Error(),
			"message": message,
			"subject": subject,
		}, "route-emitter.emit.publish-failed")
		return
	}
}
