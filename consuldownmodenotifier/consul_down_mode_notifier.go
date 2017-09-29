package consuldownmodenotifier

import (
	"os"
	"time"

	"code.cloudfoundry.org/clock"
	loggingclient "code.cloudfoundry.org/diego-logging-client"
	"code.cloudfoundry.org/lager"
)

const (
	consulDownMetric = "ConsulDownMode"
)

type ConsulDownModeNotifier struct {
	logger       lager.Logger
	value        int
	clock        clock.Clock
	interval     time.Duration
	metronClient loggingclient.IngressClient
}

func NewConsulDownModeNotifier(
	logger lager.Logger,
	value int,
	clock clock.Clock,
	interval time.Duration,
	metronClient loggingclient.IngressClient,
) *ConsulDownModeNotifier {
	return &ConsulDownModeNotifier{
		logger: logger, value: value, clock: clock, interval: interval, metronClient: metronClient,
	}
}

func (p *ConsulDownModeNotifier) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	logger := p.logger.Session("consul-down-mode-notifier")
	logger.Info("starting")
	defer logger.Info("finished")
	retryTimer := p.clock.NewTimer(0)

	close(ready)

	for {
		select {
		case <-signals:
			logger.Info("received-signal")
			return nil
		case <-retryTimer.C():
			err := p.metronClient.SendMetric(consulDownMetric, p.value)
			if err != nil {
				p.logger.Error("cannot-send-consul-down-metric", err)
			}
			retryTimer.Reset(p.interval)
		}
	}
}
