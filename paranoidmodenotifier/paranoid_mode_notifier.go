package paranoidmodenotifier

import (
	"os"
	"time"

	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/runtimeschema/metric"
)

type ParanoidModeNofitier struct {
	logger   lager.Logger
	value    int
	clock    clock.Clock
	interval time.Duration
}

func NewParanoidModeNotifier(
	logger lager.Logger,
	value int,
	clock clock.Clock,
	interval time.Duration,
) *ParanoidModeNofitier {
	return &ParanoidModeNofitier{
		logger: logger, value: value, clock: clock, interval: interval,
	}
}

func (p *ParanoidModeNofitier) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	logger := p.logger.Session("paranoid-mode-notifier")
	logger.Info("starting")
	defer logger.Info("finished")
	retryTimer := p.clock.NewTimer(0)
	var paranoidMetric = metric.Metric("ParanoidMode")

	close(ready)

	for {
		select {
		case <-signals:
			logger.Info("received-signal")
			return nil
		case <-retryTimer.C():
			paranoidMetric.Send(p.value)
			retryTimer.Reset(p.interval)
		}
	}
}
