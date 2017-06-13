package routingtable

import (
	"fmt"

	"code.cloudfoundry.org/bbs/models"
)

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

type InternalAddressMessageBuilder struct {
	MessagesToEmitBuilder
}

func (InternalAddressMessageBuilder) RegistrationsFor(existingEntry, newEntry *RoutableEndpoints) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}
	if len(newEntry.Routes) == 0 {
		//no hostnames, so nothing could possibly be registered
		return messagesToEmit
	}

	// only new entry OR something changed between existing and new entry
	newRoutes := findNewHostnames(existingEntry, newEntry)
	if existingEntry == nil || len(newRoutes) != 0 || routeServiceUrlHasChanged(existingEntry, newEntry) {
		for _, endpoint := range newEntry.Endpoints {
			createAndAddInternalAddressMessages(endpoint, newEntry.Routes, &messagesToEmit.RegistrationMessages)
		}
		return messagesToEmit
	}

	//otherwise only register *new* endpoints
	for _, endpoint := range newEntry.Endpoints {
		if !existingEntry.hasEndpoint(endpoint) {
			createAndAddInternalAddressMessages(endpoint, newEntry.Routes, &messagesToEmit.RegistrationMessages)
		}
	}

	return messagesToEmit
}

func (InternalAddressMessageBuilder) UnregistrationsFor(existingEntry, newEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}

	if len(existingEntry.Routes) == 0 {
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
				createAndAddInternalAddressMessages(endpoint, existingEntry.Routes, &messagesToEmit.UnregistrationMessages)
			}
		}
	}

	routesThatDisappeared := []Route{}
	for _, route := range existingEntry.Routes {
		if !newEntry.hasHostname(route.Hostname) {
			routesThatDisappeared = append(routesThatDisappeared, route)
		}
	}

	if len(routesThatDisappeared) > 0 {
		for _, endpoint := range endpointsThatAreStillPresent {
			// only unregister if domain is fresh or preforming event processing
			if domains == nil || domains.Contains(endpoint.Domain) {
				//if a endpoint is still present, and hostnames have disappeared, unregister those hostnames
				createAndAddInternalAddressMessages(endpoint, routesThatDisappeared, &messagesToEmit.UnregistrationMessages)
			}
		}
	}

	return messagesToEmit
}

func (InternalAddressMessageBuilder) UnfreshRegistrations(existingEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}
	for _, endpoint := range existingEntry.Endpoints {
		if domains != nil && !domains.Contains(endpoint.Domain) {
			createAndAddInternalAddressMessages(endpoint, existingEntry.Routes, &messagesToEmit.RegistrationMessages)
		}
	}

	return messagesToEmit
}

func (InternalAddressMessageBuilder) MergedRegistrations(existingEntry, newEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}

	for _, endpoint := range newEntry.Endpoints {
		routeList := newEntry.Routes
		if domains != nil && !domains.Contains(endpoint.Domain) {
			// Not Fresh
			for _, route := range existingEntry.Routes {
				var addRoute bool = true
				for _, newRoute := range routeList {
					if newRoute.Hostname == route.Hostname {
						addRoute = false
					}
				}
				if addRoute {
					routeList = append(routeList, route)
				}
			}
		}
		newEntry.Routes = routeList

		if len(newEntry.Routes) == 0 {
			//no hostnames, so nothing could possibly be registered
			continue
		}

		createAndAddInternalAddressMessages(endpoint, routeList, &messagesToEmit.RegistrationMessages)
	}
	return messagesToEmit
}

type MessagesToEmitBuilder struct {
}

func (MessagesToEmitBuilder) UnfreshRegistrations(existingEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}
	for _, endpoint := range existingEntry.Endpoints {
		if domains != nil && !domains.Contains(endpoint.Domain) {
			createAndAddMessages(endpoint, existingEntry.Routes, &messagesToEmit.RegistrationMessages)
		}
	}

	return messagesToEmit
}

func (MessagesToEmitBuilder) MergedRegistrations(existingEntry, newEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}

	for _, endpoint := range newEntry.Endpoints {
		routeList := newEntry.Routes
		if domains != nil && !domains.Contains(endpoint.Domain) {
			// Not Fresh
			for _, route := range existingEntry.Routes {
				var addRoute bool = true
				for _, newRoute := range routeList {
					if newRoute.Hostname == route.Hostname {
						addRoute = false
					}
				}
				if addRoute {
					routeList = append(routeList, route)
				}
			}
		}
		newEntry.Routes = routeList

		if len(newEntry.Routes) == 0 {
			//no hostnames, so nothing could possibly be registered
			continue
		}

		createAndAddMessages(endpoint, routeList, &messagesToEmit.RegistrationMessages)
	}
	return messagesToEmit
}

