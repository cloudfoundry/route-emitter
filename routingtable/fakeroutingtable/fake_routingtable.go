// Code generated by counterfeiter. DO NOT EDIT.
package fakeroutingtable

import (
	"sync"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager/v3"
	"code.cloudfoundry.org/route-emitter/routingtable"
)

type FakeRoutingTable struct {
	AddEndpointStub        func(lager.Logger, *models.ActualLRP) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)
	addEndpointMutex       sync.RWMutex
	addEndpointArgsForCall []struct {
		arg1 lager.Logger
		arg2 *models.ActualLRP
	}
	addEndpointReturns struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	addEndpointReturnsOnCall map[int]struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	GetExternalRoutingEventsStub        func() (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)
	getExternalRoutingEventsMutex       sync.RWMutex
	getExternalRoutingEventsArgsForCall []struct {
	}
	getExternalRoutingEventsReturns struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	getExternalRoutingEventsReturnsOnCall map[int]struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	GetInternalRoutingEventsStub        func() (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)
	getInternalRoutingEventsMutex       sync.RWMutex
	getInternalRoutingEventsArgsForCall []struct {
	}
	getInternalRoutingEventsReturns struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	getInternalRoutingEventsReturnsOnCall map[int]struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	HTTPAssociationsCountStub        func() int
	hTTPAssociationsCountMutex       sync.RWMutex
	hTTPAssociationsCountArgsForCall []struct {
	}
	hTTPAssociationsCountReturns struct {
		result1 int
	}
	hTTPAssociationsCountReturnsOnCall map[int]struct {
		result1 int
	}
	HasExternalRoutesStub        func(*models.ActualLRP) bool
	hasExternalRoutesMutex       sync.RWMutex
	hasExternalRoutesArgsForCall []struct {
		arg1 *models.ActualLRP
	}
	hasExternalRoutesReturns struct {
		result1 bool
	}
	hasExternalRoutesReturnsOnCall map[int]struct {
		result1 bool
	}
	InternalAssociationsCountStub        func() int
	internalAssociationsCountMutex       sync.RWMutex
	internalAssociationsCountArgsForCall []struct {
	}
	internalAssociationsCountReturns struct {
		result1 int
	}
	internalAssociationsCountReturnsOnCall map[int]struct {
		result1 int
	}
	RemoveEndpointStub        func(lager.Logger, *models.ActualLRP) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)
	removeEndpointMutex       sync.RWMutex
	removeEndpointArgsForCall []struct {
		arg1 lager.Logger
		arg2 *models.ActualLRP
	}
	removeEndpointReturns struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	removeEndpointReturnsOnCall map[int]struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	RemoveRoutesStub        func(lager.Logger, *models.DesiredLRP) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)
	removeRoutesMutex       sync.RWMutex
	removeRoutesArgsForCall []struct {
		arg1 lager.Logger
		arg2 *models.DesiredLRP
	}
	removeRoutesReturns struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	removeRoutesReturnsOnCall map[int]struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	SetRoutesStub        func(lager.Logger, *models.DesiredLRP, *models.DesiredLRP) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)
	setRoutesMutex       sync.RWMutex
	setRoutesArgsForCall []struct {
		arg1 lager.Logger
		arg2 *models.DesiredLRP
		arg3 *models.DesiredLRP
	}
	setRoutesReturns struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	setRoutesReturnsOnCall map[int]struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	SwapStub        func(lager.Logger, routingtable.RoutingTable, models.DomainSet) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)
	swapMutex       sync.RWMutex
	swapArgsForCall []struct {
		arg1 lager.Logger
		arg2 routingtable.RoutingTable
		arg3 models.DomainSet
	}
	swapReturns struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	swapReturnsOnCall map[int]struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}
	TCPAssociationsCountStub        func() int
	tCPAssociationsCountMutex       sync.RWMutex
	tCPAssociationsCountArgsForCall []struct {
	}
	tCPAssociationsCountReturns struct {
		result1 int
	}
	tCPAssociationsCountReturnsOnCall map[int]struct {
		result1 int
	}
	TableSizeStub        func() int
	tableSizeMutex       sync.RWMutex
	tableSizeArgsForCall []struct {
	}
	tableSizeReturns struct {
		result1 int
	}
	tableSizeReturnsOnCall map[int]struct {
		result1 int
	}
	invocations      map[string][][]interface{}
	invocationsMutex sync.RWMutex
}

