package route_emitter

import (
	"time"

	"github.com/cloudfoundry-incubator/consuladapter"
	"github.com/cloudfoundry-incubator/locket"
	"github.com/pivotal-golang/clock"
	"github.com/pivotal-golang/lager"
	"github.com/tedsuo/ifrit"
)

const RouteEmitterLockSchemaKey = "route_emitter_lock"

func RouteEmitterLockSchemaPath() string {
	return locket.LockSchemaPath(RouteEmitterLockSchemaKey)
}

type ServiceClient interface {
	NewRouteEmitterLockRunner(logger lager.Logger, bulkerID string, retryInterval time.Duration) ifrit.Runner
}

type serviceClient struct {
	session *consuladapter.Session
	clock   clock.Clock
}

func NewServiceClient(session *consuladapter.Session, clock clock.Clock) ServiceClient {
	return serviceClient{session, clock}
}

func (c serviceClient) NewRouteEmitterLockRunner(logger lager.Logger, emitterID string, retryInterval time.Duration) ifrit.Runner {
	return locket.NewLock(c.session, RouteEmitterLockSchemaPath(), []byte(emitterID), c.clock, retryInterval, logger)
}
