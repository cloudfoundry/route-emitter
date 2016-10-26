package consuldownchecker

import (
	"os"
	"strings"
	"time"

	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/consuladapter"
	"code.cloudfoundry.org/lager"
)

type ConsulDownChecker struct {
	logger        lager.Logger
	clock         clock.Clock
	consulClient  consuladapter.Client
	retryInterval time.Duration
}

func NewConsulDownChecker(
	logger lager.Logger,
	clock clock.Clock,
	consulClient consuladapter.Client,
	retryInterval time.Duration,
) *ConsulDownChecker {
	return &ConsulDownChecker{
		logger: logger, clock: clock, consulClient: consulClient, retryInterval: retryInterval,
	}
}

func (c *ConsulDownChecker) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	logger := c.logger.Session("consul-down-checker")
	logger.Info("starting")
	defer logger.Info("finished")
	retryTimer := c.clock.NewTimer(0)

	hasLeader, err := c.checkForLeader(logger)
	if hasLeader || err != nil {
		return err
	}
	close(ready)

	for {
		select {
		case <-signals:
			logger.Info("received-signal")
			return nil
		case <-retryTimer.C():
			hasLeader, err := c.checkForLeader(logger)
			if hasLeader || err != nil {
				return err
			}
			logger.Info("still-down")
			retryTimer.Reset(c.retryInterval)
		}
	}
}

func (c *ConsulDownChecker) checkForLeader(logger lager.Logger) (bool, error) {
	leader, err := c.consulClient.Status().Leader()
	if err != nil && !strings.Contains(err.Error(), "Unexpected response code: 500") {
		logger.Error("failed-getting-leader", err)
		return false, err
	}

	if leader != "" {
		logger.Info("consul-has-leader")
		return true, nil
	}

	return false, nil
}