func (fake *FakeRoutingTable) AddEndpoint(arg1 lager.Logger, arg2 *models.ActualLRP) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit) {
	fake.addEndpointMutex.Lock()
	ret, specificReturn := fake.addEndpointReturnsOnCall[len(fake.addEndpointArgsForCall)]
	fake.addEndpointArgsForCall = append(fake.addEndpointArgsForCall, struct {
		arg1 lager.Logger
		arg2 *models.ActualLRP
	}{arg1, arg2})
	fake.recordInvocation("AddEndpoint", []interface{}{arg1, arg2})
	fake.addEndpointMutex.Unlock()
	if fake.AddEndpointStub != nil {
		return fake.AddEndpointStub(arg1, arg2)
	}
	if specificReturn {
		return ret.result1, ret.result2
	}
	fakeReturns := fake.addEndpointReturns
	return fakeReturns.result1, fakeReturns.result2
}

func (fake *FakeRoutingTable) AddEndpointCallCount() int {
	fake.addEndpointMutex.RLock()
	defer fake.addEndpointMutex.RUnlock()
	return len(fake.addEndpointArgsForCall)
}

func (fake *FakeRoutingTable) AddEndpointCalls(stub func(lager.Logger, *models.ActualLRP) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)) {
	fake.addEndpointMutex.Lock()
	defer fake.addEndpointMutex.Unlock()
	fake.AddEndpointStub = stub
}

func (fake *FakeRoutingTable) AddEndpointArgsForCall(i int) (lager.Logger, *models.ActualLRP) {
	fake.addEndpointMutex.RLock()
	defer fake.addEndpointMutex.RUnlock()
	argsForCall := fake.addEndpointArgsForCall[i]
	return argsForCall.arg1, argsForCall.arg2
}

func (fake *FakeRoutingTable) AddEndpointReturns(result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.addEndpointMutex.Lock()
	defer fake.addEndpointMutex.Unlock()
	fake.AddEndpointStub = nil
	fake.addEndpointReturns = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) AddEndpointReturnsOnCall(i int, result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.addEndpointMutex.Lock()
	defer fake.addEndpointMutex.Unlock()
	fake.AddEndpointStub = nil
	if fake.addEndpointReturnsOnCall == nil {
		fake.addEndpointReturnsOnCall = make(map[int]struct {
			result1 routingtable.TCPRouteMappings
			result2 routingtable.MessagesToEmit
		})
	}
	fake.addEndpointReturnsOnCall[i] = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) GetExternalRoutingEvents() (routingtable.TCPRouteMappings, routingtable.MessagesToEmit) {
	fake.getExternalRoutingEventsMutex.Lock()
	ret, specificReturn := fake.getExternalRoutingEventsReturnsOnCall[len(fake.getExternalRoutingEventsArgsForCall)]
	fake.getExternalRoutingEventsArgsForCall = append(fake.getExternalRoutingEventsArgsForCall, struct {
	}{})
	fake.recordInvocation("GetExternalRoutingEvents", []interface{}{})
	fake.getExternalRoutingEventsMutex.Unlock()
	if fake.GetExternalRoutingEventsStub != nil {
		return fake.GetExternalRoutingEventsStub()
	}
	if specificReturn {
		return ret.result1, ret.result2
	}
	fakeReturns := fake.getExternalRoutingEventsReturns
	return fakeReturns.result1, fakeReturns.result2
}

func (fake *FakeRoutingTable) GetExternalRoutingEventsCallCount() int {
	fake.getExternalRoutingEventsMutex.RLock()
	defer fake.getExternalRoutingEventsMutex.RUnlock()
	return len(fake.getExternalRoutingEventsArgsForCall)
}

func (fake *FakeRoutingTable) GetExternalRoutingEventsCalls(stub func() (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)) {
	fake.getExternalRoutingEventsMutex.Lock()
	defer fake.getExternalRoutingEventsMutex.Unlock()
	fake.GetExternalRoutingEventsStub = stub
}

