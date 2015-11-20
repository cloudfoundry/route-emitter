package routing_table

import "github.com/cloudfoundry-incubator/bbs/models"

type MessageBuilder interface {
	RegistrationsFor(existingEntry, newEntry *RoutableEndpoints) MessagesToEmit
	UnfreshRegistrations(existingEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit
	MergedRegistrations(existingEntry, newEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit
	UnregistrationsFor(existingEntry, newEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit
}

type NoopMessageBuilder struct {
}

func (NoopMessageBuilder) RegistrationsFor(existingEntry, newEntry *RoutableEndpoints) MessagesToEmit {
	return MessagesToEmit{}
}

func (NoopMessageBuilder) UnfreshRegistrations(existingEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit {
	return MessagesToEmit{}
}

func (NoopMessageBuilder) MergedRegistrations(existingEntry, newEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit {
	return MessagesToEmit{}
}

func (NoopMessageBuilder) UnregistrationsFor(existingEntry, newEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit {
	return MessagesToEmit{}
}

type MessagesToEmitBuilder struct {
}

func (MessagesToEmitBuilder) UnfreshRegistrations(existingEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}
	for _, endpoint := range existingEntry.Endpoints {
		if domains != nil && !domains.Contains(endpoint.Domain) {
			message := RegistryMessageFor(endpoint, existingEntry.routes())
			messagesToEmit.RegistrationMessages = append(messagesToEmit.RegistrationMessages, message)
		}
	}

	return messagesToEmit
}

func (MessagesToEmitBuilder) MergedRegistrations(existingEntry, newEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}
	if len(newEntry.Hostnames) == 0 {
		//no hostnames, so nothing could possibly be registered
		return messagesToEmit
	}

	routeList := newEntry.routes()
	for _, endpoint := range newEntry.Endpoints {
		if domains != nil && !domains.Contains(endpoint.Domain) {
			// Not Fresh
			for _, hostName := range existingEntry.routes().Hostnames {
				var addHostName bool = true
				for _, value := range routeList.Hostnames {
					if value == hostName {
						addHostName = false
					}
				}

				if addHostName {
					routeList.Hostnames = append(routeList.Hostnames, hostName)
				}
			}
		}
		newEntry.Hostnames = routesAsMap(routeList.Hostnames)
		message := RegistryMessageFor(endpoint, routeList)
		messagesToEmit.RegistrationMessages = append(messagesToEmit.RegistrationMessages, message)
	}
	return messagesToEmit
}

func (MessagesToEmitBuilder) RegistrationsFor(existingEntry, newEntry *RoutableEndpoints) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}
	if len(newEntry.Hostnames) == 0 {
		//no hostnames, so nothing could possibly be registered
		return messagesToEmit
	}

	// only new entry OR something changed between existing and new entry
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

func (MessagesToEmitBuilder) UnregistrationsFor(existingEntry, newEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit {
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
			// only unregister if domain is fresh or preforming event processing
			if domains == nil || domains.Contains(endpoint.Domain) {
				//if the endpoint has disappeared unregister all its previous hostnames
				message := RegistryMessageFor(endpoint, existingEntry.routes())
				messagesToEmit.UnregistrationMessages = append(messagesToEmit.UnregistrationMessages, message)
			}
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
			// only unregister if domain is fresh or preforming event processing
			if domains == nil || domains.Contains(endpoint.Domain) {
				//if a endpoint is still present, and hostnames have disappeared, unregister those hostnames
				message := RegistryMessageFor(endpoint, Routes{
					Hostnames: hostnamesThatDisappeared,
					LogGuid:   existingEntry.LogGuid,
				})
				messagesToEmit.UnregistrationMessages = append(messagesToEmit.UnregistrationMessages, message)
			}
		}
	}

	return messagesToEmit
}

func hostnamesHaveChanged(existingEntry, newEntry *RoutableEndpoints) bool {
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

func routeServiceUrlHasChanged(existingEntry, newEntry *RoutableEndpoints) bool {
	return newEntry.RouteServiceUrl != existingEntry.RouteServiceUrl
}
