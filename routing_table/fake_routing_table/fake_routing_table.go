// This file was generated by counterfeiter
package fake_routing_table

import (
	"sync"

	"github.com/cloudfoundry-incubator/bbs/models"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
)

type FakeRoutingTable struct {
	RouteCountStub        func() int
	routeCountMutex       sync.RWMutex
	routeCountArgsForCall []struct{}
	routeCountReturns struct {
		result1 int
	}
	SwapStub        func(newTable routing_table.RoutingTable) routing_table.MessagesToEmit
	swapMutex       sync.RWMutex
	swapArgsForCall []struct {
		newTable routing_table.RoutingTable
	}
	swapReturns struct {
		result1 routing_table.MessagesToEmit
	}
	SetRoutesStub        func(key routing_table.RoutingKey, routes routing_table.Routes) routing_table.MessagesToEmit
	setRoutesMutex       sync.RWMutex
	setRoutesArgsForCall []struct {
		key    routing_table.RoutingKey
		routes routing_table.Routes
	}
	setRoutesReturns struct {
		result1 routing_table.MessagesToEmit
	}
	RemoveRoutesStub        func(key routing_table.RoutingKey, modTag *models.ModificationTag) routing_table.MessagesToEmit
	removeRoutesMutex       sync.RWMutex
	removeRoutesArgsForCall []struct {
		key    routing_table.RoutingKey
		modTag *models.ModificationTag
	}
	removeRoutesReturns struct {
		result1 routing_table.MessagesToEmit
	}
	AddEndpointStub        func(key routing_table.RoutingKey, endpoint routing_table.Endpoint) routing_table.MessagesToEmit
	addEndpointMutex       sync.RWMutex
	addEndpointArgsForCall []struct {
		key      routing_table.RoutingKey
		endpoint routing_table.Endpoint
	}
	addEndpointReturns struct {
		result1 routing_table.MessagesToEmit
	}
	RemoveEndpointStub        func(key routing_table.RoutingKey, endpoint routing_table.Endpoint) routing_table.MessagesToEmit
	removeEndpointMutex       sync.RWMutex
	removeEndpointArgsForCall []struct {
		key      routing_table.RoutingKey
		endpoint routing_table.Endpoint
	}
	removeEndpointReturns struct {
		result1 routing_table.MessagesToEmit
	}
	MessagesToEmitStub        func() routing_table.MessagesToEmit
	messagesToEmitMutex       sync.RWMutex
	messagesToEmitArgsForCall []struct{}
	messagesToEmitReturns struct {
		result1 routing_table.MessagesToEmit
	}
}

func (fake *FakeRoutingTable) RouteCount() int {
	fake.routeCountMutex.Lock()
	fake.routeCountArgsForCall = append(fake.routeCountArgsForCall, struct{}{})
	fake.routeCountMutex.Unlock()
	if fake.RouteCountStub != nil {
		return fake.RouteCountStub()
	} else {
		return fake.routeCountReturns.result1
	}
}

func (fake *FakeRoutingTable) RouteCountCallCount() int {
	fake.routeCountMutex.RLock()
	defer fake.routeCountMutex.RUnlock()
	return len(fake.routeCountArgsForCall)
}

func (fake *FakeRoutingTable) RouteCountReturns(result1 int) {
	fake.RouteCountStub = nil
	fake.routeCountReturns = struct {
		result1 int
	}{result1}
}

func (fake *FakeRoutingTable) Swap(newTable routing_table.RoutingTable) routing_table.MessagesToEmit {
	fake.swapMutex.Lock()
	fake.swapArgsForCall = append(fake.swapArgsForCall, struct {
		newTable routing_table.RoutingTable
	}{newTable})
	fake.swapMutex.Unlock()
	if fake.SwapStub != nil {
		return fake.SwapStub(newTable)
	} else {
		return fake.swapReturns.result1
	}
}

func (fake *FakeRoutingTable) SwapCallCount() int {
	fake.swapMutex.RLock()
	defer fake.swapMutex.RUnlock()
	return len(fake.swapArgsForCall)
}

func (fake *FakeRoutingTable) SwapArgsForCall(i int) routing_table.RoutingTable {
	fake.swapMutex.RLock()
	defer fake.swapMutex.RUnlock()
	return fake.swapArgsForCall[i].newTable
}

func (fake *FakeRoutingTable) SwapReturns(result1 routing_table.MessagesToEmit) {
	fake.SwapStub = nil
	fake.swapReturns = struct {
		result1 routing_table.MessagesToEmit
	}{result1}
}

func (fake *FakeRoutingTable) SetRoutes(key routing_table.RoutingKey, routes routing_table.Routes) routing_table.MessagesToEmit {
	fake.setRoutesMutex.Lock()
	fake.setRoutesArgsForCall = append(fake.setRoutesArgsForCall, struct {
		key    routing_table.RoutingKey
		routes routing_table.Routes
	}{key, routes})
	fake.setRoutesMutex.Unlock()
	if fake.SetRoutesStub != nil {
		return fake.SetRoutesStub(key, routes)
	} else {
		return fake.setRoutesReturns.result1
	}
}

func (fake *FakeRoutingTable) SetRoutesCallCount() int {
	fake.setRoutesMutex.RLock()
	defer fake.setRoutesMutex.RUnlock()
	return len(fake.setRoutesArgsForCall)
}

func (fake *FakeRoutingTable) SetRoutesArgsForCall(i int) (routing_table.RoutingKey, routing_table.Routes) {
	fake.setRoutesMutex.RLock()
	defer fake.setRoutesMutex.RUnlock()
	return fake.setRoutesArgsForCall[i].key, fake.setRoutesArgsForCall[i].routes
}