func (fake *FakeRoutingTable) GetExternalRoutingEventsReturns(result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.getExternalRoutingEventsMutex.Lock()
	defer fake.getExternalRoutingEventsMutex.Unlock()
	fake.GetExternalRoutingEventsStub = nil
	fake.getExternalRoutingEventsReturns = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) GetExternalRoutingEventsReturnsOnCall(i int, result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.getExternalRoutingEventsMutex.Lock()
	defer fake.getExternalRoutingEventsMutex.Unlock()
	fake.GetExternalRoutingEventsStub = nil
	if fake.getExternalRoutingEventsReturnsOnCall == nil {
		fake.getExternalRoutingEventsReturnsOnCall = make(map[int]struct {
			result1 routingtable.TCPRouteMappings
			result2 routingtable.MessagesToEmit
		})
	}
	fake.getExternalRoutingEventsReturnsOnCall[i] = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) GetInternalRoutingEvents() (routingtable.TCPRouteMappings, routingtable.MessagesToEmit) {
	fake.getInternalRoutingEventsMutex.Lock()
	ret, specificReturn := fake.getInternalRoutingEventsReturnsOnCall[len(fake.getInternalRoutingEventsArgsForCall)]
	fake.getInternalRoutingEventsArgsForCall = append(fake.getInternalRoutingEventsArgsForCall, struct {
	}{})
	fake.recordInvocation("GetInternalRoutingEvents", []interface{}{})
	fake.getInternalRoutingEventsMutex.Unlock()
	if fake.GetInternalRoutingEventsStub != nil {
		return fake.GetInternalRoutingEventsStub()
	}
	if specificReturn {
		return ret.result1, ret.result2
	}
	fakeReturns := fake.getInternalRoutingEventsReturns
	return fakeReturns.result1, fakeReturns.result2
}

func (fake *FakeRoutingTable) GetInternalRoutingEventsCallCount() int {
	fake.getInternalRoutingEventsMutex.RLock()
	defer fake.getInternalRoutingEventsMutex.RUnlock()
	return len(fake.getInternalRoutingEventsArgsForCall)
}

func (fake *FakeRoutingTable) GetInternalRoutingEventsCalls(stub func() (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)) {
	fake.getInternalRoutingEventsMutex.Lock()
	defer fake.getInternalRoutingEventsMutex.Unlock()
	fake.GetInternalRoutingEventsStub = stub
}

func (fake *FakeRoutingTable) GetInternalRoutingEventsReturns(result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.getInternalRoutingEventsMutex.Lock()
	defer fake.getInternalRoutingEventsMutex.Unlock()
	fake.GetInternalRoutingEventsStub = nil
	fake.getInternalRoutingEventsReturns = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) GetInternalRoutingEventsReturnsOnCall(i int, result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.getInternalRoutingEventsMutex.Lock()
	defer fake.getInternalRoutingEventsMutex.Unlock()
	fake.GetInternalRoutingEventsStub = nil
	if fake.getInternalRoutingEventsReturnsOnCall == nil {
		fake.getInternalRoutingEventsReturnsOnCall = make(map[int]struct {
			result1 routingtable.TCPRouteMappings
			result2 routingtable.MessagesToEmit
		})
	}
	fake.getInternalRoutingEventsReturnsOnCall[i] = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) HTTPAssociationsCount() int {
	fake.hTTPAssociationsCountMutex.Lock()
	ret, specificReturn := fake.hTTPAssociationsCountReturnsOnCall[len(fake.hTTPAssociationsCountArgsForCall)]
	fake.hTTPAssociationsCountArgsForCall = append(fake.hTTPAssociationsCountArgsForCall, struct {
	}{})
	fake.recordInvocation("HTTPAssociationsCount", []interface{}{})
	fake.hTTPAssociationsCountMutex.Unlock()
	if fake.HTTPAssociationsCountStub != nil {
		return fake.HTTPAssociationsCountStub()
	}
	if specificReturn {
		return ret.result1
	}
	fakeReturns := fake.hTTPAssociationsCountReturns
	return fakeReturns.result1
}

func (fake *FakeRoutingTable) HTTPAssociationsCountCallCount() int {
	fake.hTTPAssociationsCountMutex.RLock()
	defer fake.hTTPAssociationsCountMutex.RUnlock()
	return len(fake.hTTPAssociationsCountArgsForCall)
}

func (fake *FakeRoutingTable) HTTPAssociationsCountCalls(stub func() int) {
	fake.hTTPAssociationsCountMutex.Lock()
	defer fake.hTTPAssociationsCountMutex.Unlock()
	fake.HTTPAssociationsCountStub = stub
}

func (fake *FakeRoutingTable) HTTPAssociationsCountReturns(result1 int) {
	fake.hTTPAssociationsCountMutex.Lock()
	defer fake.hTTPAssociationsCountMutex.Unlock()
	fake.HTTPAssociationsCountStub = nil
	fake.hTTPAssociationsCountReturns = struct {
		result1 int
	}{result1}
}

