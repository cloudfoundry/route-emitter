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
	table   routing_table.RoutingTable
	emitter nats_emitter.NATSEmitterInterface
	logger  lager.Logger
}

func NewWatcher(
	bbs bbs.RouteEmitterBBS,
	table routing_table.RoutingTable,
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
	desiredLRPCreateOrUpdates, desiredLRPDeletes, desiredErrors := watcher.bbs.WatchForDesiredLRPChanges(watcher.logger)
	actualLRPCreateOrUpdates, actualLRPDeletes, actualErrors := watcher.bbs.WatchForActualLRPChanges(watcher.logger)

	close(ready)

	var reWatchActual <-chan time.Time
	var reWatchDesired <-chan time.Time

	for {
		select {
		case desiredCreateOrUpdate, ok := <-desiredLRPCreateOrUpdates:
			if !ok {
				desiredLRPCreateOrUpdates = nil
				desiredLRPDeletes = nil
				break
			}

			watcher.handleDesiredCreateOrUpdate(desiredCreateOrUpdate)

		case desiredDelete, ok := <-desiredLRPDeletes:
			if !ok {
				desiredLRPCreateOrUpdates = nil
				desiredLRPDeletes = nil
				break
			}

			watcher.handleDesiredDelete(desiredDelete)

		case actualCreateOrUpdate, ok := <-actualLRPCreateOrUpdates:
			if !ok {
				actualLRPCreateOrUpdates = nil
				actualLRPDeletes = nil
				break
			}

			watcher.handleActualCreateOrUpdate(actualCreateOrUpdate)

		case actualDelete, ok := <-actualLRPDeletes:
			if !ok {
				actualLRPCreateOrUpdates = nil
				actualLRPDeletes = nil
				break
			}

			watcher.handleActualDelete(actualDelete)

		case err := <-desiredErrors:
			watcher.logger.Error("desired-watch-failed", err)

			reWatchDesired = time.After(3 * time.Second)
			desiredLRPCreateOrUpdates = nil
			desiredLRPDeletes = nil
			desiredErrors = nil

		case err := <-actualErrors:
			watcher.logger.Error("actual-watch-failed", err)

			reWatchActual = time.After(3 * time.Second)
			actualLRPCreateOrUpdates = nil
			actualLRPDeletes = nil
			actualErrors = nil

		case <-reWatchDesired:
			desiredLRPCreateOrUpdates, desiredLRPDeletes, desiredErrors = watcher.bbs.WatchForDesiredLRPChanges(watcher.logger)
			reWatchDesired = nil

		case <-reWatchActual:
			actualLRPCreateOrUpdates, actualLRPDeletes, actualErrors = watcher.bbs.WatchForActualLRPChanges(watcher.logger)
			reWatchActual = nil

		case <-signals:
			watcher.logger.Info("stopping")
			return nil
		}
	}

	return nil
}

func (watcher *Watcher) handleActualCreateOrUpdate(actualLRP models.ActualLRP) {
	watcher.logger.Info("handling-actual-change", lager.Data{
		"actual-lrp": actualLRP,
	})

	container, err := routing_table.ContainerFromActual(actualLRP)
	if err != nil {
		watcher.logger.Error("failed-to-extract-container-from-actual", err)
		return
	}

	messagesToEmit := watcher.table.AddOrUpdateContainer(actualLRP.ProcessGuid, container)
	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}

func (watcher *Watcher) handleActualDelete(actualLRP models.ActualLRP) {
	watcher.logger.Info("handling-actual-delete", lager.Data{
		"actual-lrp": actualLRP,
	})

	container, err := routing_table.ContainerFromActual(actualLRP)
	if err != nil {
		return
	}

	messagesToEmit := watcher.table.RemoveContainer(actualLRP.ProcessGuid, container)
	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}

func (watcher *Watcher) handleDesiredCreateOrUpdate(desiredLRP models.DesiredLRP) {
	watcher.logger.Info("handling-desired-create-or-update", lager.Data{
		"desired-lrp": desiredLRP,
	})

	messagesToEmit := watcher.table.SetRoutes(desiredLRP.ProcessGuid, routing_table.Routes{
		URIs:    desiredLRP.Routes,
		LogGuid: desiredLRP.LogGuid,
	})

	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}

func (watcher *Watcher) handleDesiredDelete(desiredLRP models.DesiredLRP) {
	watcher.logger.Info("handling-desired-delete", lager.Data{
		"desired-lrp": desiredLRP,
	})

	messagesToEmit := watcher.table.RemoveRoutes(desiredLRP.ProcessGuid)

	watcher.emitter.Emit(messagesToEmit, &routesRegistered, &routesUnregistered)
}
