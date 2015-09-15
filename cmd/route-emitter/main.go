package main

import (
	"flag"
	"os"
	"time"

	"github.com/cloudfoundry-incubator/bbs"
	"github.com/cloudfoundry-incubator/cf-debug-server"
	"github.com/cloudfoundry-incubator/cf-lager"
	"github.com/cloudfoundry-incubator/cf_http"
	"github.com/cloudfoundry-incubator/consuladapter"
	"github.com/cloudfoundry-incubator/locket"
	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/route-emitter/syncer"
	"github.com/cloudfoundry-incubator/route-emitter/watcher"
	"github.com/cloudfoundry/dropsonde"
	"github.com/cloudfoundry/gunk/diegonats"
	"github.com/cloudfoundry/gunk/workpool"
	"github.com/nu7hatch/gouuid"
	"github.com/pivotal-golang/clock"
	"github.com/pivotal-golang/lager"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"
	"github.com/tedsuo/ifrit/sigmon"
)

var bbsAddress = flag.String(
	"bbsAddress",
	"",
	"Address of the BBS API Server",
)

var sessionName = flag.String(
	"sessionName",
	"route-emitter",
	"consul session name",
)

var consulCluster = flag.String(
	"consulCluster",
	"",
	"comma-separated list of consul server URLs (scheme://ip:port)",
)

var lockTTL = flag.Duration(
	"lockTTL",
	locket.LockTTL,
	"TTL for service lock",
)

var lockRetryInterval = flag.Duration(
	"lockRetryInterval",
	locket.RetryInterval,
	"interval to wait before retrying a failed lock acquisition",
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

var communicationTimeout = flag.Duration(
	"communicationTimeout",
	10*time.Second,
	"Timeout applied to all HTTP requests.",
)

const (
	dropsondeDestination = "localhost:3457"
	dropsondeOrigin      = "route_emitter"
)

func main() {
	cf_debug_server.AddFlags(flag.CommandLine)
	cf_lager.AddFlags(flag.CommandLine)
	flag.Parse()

	cf_http.Initialize(*communicationTimeout)

	logger, reconfigurableSink := cf_lager.New(*sessionName)
	natsClient := diegonats.NewClient()
	clock := clock.NewClock()
	syncer := syncer.NewSyncer(clock, *syncInterval, natsClient, logger)

	initializeDropsonde(logger)

	natsClientRunner := diegonats.NewClientRunner(*natsAddresses, *natsUsername, *natsPassword, logger, natsClient)

	bbsClient := bbs.NewClient(*bbsAddress)
	table := initializeRoutingTable()
	emitter := initializeNatsEmitter(natsClient, logger)
	watcher := ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
		return watcher.NewWatcher(bbsClient, clock, table, emitter, syncer.Events(), logger).Run(signals, ready)
	})

	syncRunner := ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
		return syncer.Run(signals, ready)
	})

	lockMaintainer := initializeLockMaintainer(logger, *consulCluster, *sessionName, *lockTTL, *lockRetryInterval, clock)

	members := grouper.Members{
		{"lock-maintainer", lockMaintainer},
		{"nats-client", natsClientRunner},
		{"watcher", watcher},
		{"syncer", syncRunner},
	}

	if dbgAddr := cf_debug_server.DebugAddress(flag.CommandLine); dbgAddr != "" {
		members = append(grouper.Members{
			{"debug-server", cf_debug_server.Runner(dbgAddr, reconfigurableSink)},
		}, members...)
	}

	group := grouper.NewOrdered(os.Interrupt, members)

	monitor := ifrit.Invoke(sigmon.New(group))

	logger.Info("started")

	err := <-monitor.Wait()
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

func initializeNatsEmitter(natsClient diegonats.NATSClient, logger lager.Logger) nats_emitter.NATSEmitter {
	workPool, err := workpool.NewWorkPool(20)
	if err != nil {
		logger.Fatal("failed-to-construct-nats-emitter-workpool", err, lager.Data{"num-workers": 20}) // should never happen
	}

	return nats_emitter.New(natsClient, workPool, logger)
}

func initializeRoutingTable() routing_table.RoutingTable {
	return routing_table.NewTable()
}

func initializeLockMaintainer(
	logger lager.Logger,
	consulCluster, sessionName string,
	lockTTL, lockRetryInterval time.Duration,
	clock clock.Clock,
) ifrit.Runner {
	client, err := consuladapter.NewClient(consulCluster)
	if err != nil {
		logger.Fatal("new-client-failed", err)
	}
	sessionMgr := consuladapter.NewSessionManager(client)
	consulSession, err := consuladapter.NewSession(sessionName, lockTTL, client, sessionMgr)
	if err != nil {
		logger.Fatal("consul-session-failed", err)
	}

	uuid, err := uuid.NewV4()
	if err != nil {
		logger.Fatal("Couldn't generate uuid", err)
	}

	locketClient := locket.NewClient(consulSession, clock, logger)

	return locketClient.NewRouteEmitterLock(uuid.String(), lockRetryInterval)
}