func (fake *FakeRoutingTable) HTTPAssociationsCountReturnsOnCall(i int, result1 int) {
	fake.hTTPAssociationsCountMutex.Lock()
	defer fake.hTTPAssociationsCountMutex.Unlock()
	fake.HTTPAssociationsCountStub = nil
	if fake.hTTPAssociationsCountReturnsOnCall == nil {
		fake.hTTPAssociationsCountReturnsOnCall = make(map[int]struct {
			result1 int
		})
	}
	fake.hTTPAssociationsCountReturnsOnCall[i] = struct {
		result1 int
	}{result1}
}

func (fake *FakeRoutingTable) HasExternalRoutes(arg1 *models.ActualLRP) bool {
	fake.hasExternalRoutesMutex.Lock()
	ret, specificReturn := fake.hasExternalRoutesReturnsOnCall[len(fake.hasExternalRoutesArgsForCall)]
	fake.hasExternalRoutesArgsForCall = append(fake.hasExternalRoutesArgsForCall, struct {
		arg1 *models.ActualLRP
	}{arg1})
	fake.recordInvocation("HasExternalRoutes", []interface{}{arg1})
	fake.hasExternalRoutesMutex.Unlock()
	if fake.HasExternalRoutesStub != nil {
		return fake.HasExternalRoutesStub(arg1)
	}
	if specificReturn {
		return ret.result1
	}
	fakeReturns := fake.hasExternalRoutesReturns
	return fakeReturns.result1
}

func (fake *FakeRoutingTable) HasExternalRoutesCallCount() int {
	fake.hasExternalRoutesMutex.RLock()
	defer fake.hasExternalRoutesMutex.RUnlock()
	return len(fake.hasExternalRoutesArgsForCall)
}

func (fake *FakeRoutingTable) HasExternalRoutesCalls(stub func(*models.ActualLRP) bool) {
	fake.hasExternalRoutesMutex.Lock()
	defer fake.hasExternalRoutesMutex.Unlock()
	fake.HasExternalRoutesStub = stub
}

func (fake *FakeRoutingTable) HasExternalRoutesArgsForCall(i int) *models.ActualLRP {
	fake.hasExternalRoutesMutex.RLock()
	defer fake.hasExternalRoutesMutex.RUnlock()
	argsForCall := fake.hasExternalRoutesArgsForCall[i]
	return argsForCall.arg1
}

func (fake *FakeRoutingTable) HasExternalRoutesReturns(result1 bool) {
	fake.hasExternalRoutesMutex.Lock()
	defer fake.hasExternalRoutesMutex.Unlock()
	fake.HasExternalRoutesStub = nil
	fake.hasExternalRoutesReturns = struct {
		result1 bool
	}{result1}
}

func (fake *FakeRoutingTable) HasExternalRoutesReturnsOnCall(i int, result1 bool) {
	fake.hasExternalRoutesMutex.Lock()
	defer fake.hasExternalRoutesMutex.Unlock()
	fake.HasExternalRoutesStub = nil
	if fake.hasExternalRoutesReturnsOnCall == nil {
		fake.hasExternalRoutesReturnsOnCall = make(map[int]struct {
			result1 bool
		})
	}
	fake.hasExternalRoutesReturnsOnCall[i] = struct {
		result1 bool
	}{result1}
}

func (fake *FakeRoutingTable) InternalAssociationsCount() int {
	fake.internalAssociationsCountMutex.Lock()
	ret, specificReturn := fake.internalAssociationsCountReturnsOnCall[len(fake.internalAssociationsCountArgsForCall)]
	fake.internalAssociationsCountArgsForCall = append(fake.internalAssociationsCountArgsForCall, struct {
	}{})
	fake.recordInvocation("InternalAssociationsCount", []interface{}{})
	fake.internalAssociationsCountMutex.Unlock()
	if fake.InternalAssociationsCountStub != nil {
		return fake.InternalAssociationsCountStub()
	}
	if specificReturn {
		return ret.result1
	}
	fakeReturns := fake.internalAssociationsCountReturns
	return fakeReturns.result1
}

func (fake *FakeRoutingTable) InternalAssociationsCountCallCount() int {
	fake.internalAssociationsCountMutex.RLock()
	defer fake.internalAssociationsCountMutex.RUnlock()
	return len(fake.internalAssociationsCountArgsForCall)
}

