package nats_emitter

import (
	"encoding/json"
	"sync"

	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/runtime-schema/metric"
	"github.com/cloudfoundry/gunk/diegonats"
	"github.com/cloudfoundry/gunk/workpool"
	"github.com/pivotal-golang/lager"
)

type NATSEmitterInterface interface {
	Emit(messagesToEmit routing_table.MessagesToEmit, registrationCounter, unregistrationCounter *metric.Counter) error
}

type NATSEmitter struct {
	natsClient diegonats.NATSClient
	workPool   *workpool.WorkPool
	logger     lager.Logger
}

func New(natsClient diegonats.NATSClient, workPool *workpool.WorkPool, logger lager.Logger) *NATSEmitter {
	return &NATSEmitter{
		natsClient: natsClient,
		workPool:   workPool,
		logger:     logger.Session("nats-emitter"),
	}
}

func (n *NATSEmitter) Emit(messagesToEmit routing_table.MessagesToEmit, registrationCounter, unregistrationCounter *metric.Counter) error {
	errors := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(len(messagesToEmit.RegistrationMessages))
	for _, message := range messagesToEmit.RegistrationMessages {
		n.emit("router.register", message, &wg, errors)
	}

	wg.Add(len(messagesToEmit.UnregistrationMessages))
	for _, message := range messagesToEmit.UnregistrationMessages {
		n.emit("router.unregister", message, &wg, errors)
	}

	wg.Wait()

	updateCounter(registrationCounter, messagesToEmit.RegistrationMessages)
	updateCounter(unregistrationCounter, messagesToEmit.UnregistrationMessages)

	select {
	case finalError := <-errors:
		return finalError
	default:
	}

	return nil
}

func (n *NATSEmitter) emit(subject string, message routing_table.RegistryMessage, wg *sync.WaitGroup, errors chan error) {
	n.workPool.Submit(func() {
		var err error
		defer func() {
			if err != nil {
				select {
				case errors <- err:
				default:
				}
			}
			wg.Done()
		}()

		n.logger.Debug("emit", lager.Data{
			"subject": subject,
			"message": message,
		})

		payload, err := json.Marshal(message)
		if err != nil {
			n.logger.Error("failed-to-marshal", err, lager.Data{
				"message": message,
				"subject": subject,
			})
		}

		err = n.natsClient.Publish(subject, payload)
		if err != nil {
			n.logger.Error("failed-to-publish", err, lager.Data{
				"message": message,
				"subject": subject,
			})
		}
	})
}

func updateCounter(counter *metric.Counter, messages []routing_table.RegistryMessage) {
	if counter != nil {
		count := 0
		for _, message := range messages {
			count += len(message.URIs)
		}
		counter.Add(uint64(count))
	}
}
