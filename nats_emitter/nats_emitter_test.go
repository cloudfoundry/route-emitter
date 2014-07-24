package nats_emitter_test

import (
	"errors"

	. "github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry/gibson"
	"github.com/cloudfoundry/yagnats"
	"github.com/cloudfoundry/yagnats/fakeyagnats"
	"github.com/pivotal-golang/lager/lagertest"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("NatsEmitter", func() {
	var emitter *NATSEmitter
	var natsClient *fakeyagnats.FakeYagnats

	messagesToEmit := routing_table.MessagesToEmit{
		RegistrationMessages: []gibson.RegistryMessage{
			{URIs: []string{"foo.com", "bar.com"}, Host: "1.1.1.1", Port: 11},
			{URIs: []string{"baz.com"}, Host: "2.2.2.2", Port: 22},
		},
		UnregistrationMessages: []gibson.RegistryMessage{
			{URIs: []string{"wibble.com"}, Host: "1.1.1.1", Port: 11},
			{URIs: []string{"baz.com"}, Host: "3.3.3.3", Port: 33},
		},
	}

	BeforeEach(func() {
		natsClient = fakeyagnats.New()
		logger := lagertest.NewTestLogger("test")
		emitter = New(natsClient, logger)
	})

	Describe("Emitting", func() {
		It("should emit register and unregister messages", func() {
			err := emitter.Emit(messagesToEmit)
			Ω(err).ShouldNot(HaveOccurred())

			Ω(natsClient.PublishedMessages("router.register")).Should(HaveLen(2))
			Ω(natsClient.PublishedMessages("router.unregister")).Should(HaveLen(2))

			registeredPayloads := [][]byte{
				natsClient.PublishedMessages("router.register")[0].Payload,
				natsClient.PublishedMessages("router.register")[1].Payload,
			}

			unregisteredPayloads := [][]byte{
				natsClient.PublishedMessages("router.unregister")[0].Payload,
				natsClient.PublishedMessages("router.unregister")[1].Payload,
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

		Context("when the nats client errors", func() {
			BeforeEach(func() {
				natsClient.WhenPublishing("router.register", func(*yagnats.Message) error {
					return errors.New("bam")
				})
			})

			It("should error", func() {
				Ω(emitter.Emit(messagesToEmit)).Should(MatchError(errors.New("bam")))
			})
		})
	})
})