func (fake *FakeRoutingTable) InternalAssociationsCountCalls(stub func() int) {
	fake.internalAssociationsCountMutex.Lock()
	defer fake.internalAssociationsCountMutex.Unlock()
	fake.InternalAssociationsCountStub = stub
}

func (fake *FakeRoutingTable) InternalAssociationsCountReturns(result1 int) {
	fake.internalAssociationsCountMutex.Lock()
	defer fake.internalAssociationsCountMutex.Unlock()
	fake.InternalAssociationsCountStub = nil
	fake.internalAssociationsCountReturns = struct {
		result1 int
	}{result1}
}

func (fake *FakeRoutingTable) InternalAssociationsCountReturnsOnCall(i int, result1 int) {
	fake.internalAssociationsCountMutex.Lock()
	defer fake.internalAssociationsCountMutex.Unlock()
	fake.InternalAssociationsCountStub = nil
	if fake.internalAssociationsCountReturnsOnCall == nil {
		fake.internalAssociationsCountReturnsOnCall = make(map[int]struct {
			result1 int
		})
	}
	fake.internalAssociationsCountReturnsOnCall[i] = struct {
		result1 int
	}{result1}
}

func (fake *FakeRoutingTable) RemoveEndpoint(arg1 lager.Logger, arg2 *models.ActualLRP) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit) {
	fake.removeEndpointMutex.Lock()
	ret, specificReturn := fake.removeEndpointReturnsOnCall[len(fake.removeEndpointArgsForCall)]
	fake.removeEndpointArgsForCall = append(fake.removeEndpointArgsForCall, struct {
		arg1 lager.Logger
		arg2 *models.ActualLRP
	}{arg1, arg2})
	fake.recordInvocation("RemoveEndpoint", []interface{}{arg1, arg2})
	fake.removeEndpointMutex.Unlock()
	if fake.RemoveEndpointStub != nil {
		return fake.RemoveEndpointStub(arg1, arg2)
	}
	if specificReturn {
		return ret.result1, ret.result2
	}
	fakeReturns := fake.removeEndpointReturns
	return fakeReturns.result1, fakeReturns.result2
}

func (fake *FakeRoutingTable) RemoveEndpointCallCount() int {
	fake.removeEndpointMutex.RLock()
	defer fake.removeEndpointMutex.RUnlock()
	return len(fake.removeEndpointArgsForCall)
}

func (fake *FakeRoutingTable) RemoveEndpointCalls(stub func(lager.Logger, *models.ActualLRP) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)) {
	fake.removeEndpointMutex.Lock()
	defer fake.removeEndpointMutex.Unlock()
	fake.RemoveEndpointStub = stub
}

func (fake *FakeRoutingTable) RemoveEndpointArgsForCall(i int) (lager.Logger, *models.ActualLRP) {
	fake.removeEndpointMutex.RLock()
	defer fake.removeEndpointMutex.RUnlock()
	argsForCall := fake.removeEndpointArgsForCall[i]
	return argsForCall.arg1, argsForCall.arg2
}

func (fake *FakeRoutingTable) RemoveEndpointReturns(result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.removeEndpointMutex.Lock()
	defer fake.removeEndpointMutex.Unlock()
	fake.RemoveEndpointStub = nil
	fake.removeEndpointReturns = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) RemoveEndpointReturnsOnCall(i int, result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.removeEndpointMutex.Lock()
	defer fake.removeEndpointMutex.Unlock()
	fake.RemoveEndpointStub = nil
	if fake.removeEndpointReturnsOnCall == nil {
		fake.removeEndpointReturnsOnCall = make(map[int]struct {
			result1 routingtable.TCPRouteMappings
			result2 routingtable.MessagesToEmit
		})
	}
	fake.removeEndpointReturnsOnCall[i] = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) RemoveRoutes(arg1 lager.Logger, arg2 *models.DesiredLRP) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit) {
	fake.removeRoutesMutex.Lock()
	ret, specificReturn := fake.removeRoutesReturnsOnCall[len(fake.removeRoutesArgsForCall)]
	fake.removeRoutesArgsForCall = append(fake.removeRoutesArgsForCall, struct {
		arg1 lager.Logger
		arg2 *models.DesiredLRP
	}{arg1, arg2})
	fake.recordInvocation("RemoveRoutes", []interface{}{arg1, arg2})
	fake.removeRoutesMutex.Unlock()
	if fake.RemoveRoutesStub != nil {
		return fake.RemoveRoutesStub(arg1, arg2)
	}
	if specificReturn {
		return ret.result1, ret.result2
	}
	fakeReturns := fake.removeRoutesReturns
	return fakeReturns.result1, fakeReturns.result2
}

