package main

import (
	"flag"
	"os"
	"strings"
	"time"

	"github.com/cloudfoundry-incubator/cf-debug-server"
	"github.com/cloudfoundry-incubator/cf-lager"
	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/route-emitter/syncer"
	"github.com/cloudfoundry-incubator/route-emitter/watcher"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	_ "github.com/cloudfoundry/dropsonde/autowire"
	"github.com/cloudfoundry/gunk/group_runner"
	"github.com/cloudfoundry/gunk/natsclientrunner"
	"github.com/cloudfoundry/gunk/timeprovider"
	"github.com/cloudfoundry/storeadapter/etcdstoreadapter"
	"github.com/cloudfoundry/storeadapter/workerpool"
	"github.com/cloudfoundry/yagnats"
	"github.com/pivotal-golang/lager"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/sigmon"
)

var etcdCluster = flag.String(
	"etcdCluster",
	"http://127.0.0.1:4001",
	"comma-separated list of etcd addresses (http://ip:port)",
)

var natsAddresses = flag.String(
	"natsAddresses",
	"127.0.0.1:4222",
	"comma-separated list of NATS addresses (ip:port)",
)

var natsUsername = flag.String(
	"natsUsername",
	"nats",
	"Username to connect to nats",
)

var natsPassword = flag.String(
	"natsPassword",
	"nats",
	"Password for nats user",
)

var syncInterval = flag.Duration(
	"syncInterval",
	time.Minute,
	"the interval between syncs of the routing table from etcd",
)

func main() {
	flag.Parse()

	logger := cf_lager.New("route-emitter")

	cf_debug_server.Run()

	bbs := initializeBbs(logger)
	table := initializeRoutingTable()

	var natsClient yagnats.NATSConn
	var emitter *nats_emitter.NATSEmitter
	natsClientRunner := natsclientrunner.New(*natsAddresses, *natsUsername, *natsPassword, logger, &natsClient)

	watcher := ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
		emitter = initializeNatsEmitter(natsClient, logger)
		return watcher.NewWatcher(bbs, table, emitter, logger).Run(signals, ready)
	})

	syncer := ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
		return syncer.NewSyncer(bbs, table, emitter, *syncInterval, natsClient, logger).Run(signals, ready)
	})

	group := group_runner.New([]group_runner.Member{
		{"nats-client", natsClientRunner},
		{"watcher", watcher},
		{"syncer", syncer},
	})

	monitor := ifrit.Envoke(sigmon.New(group))

	logger.Info("started")

	err := <-monitor.Wait()
	if err != nil {
		logger.Error("exited-with-failure", err)
		os.Exit(1)
	}

	logger.Info("exited")
}

func initializeNatsEmitter(natsClient yagnats.NATSConn, logger lager.Logger) *nats_emitter.NATSEmitter {
	return nats_emitter.New(natsClient, logger)
}

func initializeRoutingTable() *routing_table.RoutingTable {
	return routing_table.New()
}

func initializeBbs(logger lager.Logger) Bbs.RouteEmitterBBS {
	etcdAdapter := etcdstoreadapter.NewETCDStoreAdapter(
		strings.Split(*etcdCluster, ","),
		workerpool.NewWorkerPool(10),
	)

	err := etcdAdapter.Connect()
	if err != nil {
		logger.Fatal("failed-to-connect-to-etcd", err)
	}

	return Bbs.NewRouteEmitterBBS(etcdAdapter, timeprovider.NewTimeProvider(), logger)
}
