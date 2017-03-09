package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"code.cloudfoundry.org/bbs"
	"code.cloudfoundry.org/cfhttp"
	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/consuladapter"
	"code.cloudfoundry.org/debugserver"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/lager/lagerflags"
	route_emitter "code.cloudfoundry.org/route-emitter"
	"code.cloudfoundry.org/route-emitter/cmd/route-emitter/config"
	"code.cloudfoundry.org/route-emitter/consuldownchecker"
	"code.cloudfoundry.org/route-emitter/consuldownmodenotifier"
	"code.cloudfoundry.org/route-emitter/diegonats"
	"code.cloudfoundry.org/route-emitter/nats_emitter"
	"code.cloudfoundry.org/route-emitter/routing_table"
	"code.cloudfoundry.org/route-emitter/syncer"
	tcp_emitter "code.cloudfoundry.org/route-emitter/tcp/emitter"
	tcpRoutingTable "code.cloudfoundry.org/route-emitter/tcp/routing_table"
	"code.cloudfoundry.org/route-emitter/tcp/routing_table/schema"
	tcpSyncer "code.cloudfoundry.org/route-emitter/tcp/syncer"
	tcpWatcher "code.cloudfoundry.org/route-emitter/tcp/watcher"
	"code.cloudfoundry.org/route-emitter/watcher"
	"code.cloudfoundry.org/routing-api"
	uaaclient "code.cloudfoundry.org/uaa-go-client"
	"code.cloudfoundry.org/workpool"
	"github.com/cloudfoundry/dropsonde"
	"github.com/nu7hatch/gouuid"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"
	"github.com/tedsuo/ifrit/http_server"
	"github.com/tedsuo/ifrit/sigmon"
)

var configFilePath = flag.String(
	"config",
	"",
	"Path to JSON configuration file",
)

const (
	dropsondeOrigin = "route_emitter"
)

func main() {
	flag.Parse()

	cfg, err := config.NewRouteEmitterConfig(*configFilePath)
	if err != nil {
		logger, _ := lagerflags.NewFromConfig("route-emitter", lagerflags.DefaultLagerConfig())
		logger.Fatal("failed-to-parse-config", err)
	}

	cfhttp.Initialize(time.Duration(cfg.CommunicationTimeout))

	logger, reconfigurableSink := lagerflags.NewFromConfig(cfg.ConsulSessionName, cfg.LagerConfig)
	natsClient := diegonats.NewClient()

	natsPingDuration := 20 * time.Second
	logger.Info("setting-nats-ping-interval", lager.Data{"duration-in-seconds": natsPingDuration.Seconds()})
	natsClient.SetPingInterval(natsPingDuration)

	clock := clock.NewClock()
	syncer := syncer.NewSyncer(clock, time.Duration(cfg.SyncInterval), natsClient, logger)

	initializeDropsonde(logger, cfg.DropsondePort)

	natsClientRunner := diegonats.NewClientRunner(cfg.NATSAddresses, cfg.NATSUsername, cfg.NATSPassword, logger, natsClient)

	bbsClient := initializeBBSClient(logger, cfg)

	table := initializeRoutingTable(logger)
	emitter := initializeNatsEmitter(logger, natsClient, cfg.RouteEmittingWorkers)
	watcher := watcher.NewWatcher(
		cfg.CellID,
		bbsClient,
		clock,
		table,
		emitter,
		syncer.Events(),
		logger,
	)

	consulClient := initializeConsulClient(logger, cfg.ConsulCluster)

	routingAPIAddress := fmt.Sprintf("%s:%d", cfg.RoutingAPI.URI, cfg.RoutingAPI.Port)
	logger.Debug("creating-routing-api-client", lager.Data{"api-location": routingAPIAddress})

	routingAPIClient := routing_api.NewClient(routingAPIAddress, false)

	// if cfg.TCPRouteTTL.Seconds() > 65535 {
	// 	logger.Error("invalid-route-ttl", errors.New("route TTL value too large"))
	// 	os.Exit(1)
	// }
	logger.Debug("creating-noop-uaa-client")
	uaaClient := uaaclient.NewNoOpUaaClient()

	// Check UAA connectivity
	// _, err = uaaClient.FetchKey()
	// if err != nil {
	// 	logger.Error("failed-connecting-to-uaa", err)
	// 	os.Exit(1)
	// }

	tcpLogger := logger.Session("tcp")
	tcpEmitter := tcp_emitter.NewEmitter(tcpLogger, routingAPIClient, uaaClient, int(time.Duration(cfg.TCPRouteTTL).Seconds()))
	tcpTable := schema.NewTable(tcpLogger, nil)
	routingTableHandler := tcpRoutingTable.NewRoutingTableHandler(tcpLogger, tcpTable, tcpEmitter, bbsClient)
	syncChannel := make(chan struct{})
	tcpSyncRunner := tcpSyncer.New(clock, time.Duration(cfg.SyncInterval), syncChannel, tcpLogger)
	tcpWatcherRunner := tcpWatcher.NewWatcher(bbsClient, clock, routingTableHandler, syncChannel, tcpLogger)

	lockMaintainer := initializeLockMaintainer(
		logger,
		consulClient,
		cfg.ConsulSessionName,
		time.Duration(cfg.LockTTL),
		time.Duration(cfg.LockRetryInterval),
		clock,
	)

	consulDownModeNotifier := consuldownmodenotifier.NewConsulDownModeNotifier(
		logger,
		0,
		clock,
		time.Duration(cfg.ConsulDownModeNotificationInterval),
	)

	handler := func(resp http.ResponseWriter, req *http.Request) {
		resp.WriteHeader(http.StatusOK)
	}
	healthCheckServer := http_server.New(cfg.HealthCheckAddress, http.HandlerFunc(handler))
	members := grouper.Members{
		{"nats-client", natsClientRunner},
		{"healthcheck", healthCheckServer},
	}

	if cfg.CellID == "" {
		// we are running in global mode
		members = append(members, grouper.Member{"lock-maintainer", lockMaintainer})
	}

	members = append(members,
		grouper.Member{"consul-down-mode-notifier", consulDownModeNotifier},
		grouper.Member{"watcher", watcher},
		grouper.Member{"syncer", syncer},
		grouper.Member{"tcp-watcher", tcpWatcherRunner},
		grouper.Member{"tcp-syncer", tcpSyncRunner},
	)

	if cfg.DebugAddress != "" {
		members = append(grouper.Members{
			{"debug-server", debugserver.Runner(cfg.DebugAddress, reconfigurableSink)},
		}, members...)
	}

	group := grouper.NewOrdered(os.Interrupt, members)

	monitor := ifrit.Invoke(sigmon.New(group))

	logger.Info("started")

	err = <-monitor.Wait()
	if err != nil {
		logger.Error("finished-with-failure", err)
	} else {
		logger.Info("finished")
	}

	// ConsulDown mode

	logger = logger.Session("consul-down-mode")

	consulDownChecker := consuldownchecker.NewConsulDownChecker(
		logger,
		clock,
		consulClient,
		time.Duration(cfg.LockRetryInterval),
	)

	consulDownModeNotifier = consuldownmodenotifier.NewConsulDownModeNotifier(
		logger,
		1,
		clock,
		time.Duration(cfg.ConsulDownModeNotificationInterval),
	)

	members = grouper.Members{
		{"consul-down-checker", consulDownChecker},
		{"consul-down-mode-notifier", consulDownModeNotifier},
		{"nats-client", natsClientRunner},
		{"watcher", watcher},
		{"syncer", syncer},
	}

	group = grouper.NewOrdered(os.Interrupt, members)

	logger.Info("starting")

	monitor = ifrit.Invoke(sigmon.New(group))

	logger.Info("started")

	err = <-monitor.Wait()
	if err != nil {
		logger.Error("exited-with-failure", err)
		os.Exit(1)
	}

	logger.Info("exited")
}

