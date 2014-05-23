package watcher

import (
	"encoding/json"
	"log"
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
	actualLRPs, _, actualErrors := watcher.bbs.WatchForActualLongRunningProcesses()

	close(ready)

	for {
	InnerLoop:
		for {
			select {
			case desiredChange, ok := <-desiredLRPChanges:
				if !ok {
					break InnerLoop
				}

				actuals, err := watcher.bbs.GetActualLRPs(desiredChange.After.ProcessGuid)
				if err != nil {
					if err == storeadapter.ErrorKeyNotFound {
						break
					}
					panic("TESTME - " + err.Error())
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
					err = watcher.register(added, actual)
					if err != nil {
						panic("TESTME:" + err.Error())
					}

					err = watcher.unregister(removed, actual)
					if err != nil {
						panic("TESTME:" + err.Error())
					}
				}

				// TODO: check for removed routes (prev vs. current node value)

			case actual, ok := <-actualLRPs:
				if !ok {
					break InnerLoop
				}

				desired, err := watcher.bbs.GetDesiredLRP(actual.ProcessGuid)
				if err != nil {
					if err == storeadapter.ErrorKeyNotFound {
						break
					}
					panic("TESTME - " + err.Error())
				}

				err = watcher.register(desired.Routes, actual)
				if err != nil {
					panic("TESTME:" + err.Error())
				}

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

				actualLRPs, _, actualErrors = watcher.bbs.WatchForActualLongRunningProcesses()

				break InnerLoop

			case sig := <-signals:
				log.Println("CAUGHT SIGNAL", sig)
				//if watcher.shouldStop(sig) {
				//watcher.logger.Info("route-emitter.stopping-watch")
				//close(stopChan)
				//return nil
				//}
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

func (watcher *Watcher) register(routes []string, actual models.LRP) error {
	return watcher.updateRoutes("router.register", routes, actual)
}

func (watcher *Watcher) unregister(routes []string, actual models.LRP) error {
	return watcher.updateRoutes("router.unregister", routes, actual)
}

func (watcher *Watcher) updateRoutes(subject string, routes []string, actual models.LRP) error {
	message := gibson.RegistryMessage{
		URIs: routes,
		Host: actual.Host,
		Port: int(actual.Ports[0].HostPort),
	}

	payload, err := json.Marshal(message)
	if err != nil {
		panic(err)
	}

	return watcher.natsClient.Publish(subject, payload)
}