func (fake *FakeRoutingTable) RemoveRoutesCallCount() int {
	fake.removeRoutesMutex.RLock()
	defer fake.removeRoutesMutex.RUnlock()
	return len(fake.removeRoutesArgsForCall)
}

func (fake *FakeRoutingTable) RemoveRoutesCalls(stub func(lager.Logger, *models.DesiredLRP) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)) {
	fake.removeRoutesMutex.Lock()
	defer fake.removeRoutesMutex.Unlock()
	fake.RemoveRoutesStub = stub
}

func (fake *FakeRoutingTable) RemoveRoutesArgsForCall(i int) (lager.Logger, *models.DesiredLRP) {
	fake.removeRoutesMutex.RLock()
	defer fake.removeRoutesMutex.RUnlock()
	argsForCall := fake.removeRoutesArgsForCall[i]
	return argsForCall.arg1, argsForCall.arg2
}

func (fake *FakeRoutingTable) RemoveRoutesReturns(result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.removeRoutesMutex.Lock()
	defer fake.removeRoutesMutex.Unlock()
	fake.RemoveRoutesStub = nil
	fake.removeRoutesReturns = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) RemoveRoutesReturnsOnCall(i int, result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.removeRoutesMutex.Lock()
	defer fake.removeRoutesMutex.Unlock()
	fake.RemoveRoutesStub = nil
	if fake.removeRoutesReturnsOnCall == nil {
		fake.removeRoutesReturnsOnCall = make(map[int]struct {
			result1 routingtable.TCPRouteMappings
			result2 routingtable.MessagesToEmit
		})
	}
	fake.removeRoutesReturnsOnCall[i] = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) SetRoutes(arg1 lager.Logger, arg2 *models.DesiredLRP, arg3 *models.DesiredLRP) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit) {
	fake.setRoutesMutex.Lock()
	ret, specificReturn := fake.setRoutesReturnsOnCall[len(fake.setRoutesArgsForCall)]
	fake.setRoutesArgsForCall = append(fake.setRoutesArgsForCall, struct {
		arg1 lager.Logger
		arg2 *models.DesiredLRP
		arg3 *models.DesiredLRP
	}{arg1, arg2, arg3})
	fake.recordInvocation("SetRoutes", []interface{}{arg1, arg2, arg3})
	fake.setRoutesMutex.Unlock()
	if fake.SetRoutesStub != nil {
		return fake.SetRoutesStub(arg1, arg2, arg3)
	}
	if specificReturn {
		return ret.result1, ret.result2
	}
	fakeReturns := fake.setRoutesReturns
	return fakeReturns.result1, fakeReturns.result2
}

func (fake *FakeRoutingTable) SetRoutesCallCount() int {
	fake.setRoutesMutex.RLock()
	defer fake.setRoutesMutex.RUnlock()
	return len(fake.setRoutesArgsForCall)
}

func (fake *FakeRoutingTable) SetRoutesCalls(stub func(lager.Logger, *models.DesiredLRP, *models.DesiredLRP) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)) {
	fake.setRoutesMutex.Lock()
	defer fake.setRoutesMutex.Unlock()
	fake.SetRoutesStub = stub
}

func (fake *FakeRoutingTable) SetRoutesArgsForCall(i int) (lager.Logger, *models.DesiredLRP, *models.DesiredLRP) {
	fake.setRoutesMutex.RLock()
	defer fake.setRoutesMutex.RUnlock()
	argsForCall := fake.setRoutesArgsForCall[i]
	return argsForCall.arg1, argsForCall.arg2, argsForCall.arg3
}

func (fake *FakeRoutingTable) SetRoutesReturns(result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.setRoutesMutex.Lock()
	defer fake.setRoutesMutex.Unlock()
	fake.SetRoutesStub = nil
	fake.setRoutesReturns = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) SetRoutesReturnsOnCall(i int, result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.setRoutesMutex.Lock()
	defer fake.setRoutesMutex.Unlock()
	fake.SetRoutesStub = nil
	if fake.setRoutesReturnsOnCall == nil {
		fake.setRoutesReturnsOnCall = make(map[int]struct {
			result1 routingtable.TCPRouteMappings
			result2 routingtable.MessagesToEmit
		})
	}
	fake.setRoutesReturnsOnCall[i] = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) Swap(arg1 lager.Logger, arg2 routingtable.RoutingTable, arg3 models.DomainSet) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit) {
	fake.swapMutex.Lock()
	ret, specificReturn := fake.swapReturnsOnCall[len(fake.swapArgsForCall)]
	fake.swapArgsForCall = append(fake.swapArgsForCall, struct {
		arg1 lager.Logger
		arg2 routingtable.RoutingTable
		arg3 models.DomainSet
	}{arg1, arg2, arg3})
	fake.recordInvocation("Swap", []interface{}{arg1, arg2, arg3})
	fake.swapMutex.Unlock()
	if fake.SwapStub != nil {
		return fake.SwapStub(arg1, arg2, arg3)
	}
	if specificReturn {
		return ret.result1, ret.result2
	}
	fakeReturns := fake.swapReturns
	return fakeReturns.result1, fakeReturns.result2
}