func (fake *FakeRoutingTable) SetRoutesReturns(result1 routing_table.MessagesToEmit) {
	fake.SetRoutesStub = nil
	fake.setRoutesReturns = struct {
		result1 routing_table.MessagesToEmit
	}{result1}
}

func (fake *FakeRoutingTable) RemoveRoutes(key routing_table.RoutingKey, modTag *models.ModificationTag) routing_table.MessagesToEmit {
	fake.removeRoutesMutex.Lock()
	fake.removeRoutesArgsForCall = append(fake.removeRoutesArgsForCall, struct {
		key    routing_table.RoutingKey
		modTag *models.ModificationTag
	}{key, modTag})
	fake.removeRoutesMutex.Unlock()
	if fake.RemoveRoutesStub != nil {
		return fake.RemoveRoutesStub(key, modTag)
	} else {
		return fake.removeRoutesReturns.result1
	}
}

func (fake *FakeRoutingTable) RemoveRoutesCallCount() int {
	fake.removeRoutesMutex.RLock()
	defer fake.removeRoutesMutex.RUnlock()
	return len(fake.removeRoutesArgsForCall)
}

func (fake *FakeRoutingTable) RemoveRoutesArgsForCall(i int) (routing_table.RoutingKey, *models.ModificationTag) {
	fake.removeRoutesMutex.RLock()
	defer fake.removeRoutesMutex.RUnlock()
	return fake.removeRoutesArgsForCall[i].key, fake.removeRoutesArgsForCall[i].modTag
}

func (fake *FakeRoutingTable) RemoveRoutesReturns(result1 routing_table.MessagesToEmit) {
	fake.RemoveRoutesStub = nil
	fake.removeRoutesReturns = struct {
		result1 routing_table.MessagesToEmit
	}{result1}
}

func (fake *FakeRoutingTable) AddEndpoint(key routing_table.RoutingKey, endpoint routing_table.Endpoint) routing_table.MessagesToEmit {
	fake.addEndpointMutex.Lock()
	fake.addEndpointArgsForCall = append(fake.addEndpointArgsForCall, struct {
		key      routing_table.RoutingKey
		endpoint routing_table.Endpoint
	}{key, endpoint})
	fake.addEndpointMutex.Unlock()
	if fake.AddEndpointStub != nil {
		return fake.AddEndpointStub(key, endpoint)
	} else {
		return fake.addEndpointReturns.result1
	}
}

func (fake *FakeRoutingTable) AddEndpointCallCount() int {
	fake.addEndpointMutex.RLock()
	defer fake.addEndpointMutex.RUnlock()
	return len(fake.addEndpointArgsForCall)
}

func (fake *FakeRoutingTable) AddEndpointArgsForCall(i int) (routing_table.RoutingKey, routing_table.Endpoint) {
	fake.addEndpointMutex.RLock()
	defer fake.addEndpointMutex.RUnlock()
	return fake.addEndpointArgsForCall[i].key, fake.addEndpointArgsForCall[i].endpoint
}

func (fake *FakeRoutingTable) AddEndpointReturns(result1 routing_table.MessagesToEmit) {
	fake.AddEndpointStub = nil
	fake.addEndpointReturns = struct {
		result1 routing_table.MessagesToEmit
	}{result1}
}

func (fake *FakeRoutingTable) RemoveEndpoint(key routing_table.RoutingKey, endpoint routing_table.Endpoint) routing_table.MessagesToEmit {
	fake.removeEndpointMutex.Lock()
	fake.removeEndpointArgsForCall = append(fake.removeEndpointArgsForCall, struct {
		key      routing_table.RoutingKey
		endpoint routing_table.Endpoint
	}{key, endpoint})
	fake.removeEndpointMutex.Unlock()
	if fake.RemoveEndpointStub != nil {
		return fake.RemoveEndpointStub(key, endpoint)
	} else {
		return fake.removeEndpointReturns.result1
	}
}

func (fake *FakeRoutingTable) RemoveEndpointCallCount() int {
	fake.removeEndpointMutex.RLock()
	defer fake.removeEndpointMutex.RUnlock()
	return len(fake.removeEndpointArgsForCall)
}

func (fake *FakeRoutingTable) RemoveEndpointArgsForCall(i int) (routing_table.RoutingKey, routing_table.Endpoint) {
	fake.removeEndpointMutex.RLock()
	defer fake.removeEndpointMutex.RUnlock()
	return fake.removeEndpointArgsForCall[i].key, fake.removeEndpointArgsForCall[i].endpoint
}

func (fake *FakeRoutingTable) RemoveEndpointReturns(result1 routing_table.MessagesToEmit) {
	fake.RemoveEndpointStub = nil
	fake.removeEndpointReturns = struct {
		result1 routing_table.MessagesToEmit
	}{result1}
}

func (fake *FakeRoutingTable) MessagesToEmit() routing_table.MessagesToEmit {
	fake.messagesToEmitMutex.Lock()
	fake.messagesToEmitArgsForCall = append(fake.messagesToEmitArgsForCall, struct{}{})
	fake.messagesToEmitMutex.Unlock()
	if fake.MessagesToEmitStub != nil {
		return fake.MessagesToEmitStub()
	} else {
		return fake.messagesToEmitReturns.result1
	}
}

func (fake *FakeRoutingTable) MessagesToEmitCallCount() int {
	fake.messagesToEmitMutex.RLock()
	defer fake.messagesToEmitMutex.RUnlock()
	return len(fake.messagesToEmitArgsForCall)
}

func (fake *FakeRoutingTable) MessagesToEmitReturns(result1 routing_table.MessagesToEmit) {
	fake.MessagesToEmitStub = nil
	fake.messagesToEmitReturns = struct {
		result1 routing_table.MessagesToEmit
	}{result1}
}

var _ routing_table.RoutingTable = new(FakeRoutingTable)
