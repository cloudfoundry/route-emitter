package syncer

import (
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
)

type SyncEvents struct {
	Begin chan SyncBegin
	End   chan SyncEnd
	Emit  chan struct{}
}

type SyncBegin struct {
	Ack chan struct{}
}

type SyncEnd struct {
	Table    routing_table.RoutingTable
	Callback func(routing_table.RoutingTable)
}
