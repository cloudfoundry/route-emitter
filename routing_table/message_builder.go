package routing_table

type MessageBuilder interface {
	RegistrationsFor(existingEntry *RoutableEndpoints, newEntry *RoutableEndpoints) MessagesToEmit
	UnregistrationsFor(existingEntry *RoutableEndpoints, newEntry *RoutableEndpoints) MessagesToEmit
}

type NoopMessageBuilder struct {
}

func (NoopMessageBuilder) RegistrationsFor(existingEntry *RoutableEndpoints, newEntry *RoutableEndpoints) MessagesToEmit {
	return MessagesToEmit{}
}
func (NoopMessageBuilder) UnregistrationsFor(existingEntry *RoutableEndpoints, newEntry *RoutableEndpoints) MessagesToEmit {
	return MessagesToEmit{}
}

type MessagesToEmitBuilder struct {
}

func (MessagesToEmitBuilder) RegistrationsFor(existingEntry *RoutableEndpoints, newEntry *RoutableEndpoints) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}

	if len(newEntry.Hostnames) == 0 {
		//no hostnames, so nothing could possibly be registered
		return messagesToEmit
	}

	if existingEntry == nil || hostnamesHaveChanged(existingEntry, newEntry) || routeServiceUrlHasChanged(existingEntry, newEntry) {
		for _, endpoint := range newEntry.Endpoints {
			message := RegistryMessageFor(endpoint, newEntry.routes())
			messagesToEmit.RegistrationMessages = append(messagesToEmit.RegistrationMessages, message)
		}
		return messagesToEmit
	}

	//otherwise only register *new* endpoints
	for _, endpoint := range newEntry.Endpoints {
		if !existingEntry.hasEndpoint(endpoint) {
			message := RegistryMessageFor(endpoint, newEntry.routes())
			messagesToEmit.RegistrationMessages = append(messagesToEmit.RegistrationMessages, message)
		}
	}

	return messagesToEmit
}

func (MessagesToEmitBuilder) UnregistrationsFor(existingEntry *RoutableEndpoints, newEntry *RoutableEndpoints) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}

	if len(existingEntry.Hostnames) == 0 {
		// the existing entry has no hostnames and so there is nothing to unregister
		return messagesToEmit
	}

	endpointsThatAreStillPresent := []Endpoint{}
	for _, endpoint := range existingEntry.Endpoints {
		if newEntry.hasEndpoint(endpoint) {
			endpointsThatAreStillPresent = append(endpointsThatAreStillPresent, endpoint)
		} else {
			//if the endpoint has disappeared unregister all its previous hostnames
			message := RegistryMessageFor(endpoint, existingEntry.routes())
			messagesToEmit.UnregistrationMessages = append(messagesToEmit.UnregistrationMessages, message)
		}
	}

	hostnamesThatDisappeared := []string{}
	for hostname := range existingEntry.Hostnames {
		if !newEntry.hasHostname(hostname) {
			hostnamesThatDisappeared = append(hostnamesThatDisappeared, hostname)
		}
	}

	if len(hostnamesThatDisappeared) > 0 {
		for _, endpoint := range endpointsThatAreStillPresent {
			//if a endpoint is still present, and hostnames have disappeared, unregister those hostnames
			message := RegistryMessageFor(endpoint, Routes{
				Hostnames: hostnamesThatDisappeared,
				LogGuid:   existingEntry.LogGuid,
			})
			messagesToEmit.UnregistrationMessages = append(messagesToEmit.UnregistrationMessages, message)
		}
	}

	return messagesToEmit
}

func hostnamesHaveChanged(existingEntry *RoutableEndpoints, newEntry *RoutableEndpoints) bool {
	if len(newEntry.Hostnames) != len(existingEntry.Hostnames) {
		return true
	} else {
		for hostname := range newEntry.Hostnames {
			if !existingEntry.hasHostname(hostname) {
				return true
			}
		}
	}

	return false
}

func routeServiceUrlHasChanged(existingEntry *RoutableEndpoints, newEntry *RoutableEndpoints) bool {
	return newEntry.RouteServiceUrl != existingEntry.RouteServiceUrl
}
