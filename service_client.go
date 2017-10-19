package route_emitter

import (
	"time"

	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/consuladapter"
	loggingclient "code.cloudfoundry.org/diego-logging-client"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/locket"
	"github.com/tedsuo/ifrit"
)

const RouteEmitterLockSchemaKey = "route_emitter_lock"

func RouteEmitterLockSchemaPath() string {
	return locket.LockSchemaPath(RouteEmitterLockSchemaKey)
}

type ServiceClient interface {
	NewRouteEmitterLockRunner(logger lager.Logger, bulkerID string, retryInterval, lockTTL time.Duration, metronClient loggingclient.IngressClient) ifrit.Runner
}

type serviceClient struct {
	consulClient consuladapter.Client
	clock        clock.Clock
}

func NewServiceClient(consulClient consuladapter.Client, clock clock.Clock) ServiceClient {
	return serviceClient{
		consulClient: consulClient,
		clock:        clock,
	}
}

func (c serviceClient) NewRouteEmitterLockRunner(logger lager.Logger, emitterID string, retryInterval, lockTTL time.Duration, metronClient loggingclient.IngressClient) ifrit.Runner {
	return locket.NewLock(logger, c.consulClient, RouteEmitterLockSchemaPath(), []byte(emitterID), c.clock, retryInterval, lockTTL, locket.WithMetronClient(metronClient))
}
