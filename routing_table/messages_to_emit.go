package routing_table

import "github.com/cloudfoundry/gibson"

type MessagesToEmit struct {
	RegistrationMessages   []gibson.RegistryMessage
	UnregistrationMessages []gibson.RegistryMessage
}

func (m MessagesToEmit) merge(o MessagesToEmit) MessagesToEmit {
	return MessagesToEmit{
		RegistrationMessages:   append(m.RegistrationMessages, o.RegistrationMessages...),
		UnregistrationMessages: append(m.UnregistrationMessages, o.UnregistrationMessages...),
	}
}

func RegistryMessageFor(container Container, routes ...string) gibson.RegistryMessage {
	return gibson.RegistryMessage{
		URIs: routes,
		Host: container.Host,
		Port: container.Port,
	}
}