func initializeDropsonde(logger lager.Logger, dropsondePort int) {
	dropsondeDestination := fmt.Sprint("localhost:", dropsondePort)
	err := dropsonde.Initialize(dropsondeDestination, dropsondeOrigin)
	if err != nil {
		logger.Error("failed to initialize dropsonde: %v", err)
	}
}

func initializeNatsEmitter(
	logger lager.Logger,
	natsClient diegonats.NATSClient,
	routeEmittingWorkers int,
) nats_emitter.NATSEmitter {
	workPool, err := workpool.NewWorkPool(routeEmittingWorkers)
	if err != nil {
		logger.Fatal("failed-to-construct-nats-emitter-workpool", err, lager.Data{"num-workers": routeEmittingWorkers}) // should never happen
	}

	return nats_emitter.New(natsClient, workPool, logger)
}

func initializeRoutingTable(logger lager.Logger) routing_table.RoutingTable {
	return routing_table.NewTable(logger)
}

func initializeConsulClient(logger lager.Logger, consulCluster string) consuladapter.Client {
	consulClient, err := consuladapter.NewClientFromUrl(consulCluster)
	if err != nil {
		logger.Fatal("new-client-failed", err)
	}
	return consulClient
}

func initializeLockMaintainer(
	logger lager.Logger,
	consulClient consuladapter.Client,
	sessionName string,
	lockTTL, lockRetryInterval time.Duration,
	clock clock.Clock,
) ifrit.Runner {
	uuid, err := uuid.NewV4()
	if err != nil {
		logger.Fatal("Couldn't generate uuid", err)
	}

	serviceClient := route_emitter.NewServiceClient(consulClient, clock)

	return serviceClient.NewRouteEmitterLockRunner(logger, uuid.String(), lockRetryInterval, lockTTL)
}

func initializeBBSClient(
	logger lager.Logger,
	cfg config.RouteEmitterConfig,
) bbs.Client {
	bbsURL, err := url.Parse(cfg.BBSAddress)
	if err != nil {
		logger.Fatal("Invalid BBS URL", err)
	}

	if bbsURL.Scheme != "https" {
		return bbs.NewClient(cfg.BBSAddress)
	}

	bbsClient, err := bbs.NewSecureClient(
		cfg.BBSAddress,
		cfg.BBSCACertFile,
		cfg.BBSClientCertFile,
		cfg.BBSClientKeyFile,
		cfg.BBSClientSessionCacheSize,
		cfg.BBSMaxIdleConnsPerHost,
	)
	if err != nil {
		logger.Fatal("Failed to configure secure BBS client", err)
	}
	return bbsClient
}