func (fake *FakeRoutingTable) SwapCallCount() int {
	fake.swapMutex.RLock()
	defer fake.swapMutex.RUnlock()
	return len(fake.swapArgsForCall)
}

func (fake *FakeRoutingTable) SwapCalls(stub func(lager.Logger, routingtable.RoutingTable, models.DomainSet) (routingtable.TCPRouteMappings, routingtable.MessagesToEmit)) {
	fake.swapMutex.Lock()
	defer fake.swapMutex.Unlock()
	fake.SwapStub = stub
}

func (fake *FakeRoutingTable) SwapArgsForCall(i int) (lager.Logger, routingtable.RoutingTable, models.DomainSet) {
	fake.swapMutex.RLock()
	defer fake.swapMutex.RUnlock()
	argsForCall := fake.swapArgsForCall[i]
	return argsForCall.arg1, argsForCall.arg2, argsForCall.arg3
}

func (fake *FakeRoutingTable) SwapReturns(result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.swapMutex.Lock()
	defer fake.swapMutex.Unlock()
	fake.SwapStub = nil
	fake.swapReturns = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) SwapReturnsOnCall(i int, result1 routingtable.TCPRouteMappings, result2 routingtable.MessagesToEmit) {
	fake.swapMutex.Lock()
	defer fake.swapMutex.Unlock()
	fake.SwapStub = nil
	if fake.swapReturnsOnCall == nil {
		fake.swapReturnsOnCall = make(map[int]struct {
			result1 routingtable.TCPRouteMappings
			result2 routingtable.MessagesToEmit
		})
	}
	fake.swapReturnsOnCall[i] = struct {
		result1 routingtable.TCPRouteMappings
		result2 routingtable.MessagesToEmit
	}{result1, result2}
}

func (fake *FakeRoutingTable) TCPAssociationsCount() int {
	fake.tCPAssociationsCountMutex.Lock()
	ret, specificReturn := fake.tCPAssociationsCountReturnsOnCall[len(fake.tCPAssociationsCountArgsForCall)]
	fake.tCPAssociationsCountArgsForCall = append(fake.tCPAssociationsCountArgsForCall, struct {
	}{})
	fake.recordInvocation("TCPAssociationsCount", []interface{}{})
	fake.tCPAssociationsCountMutex.Unlock()
	if fake.TCPAssociationsCountStub != nil {
		return fake.TCPAssociationsCountStub()
	}
	if specificReturn {
		return ret.result1
	}
	fakeReturns := fake.tCPAssociationsCountReturns
	return fakeReturns.result1
}

func (fake *FakeRoutingTable) TCPAssociationsCountCallCount() int {
	fake.tCPAssociationsCountMutex.RLock()
	defer fake.tCPAssociationsCountMutex.RUnlock()
	return len(fake.tCPAssociationsCountArgsForCall)
}

func (fake *FakeRoutingTable) TCPAssociationsCountCalls(stub func() int) {
	fake.tCPAssociationsCountMutex.Lock()
	defer fake.tCPAssociationsCountMutex.Unlock()
	fake.TCPAssociationsCountStub = stub
}

func (fake *FakeRoutingTable) TCPAssociationsCountReturns(result1 int) {
	fake.tCPAssociationsCountMutex.Lock()
	defer fake.tCPAssociationsCountMutex.Unlock()
	fake.TCPAssociationsCountStub = nil
	fake.tCPAssociationsCountReturns = struct {
		result1 int
	}{result1}
}

