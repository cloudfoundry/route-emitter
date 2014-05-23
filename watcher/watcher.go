package watcher

import (
	"encoding/json"
	"os"

	"github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry/gibson"
	"github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/storeadapter"
	"github.com/cloudfoundry/yagnats"
)

type Watcher struct {
	bbs        bbs.LRPRouterBBS
	natsClient yagnats.NATSClient
	logger     *gosteno.Logger
}

func NewWatcher(bbs bbs.LRPRouterBBS, natsClient yagnats.NATSClient, logger *gosteno.Logger) *Watcher {
	return &Watcher{
		bbs:        bbs,
		natsClient: natsClient,
		logger:     logger,
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

				actuals, err := watcher.bbs.GetRunningActualLRPsByProcessGuid(desiredChange.After.ProcessGuid)
				if err != nil {
					if err == storeadapter.ErrorKeyNotFound {
						break
					}

					continue
				}

				var oldRoutes, newRoutes []string
				if desiredChange.Before != nil {
					oldRoutes = desiredChange.Before.Routes
				}
				if desiredChange.After != nil {
					newRoutes = desiredChange.After.Routes
				}

				added := subtract(newRoutes, oldRoutes)
				removed := subtract(oldRoutes, newRoutes)

				for _, actual := range actuals {
					watcher.register(added, actual)
					watcher.unregister(removed, actual)
				}

			case actualChange, ok := <-actualLRPChanges:
				if !ok {
					break InnerLoop
				}

				if actualChange.After == nil {
					continue
				}

				actual := *actualChange.After

				if actual.State != models.LRPStateRunning {
					continue
				}

				desired, err := watcher.bbs.GetDesiredLRPByProcessGuid(actual.ProcessGuid)
				if err != nil {
					if err == storeadapter.ErrorKeyNotFound {
						break
					}

					continue
				}

				watcher.register(desired.Routes, actual)

			case err := <-desiredErrors:
				watcher.logger.Errord(map[string]interface{}{
					"error": err.Error(),
				}, "route-emitter.desired-watch-failed")

				desiredLRPChanges, _, desiredErrors = watcher.bbs.WatchForDesiredLRPChanges()

				break InnerLoop

			case err := <-actualErrors:
				watcher.logger.Errord(map[string]interface{}{
					"error": err.Error(),
				}, "route-emitter.actual-watch-failed")

				actualLRPChanges, _, actualErrors = watcher.bbs.WatchForActualLRPChanges()

				break InnerLoop

			case <-signals:
				watcher.logger.Info("route-emitter.stopping-watch")
				return nil
			}
		}
	}

	return nil
}

func subtract(subtractee, subtractend []string) []string {
	result := []string{}
	for _, x := range subtractee {
		removeIt := false
		for _, y := range subtractend {
			if x == y {
				removeIt = true
				break
			}
		}

		if !removeIt {
			result = append(result, x)
		}
	}
	return result
}

func (watcher *Watcher) register(routes []string, actual models.LRP) {
	watcher.updateRoutes("router.register", routes, actual)
}

func (watcher *Watcher) unregister(routes []string, actual models.LRP) {
	watcher.updateRoutes("router.unregister", routes, actual)
}

func (watcher *Watcher) updateRoutes(subject string, routes []string, actual models.LRP) {
	message := gibson.RegistryMessage{
		URIs: routes,
		Host: actual.Host,
		Port: int(actual.Ports[0].HostPort),
	}

	payload, err := json.Marshal(message)
	if err != nil {
		panic(err)
	}

	err = watcher.natsClient.Publish(subject, payload)
	if err != nil {
		watcher.logger.Warnd(map[string]interface{}{
			"error":   err,
			"subject": subject,
		}, "watcher.route-update.failed")
	}
}
