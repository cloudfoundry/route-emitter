package watcher

import (
	"os"
	"time"

	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry/gosteno"
)

type Watcher struct {
	bbs     bbs.LRPRouterBBS
	table   routing_table.RoutingTableInterface
	emitter nats_emitter.NATSEmitterInterface
	logger  *gosteno.Logger
}

func NewWatcher(bbs bbs.LRPRouterBBS, table routing_table.RoutingTableInterface, emitter nats_emitter.NATSEmitterInterface, logger *gosteno.Logger) *Watcher {
	return &Watcher{
		bbs:     bbs,
		table:   table,
		emitter: emitter,
		logger:  logger,
	}
}

func (watcher *Watcher) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	desiredLRPChanges, _, desiredErrors := watcher.bbs.WatchForDesiredLRPChanges()
	actualLRPChanges, _, actualErrors := watcher.bbs.WatchForActualLRPChanges()

	close(ready)

	for {
	InnerLoop:
		for {
			select {
			case desiredChange, ok := <-desiredLRPChanges:
				if !ok {
					break InnerLoop
				}

				watcher.logger.Infod(map[string]interface{}{
					"desired-change": desiredChange,
				}, "route-emitter.watcher.detected-desired-change")

				var messagesToEmit routing_table.MessagesToEmit
				if desiredChange.After == nil {
					if desiredChange.Before != nil {
						messagesToEmit = watcher.table.RemoveRoutes(desiredChange.Before.ProcessGuid)
					}
				} else {
					messagesToEmit = watcher.table.SetRoutes(desiredChange.After.ProcessGuid, desiredChange.After.Routes...)
				}

				watcher.emitter.Emit(messagesToEmit)

			case actualChange, ok := <-actualLRPChanges:
				if !ok {
					break InnerLoop
				}

				watcher.logger.Infod(map[string]interface{}{
					"actual-change": actualChange,
				}, "route-emitter.watcher.detected-actual-change")

				var messagesToEmit routing_table.MessagesToEmit
				if actualChange.After == nil {
					if actualChange.Before != nil {
						container, err := routing_table.ContainerFromActual(*actualChange.Before)
						if err != nil {
							continue
						}
						messagesToEmit = watcher.table.RemoveContainer(actualChange.Before.ProcessGuid, container)
					}
				} else {
					container, err := routing_table.ContainerFromActual(*actualChange.After)
					if err != nil {
						continue
					}
					messagesToEmit = watcher.table.AddOrUpdateContainer(actualChange.After.ProcessGuid, container)
				}

				watcher.emitter.Emit(messagesToEmit)

			case err := <-desiredErrors:
				watcher.logger.Errord(map[string]interface{}{
					"error": err.Error(),
				}, "route-emitter.watcher.desired-watch-failed")

				time.Sleep(3 * time.Second)

				desiredLRPChanges, _, desiredErrors = watcher.bbs.WatchForDesiredLRPChanges()

				break InnerLoop

			case err := <-actualErrors:
				watcher.logger.Errord(map[string]interface{}{
					"error": err.Error(),
				}, "route-emitter.watcher.actual-watch-failed")

				time.Sleep(3 * time.Second)

				actualLRPChanges, _, actualErrors = watcher.bbs.WatchForActualLRPChanges()

				break InnerLoop

			case <-signals:
				watcher.logger.Info("route-emitter.watcher.stopping")
				return nil
			}
		}
	}

	return nil
}
