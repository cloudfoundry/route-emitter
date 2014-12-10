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
