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
	"github.com/cloudfoundry-incubator/runtime-schema/bbs/lock_bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/heartbeater"
	"github.com/cloudfoundry/dropsonde"
	"github.com/cloudfoundry/gunk/diegonats"
	"github.com/cloudfoundry/gunk/timeprovider"
	"github.com/cloudfoundry/gunk/workpool"
	"github.com/cloudfoundry/storeadapter/etcdstoreadapter"
	"github.com/nu7hatch/gouuid"
	"github.com/pivotal-golang/lager"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"
	"github.com/tedsuo/ifrit/restart"
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

var heartbeatInterval = flag.Duration(
	"heartbeatInterval",
	lock_bbs.HEARTBEAT_INTERVAL,
	"the interval between heartbeats to the lock",
)

const (
	dropsondeDestination = "localhost:3457"
	dropsondeOrigin      = "route_emitter"
)

func main() {
	flag.Parse()

	logger := cf_lager.New("route-emitter")

	cf_debug_server.Run()

	initializeDropsonde(logger)
	bbs := initializeBbs(logger)
	table := initializeRoutingTable()

	natsClient := diegonats.NewClient()
	natsClientRunner := diegonats.NewClientRunner(*natsAddresses, *natsUsername, *natsPassword, logger, natsClient)

	emitter := initializeNatsEmitter(natsClient, logger)
	watcher := ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
		return watcher.NewWatcher(bbs, table, emitter, logger).Run(signals, ready)
	})

	syncer := ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
		return syncer.NewSyncer(bbs, table, emitter, *syncInterval, natsClient, logger).Run(signals, ready)
	})

	uuid, err := uuid.NewV4()
	if err != nil {
		logger.Fatal("Couldn't generate uuid", err)
	}

	heartbeat := bbs.NewRouteEmitterLock(uuid.String(), *heartbeatInterval)

	group := grouper.NewOrdered(os.Interrupt, grouper.Members{
		{"heartbeater", restart.OnError(heartbeat, heartbeater.ErrStoreUnavailable)},
		{"nats-client", natsClientRunner},
		{"watcher", watcher},
		{"syncer", syncer},
	})

	monitor := ifrit.Invoke(sigmon.New(group))

	logger.Info("started")

	err = <-monitor.Wait()
	if err != nil {
		logger.Error("exited-with-failure", err)
		os.Exit(1)
	}

	logger.Info("exited")
}

func initializeDropsonde(logger lager.Logger) {
	err := dropsonde.Initialize(dropsondeDestination, dropsondeOrigin)
	if err != nil {
		logger.Error("failed to initialize dropsonde: %v", err)
	}
}

func initializeNatsEmitter(natsClient diegonats.NATSClient, logger lager.Logger) *nats_emitter.NATSEmitter {
	pool := workpool.New(10, 10, workpool.DefaultAround)
	return nats_emitter.New(natsClient, pool, logger)
}

func initializeRoutingTable() routing_table.RoutingTable {
	return routing_table.New()
}

func initializeBbs(logger lager.Logger) Bbs.RouteEmitterBBS {
	etcdAdapter := etcdstoreadapter.NewETCDStoreAdapter(
		strings.Split(*etcdCluster, ","),
		workpool.NewWorkPool(10),
	)

	err := etcdAdapter.Connect()
	if err != nil {
		logger.Fatal("failed-to-connect-to-etcd", err)
	}

	return Bbs.NewRouteEmitterBBS(etcdAdapter, timeprovider.NewTimeProvider(), logger)
}
