package nats_emitter_test

import (
	"errors"

	"github.com/apcera/nats"
	. "github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/runtime-schema/metric"
	fake_metrics_sender "github.com/cloudfoundry/dropsonde/metric_sender/fake"
	"github.com/cloudfoundry/dropsonde/metrics"
	"github.com/cloudfoundry/gunk/diegonats"
	"github.com/cloudfoundry/gunk/workpool"
	"github.com/pivotal-golang/lager/lagertest"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func createCounter(name string) *metric.Counter {
	counter := metric.Counter(name)
	return &counter
}

var _ = Describe("NatsEmitter", func() {
	var emitter *NATSEmitter
	var natsClient *diegonats.FakeNATSClient
	var fakeMetricSender *fake_metrics_sender.FakeMetricSender

	messagesToEmit := routing_table.MessagesToEmit{
		RegistrationMessages: []routing_table.RegistryMessage{
			{URIs: []string{"foo.com", "bar.com"}, Host: "1.1.1.1", Port: 11},
			{URIs: []string{"baz.com"}, Host: "2.2.2.2", Port: 22},
		},
		UnregistrationMessages: []routing_table.RegistryMessage{
			{URIs: []string{"wibble.com"}, Host: "1.1.1.1", Port: 11},
			{URIs: []string{"baz.com"}, Host: "3.3.3.3", Port: 33},
		},
	}

	BeforeEach(func() {
		natsClient = diegonats.NewFakeClient()
		logger := lagertest.NewTestLogger("test")
		workPool := workpool.NewWorkPool(1)
		emitter = New(natsClient, workPool, logger)
		fakeMetricSender = fake_metrics_sender.NewFakeMetricSender()
		metrics.Initialize(fakeMetricSender)
	})

	Describe("Emitting", func() {
		It("should emit register and unregister messages", func() {
			err := emitter.Emit(messagesToEmit, nil, nil)
			Ω(err).ShouldNot(HaveOccurred())

			Ω(natsClient.PublishedMessages("router.register")).Should(HaveLen(2))
			Ω(natsClient.PublishedMessages("router.unregister")).Should(HaveLen(2))

			registeredPayloads := [][]byte{
				natsClient.PublishedMessages("router.register")[0].Data,
				natsClient.PublishedMessages("router.register")[1].Data,
			}

			unregisteredPayloads := [][]byte{
				natsClient.PublishedMessages("router.unregister")[0].Data,
				natsClient.PublishedMessages("router.unregister")[1].Data,
			}

			Ω(registeredPayloads).Should(ContainElement(MatchJSON(`
        {
          "uris":["foo.com", "bar.com"],
          "host":"1.1.1.1",
          "port":11
        }
      `)))
			Ω(registeredPayloads).Should(ContainElement(MatchJSON(`
        {
          "uris":["baz.com"],
          "host":"2.2.2.2",
          "port":22
        }
      `)))

			Ω(unregisteredPayloads).Should(ContainElement(MatchJSON(`
        {
          "uris":["wibble.com"],
          "host":"1.1.1.1",
          "port":11
        }
      `)))
			Ω(unregisteredPayloads).Should(ContainElement(MatchJSON(`
        {
          "uris":["baz.com"],
          "host":"3.3.3.3",
          "port":33
        }
      `)))
		})

		It("increments the 'routes registered' counter", func() {
			fakeRegistrationCounter := createCounter("fake-registration-counter")
			err := emitter.Emit(messagesToEmit, fakeRegistrationCounter, nil)
			Ω(err).ShouldNot(HaveOccurred())
			Ω(fakeMetricSender.GetCounter("fake-registration-counter")).Should(BeEquivalentTo(3))

			err = emitter.Emit(messagesToEmit, fakeRegistrationCounter, nil)
			Ω(err).ShouldNot(HaveOccurred())
			Ω(fakeMetricSender.GetCounter("fake-registration-counter")).Should(BeEquivalentTo(6))
		})

		It("increments the 'routes unregistered' counter", func() {
			fakeUnregistrationCounter := createCounter("fake-unregistration-counter")
			err := emitter.Emit(messagesToEmit, nil, fakeUnregistrationCounter)
			Ω(err).ShouldNot(HaveOccurred())
			Ω(fakeMetricSender.GetCounter("fake-unregistration-counter")).Should(BeEquivalentTo(2))

			err = emitter.Emit(messagesToEmit, nil, fakeUnregistrationCounter)
			Ω(err).ShouldNot(HaveOccurred())
			Ω(fakeMetricSender.GetCounter("fake-unregistration-counter")).Should(BeEquivalentTo(4))
		})

		Context("when the nats client errors", func() {
			BeforeEach(func() {
				natsClient.WhenPublishing("router.register", func(*nats.Msg) error {
					return errors.New("bam")
				})
			})

			It("should error", func() {
				Ω(emitter.Emit(messagesToEmit, nil, nil)).Should(MatchError(errors.New("bam")))
			})
		})
	})
})