func (MessagesToEmitBuilder) RegistrationsFor(existingEntry, newEntry *RoutableEndpoints) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}
	if len(newEntry.Routes) == 0 {
		//no hostnames, so nothing could possibly be registered
		return messagesToEmit
	}

	// only new entry OR something changed between existing and new entry
	newRoutes := findNewHostnames(existingEntry, newEntry)
	fmt.Printf("EXISITING: %#v\n\n\n", existingEntry)
	fmt.Printf("NEW: %#v\n\n\n", newEntry)
	if existingEntry == nil || routeServiceUrlHasChanged(existingEntry, newEntry) {
		fmt.Println("RRRRRRRRRRRRRRRRRRRRRRRRRRR")
		for _, endpoint := range newEntry.Endpoints {
			createAndAddMessages(endpoint, newEntry.Routes, &messagesToEmit.RegistrationMessages)
		}
	} else if len(newRoutes) != 0 {
		fmt.Printf("NEW ROUTES: %#v\n\n\n", newRoutes)
		for _, endpoint := range newEntry.Endpoints {
			createAndAddMessages(endpoint, newRoutes, &messagesToEmit.RegistrationMessages)
		}
	} else {
		fmt.Println("GGGGGGGGGGGGGGGGGGGGGGGGGGGGGggg")
		//otherwise only register *new* endpoints
		for _, endpoint := range newEntry.Endpoints {
			if !existingEntry.hasEndpoint(endpoint) {
				createAndAddMessages(endpoint, newEntry.Routes, &messagesToEmit.RegistrationMessages)
			}
		}
	}

	return messagesToEmit
}

func (MessagesToEmitBuilder) UnregistrationsFor(existingEntry, newEntry *RoutableEndpoints, domains models.DomainSet) MessagesToEmit {
	messagesToEmit := MessagesToEmit{}

	if len(existingEntry.Routes) == 0 {
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
				createAndAddMessages(endpoint, existingEntry.Routes, &messagesToEmit.UnregistrationMessages)
			}
		}
	}

	routesThatDisappeared := []Route{}
	for _, route := range existingEntry.Routes {
		if !newEntry.hasHostname(route.Hostname) {
			routesThatDisappeared = append(routesThatDisappeared, route)
		}
	}

	fmt.Println("ROUTES THAT DISAPPEARED:")
	fmt.Println(routesThatDisappeared)
	if len(routesThatDisappeared) > 0 {
		for _, endpoint := range endpointsThatAreStillPresent {
			// only unregister if domain is fresh or preforming event processing
			if domains == nil || domains.Contains(endpoint.Domain) {
				//if a endpoint is still present, and hostnames have disappeared, unregister those hostnames
				createAndAddMessages(endpoint, routesThatDisappeared, &messagesToEmit.UnregistrationMessages)
			}
		}
	}

	return messagesToEmit
}

func findNewHostnames(existingEntry, newEntry *RoutableEndpoints) []Route {
	var routes []Route
	if existingEntry == nil {
		return newEntry.Routes
	}

	for _, route := range newEntry.Routes {
		if !existingEntry.hasHostname(route.Hostname) {
			routes = append(routes, route)
		}
	}

	return routes
}

func routeServiceUrlHasChanged(existingEntry, newEntry *RoutableEndpoints) bool {
	for _, route := range newEntry.Routes {
		if !existingEntry.hasRouteServiceUrl(route.RouteServiceUrl) {
			return true
		}
	}
	return false
}

func createAndAddMessages(endpoint Endpoint, routes []Route, messages *[]RegistryMessage) {
	for _, route := range routes {
		message := RegistryMessageFor(endpoint, route)
		*messages = append(*messages, message)
	}
}

func createAndAddInternalAddressMessages(endpoint Endpoint, routes []Route, messages *[]RegistryMessage) {
	for _, route := range routes {
		message := InternalAddressRegistryMessageFor(endpoint, route)
		*messages = append(*messages, message)
	}
}
