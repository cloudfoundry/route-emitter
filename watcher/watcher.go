package watcher

import (
	"os"
	"time"

	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/metric"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/pivotal-golang/lager"
)

var (
	routesRegistered   = metric.Counter("RoutesRegistered")
	routesUnregistered = metric.Counter("RoutesUnregistered")
)

type Watcher struct {
	bbs     bbs.RouteEmitterBBS
	table   routing_table.RoutingTableInterface
	emitter nats_emitter.NATSEmitterInterface
	logger  lager.Logger
}

func NewWatcher(
	bbs bbs.RouteEmitterBBS,
	table routing_table.RoutingTableInterface,
	emitter nats_emitter.NATSEmitterInterface,
	logger lager.Logger,
) *Watcher {
	return &Watcher{
		bbs:     bbs,
		table:   table,
		emitter: emitter,
		logger:  logger.Session("watcher"),
	}
}

func (watcher *Watcher) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	desiredLRPChanges, _, desiredErrors := watcher.bbs.WatchForDesiredLRPChanges()
	actualLRPChanges, _, actualErrors := watcher.bbs.WatchForActualLRPChanges()

	close(ready)

	var reWatchActual <-chan time.Time
	var reWatchDesired <-chan time.Time

	for {
		select {
		case desiredChange, ok := <-desiredLRPChanges:
			if !ok {
				desiredLRPChanges = nil
				break
			}

			watcher.handleDesiredChange(desiredChange)

		case actualChange, ok := <-actualLRPChanges:
			if !ok {
				actualLRPChanges = nil
				break
			}

			watcher.handleActualChange(actualChange)

		case err := <-desiredErrors:
			watcher.logger.Error("desired-watch-failed", err)

			reWatchDesired = time.After(3 * time.Second)
			desiredLRPChanges = nil
			desiredErrors = nil

		case err := <-actualErrors:
			watcher.logger.Error("actual-watch-failed", err)

			reWatchActual = time.After(3 * time.Second)
			actualLRPChanges = nil
			actualErrors = nil

		case <-reWatchActual:
			actualLRPChanges, _, actualErrors = watcher.bbs.WatchForActualLRPChanges()
			reWatchActual = nil

		case <-reWatchDesired:
			desiredLRPChanges, _, desiredErrors = watcher.bbs.WatchForDesiredLRPChanges()
			reWatchDesired = nil

		case <-signals:
			watcher.logger.Info("stopping")
			return nil
		}
	}

	return nil
}

func (watcher *Watcher) handleActualChange(change models.ActualLRPChange) {
	watcher.logger.Info("detected-actual-change", lager.Data{
		"actual-change": change,
	})

	var messagesToEmit routing_table.MessagesToEmit
	if change.After == nil {
		if change.Before != nil {
			container, err := routing_table.ContainerFromActual(*change.Before)
			if err != nil {
				return
			}

			messagesToEmit = watcher.table.RemoveContainer(change.Before.ProcessGuid, container)
		}
	} else {
		container, err := routing_table.ContainerFromActual(*change.After)
		if err != nil {
			return
		}

		messagesToEmit = watcher.table.AddOrUpdateContainer(change.After.ProcessGuid, container)
	}

	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}

func (watcher *Watcher) handleDesiredChange(change models.DesiredLRPChange) {
	watcher.logger.Info("detected-desired-change", lager.Data{
		"desired-change": change,
	})

	var messagesToEmit routing_table.MessagesToEmit
	if change.After == nil {
		if change.Before != nil {
			messagesToEmit = watcher.table.RemoveRoutes(change.Before.ProcessGuid)
		}
	} else {
		messagesToEmit = watcher.table.SetRoutes(change.After.ProcessGuid, change.After.Routes...)
	}

	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}