func (fake *FakeRoutingTable) TCPAssociationsCountReturnsOnCall(i int, result1 int) {
	fake.tCPAssociationsCountMutex.Lock()
	defer fake.tCPAssociationsCountMutex.Unlock()
	fake.TCPAssociationsCountStub = nil
	if fake.tCPAssociationsCountReturnsOnCall == nil {
		fake.tCPAssociationsCountReturnsOnCall = make(map[int]struct {
			result1 int
		})
	}
	fake.tCPAssociationsCountReturnsOnCall[i] = struct {
		result1 int
	}{result1}
}

func (fake *FakeRoutingTable) TableSize() int {
	fake.tableSizeMutex.Lock()
	ret, specificReturn := fake.tableSizeReturnsOnCall[len(fake.tableSizeArgsForCall)]
	fake.tableSizeArgsForCall = append(fake.tableSizeArgsForCall, struct {
	}{})
	fake.recordInvocation("TableSize", []interface{}{})
	fake.tableSizeMutex.Unlock()
	if fake.TableSizeStub != nil {
		return fake.TableSizeStub()
	}
	if specificReturn {
		return ret.result1
	}
	fakeReturns := fake.tableSizeReturns
	return fakeReturns.result1
}

func (fake *FakeRoutingTable) TableSizeCallCount() int {
	fake.tableSizeMutex.RLock()
	defer fake.tableSizeMutex.RUnlock()
	return len(fake.tableSizeArgsForCall)
}

func (fake *FakeRoutingTable) TableSizeCalls(stub func() int) {
	fake.tableSizeMutex.Lock()
	defer fake.tableSizeMutex.Unlock()
	fake.TableSizeStub = stub
}

func (fake *FakeRoutingTable) TableSizeReturns(result1 int) {
	fake.tableSizeMutex.Lock()
	defer fake.tableSizeMutex.Unlock()
	fake.TableSizeStub = nil
	fake.tableSizeReturns = struct {
		result1 int
	}{result1}
}

func (fake *FakeRoutingTable) TableSizeReturnsOnCall(i int, result1 int) {
	fake.tableSizeMutex.Lock()
	defer fake.tableSizeMutex.Unlock()
	fake.TableSizeStub = nil
	if fake.tableSizeReturnsOnCall == nil {
		fake.tableSizeReturnsOnCall = make(map[int]struct {
			result1 int
		})
	}
	fake.tableSizeReturnsOnCall[i] = struct {
		result1 int
	}{result1}
}

func (fake *FakeRoutingTable) Invocations() map[string][][]interface{} {
	fake.invocationsMutex.RLock()
	defer fake.invocationsMutex.RUnlock()
	fake.addEndpointMutex.RLock()
	defer fake.addEndpointMutex.RUnlock()
	fake.getExternalRoutingEventsMutex.RLock()
	defer fake.getExternalRoutingEventsMutex.RUnlock()
	fake.getInternalRoutingEventsMutex.RLock()
	defer fake.getInternalRoutingEventsMutex.RUnlock()
	fake.hTTPAssociationsCountMutex.RLock()
	defer fake.hTTPAssociationsCountMutex.RUnlock()
	fake.hasExternalRoutesMutex.RLock()
	defer fake.hasExternalRoutesMutex.RUnlock()
	fake.internalAssociationsCountMutex.RLock()
	defer fake.internalAssociationsCountMutex.RUnlock()
	fake.removeEndpointMutex.RLock()
	defer fake.removeEndpointMutex.RUnlock()
	fake.removeRoutesMutex.RLock()
	defer fake.removeRoutesMutex.RUnlock()
	fake.setRoutesMutex.RLock()
	defer fake.setRoutesMutex.RUnlock()
	fake.swapMutex.RLock()
	defer fake.swapMutex.RUnlock()
	fake.tCPAssociationsCountMutex.RLock()
	defer fake.tCPAssociationsCountMutex.RUnlock()
	fake.tableSizeMutex.RLock()
	defer fake.tableSizeMutex.RUnlock()
	copiedInvocations := map[string][][]interface{}{}
	for key, value := range fake.invocations {
		copiedInvocations[key] = value
	}
	return copiedInvocations
}

func (fake *FakeRoutingTable) recordInvocation(key string, args []interface{}) {
	fake.invocationsMutex.Lock()
	defer fake.invocationsMutex.Unlock()
	if fake.invocations == nil {
		fake.invocations = map[string][][]interface{}{}
	}
	if fake.invocations[key] == nil {
		fake.invocations[key] = [][]interface{}{}
	}
	fake.invocations[key] = append(fake.invocations[key], args)
}

var _ routingtable.RoutingTable = new(FakeRoutingTable)
