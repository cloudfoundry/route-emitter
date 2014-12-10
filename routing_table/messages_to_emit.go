package routing_table

type MessagesToEmit struct {
	RegistrationMessages   []RegistryMessage
	UnregistrationMessages []RegistryMessage
}

func (m MessagesToEmit) merge(o MessagesToEmit) MessagesToEmit {
	return MessagesToEmit{
		RegistrationMessages:   append(m.RegistrationMessages, o.RegistrationMessages...),
		UnregistrationMessages: append(m.UnregistrationMessages, o.UnregistrationMessages...),
	}
}

func RegistryMessageFor(container Container, routes ...string) RegistryMessage {
	return RegistryMessage{
		URIs: routes,
		Host: container.Host,
		Port: container.Port,
	}
}
