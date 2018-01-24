package main

import (
	"errors"
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
	loggingclient "code.cloudfoundry.org/diego-logging-client"
	"code.cloudfoundry.org/go-loggregator/runtimeemitter"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/lager/lagerflags"
	"code.cloudfoundry.org/locket"
	"code.cloudfoundry.org/locket/jointlock"
	"code.cloudfoundry.org/locket/lock"
	"code.cloudfoundry.org/locket/lockheldmetrics"
	locketmodels "code.cloudfoundry.org/locket/models"
	route_emitter "code.cloudfoundry.org/route-emitter"
	"code.cloudfoundry.org/route-emitter/cmd/route-emitter/config"
	"code.cloudfoundry.org/route-emitter/consuldownchecker"
	"code.cloudfoundry.org/route-emitter/consuldownmodenotifier"
	"code.cloudfoundry.org/route-emitter/diegonats"
	"code.cloudfoundry.org/route-emitter/emitter"
	"code.cloudfoundry.org/route-emitter/routehandlers"
	"code.cloudfoundry.org/route-emitter/routingtable"
	"code.cloudfoundry.org/route-emitter/scheduler"
	"code.cloudfoundry.org/route-emitter/syncer"
	"code.cloudfoundry.org/route-emitter/watcher"
	"code.cloudfoundry.org/routing-api"
	uaaclient "code.cloudfoundry.org/uaa-go-client"
	uaaconfig "code.cloudfoundry.org/uaa-go-client/config"
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
	dropsondeOrigin     = "route_emitter"
	routeEmitterLockKey = "route-emitter"
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

	externalChan := make(chan struct{}, 1)
	internalChan := make(chan struct{}, 1)
	syncer := syncer.NewSyncer(clock, time.Duration(cfg.SyncInterval), logger)
	externalScheduler := scheduler.NewRouteBroadcastScheduler(clock, natsClient, logger, "router", externalChan)
	internalScheduler := scheduler.NewRouteBroadcastScheduler(clock, natsClient, logger, "service-discovery", internalChan)

	metronClient, err := initializeMetron(logger, cfg)
	if err != nil {
		logger.Error("failed-to-initialize-metron-client", err)
		os.Exit(1)
	}

	natsClientRunner := diegonats.NewClientRunner(cfg.NATSAddresses, cfg.NATSUsername, cfg.NATSPassword, logger, natsClient)

	bbsClient := initializeBBSClient(logger, cfg)

	localMode := cfg.CellID != ""
	table := routingtable.NewRoutingTable(logger, cfg.RegisterDirectInstanceRoutes, metronClient)
	natsEmitter := initializeNatsEmitter(logger, natsClient, cfg.RouteEmittingWorkers, metronClient, cfg.EnableInternalEmitter)

	routeTTL := time.Duration(cfg.TCPRouteTTL)
	if routeTTL.Seconds() > 65535 {
		logger.Fatal("invalid-route-ttl", errors.New("route TTL value too large"), lager.Data{"ttl": routeTTL.Seconds()})
	}

	var routingAPIEmitter emitter.RoutingAPIEmitter
	if cfg.EnableTCPEmitter {
		tcpLogger := logger.Session("tcp")
		uaaClient := newUaaClient(tcpLogger, &cfg, clock)
		routingAPIAddress := fmt.Sprintf("%s:%d", cfg.RoutingAPI.URL, cfg.RoutingAPI.Port)
		logger.Debug("creating-routing-api-client", lager.Data{"api-location": routingAPIAddress})
		routingAPIClient := routing_api.NewClient(routingAPIAddress, false)
		routingAPIEmitter = emitter.NewRoutingAPIEmitter(tcpLogger, routingAPIClient, uaaClient, int(routeTTL.Seconds()))
	}

	handler := routehandlers.NewHandler(table, natsEmitter, routingAPIEmitter, localMode, metronClient)

	watcher := watcher.NewWatcher(
		cfg.CellID,
		bbsClient,
		clock,
		handler,
		syncer.SyncCh(),
		externalScheduler.EmitCh(),
		internalScheduler.EmitCh(),
		logger,
		metronClient,
	)

	healthHandler := func(resp http.ResponseWriter, req *http.Request) {
		resp.WriteHeader(http.StatusOK)
	}
	healthCheckServer := http_server.New(cfg.HealthCheckAddress, http.HandlerFunc(healthHandler))
	members := grouper.Members{
		{"nats-client", natsClientRunner},
		{"healthcheck", healthCheckServer},
	}

	var consulClient consuladapter.Client
	consulLocks := []grouper.Member{}
	locketLocks := []grouper.Member{}
	var consulDownModeNotifier *consuldownmodenotifier.ConsulDownModeNotifier

	if cfg.CellID == "" {
		if cfg.ConsulEnabled {
			consulClient = initializeConsulClient(logger, cfg.ConsulCluster)

			lockMaintainer := initializeLockMaintainer(
				logger,
				consulClient,
				cfg.ConsulSessionName,
				time.Duration(cfg.LockTTL),
				time.Duration(cfg.LockRetryInterval),
				clock,
				metronClient,
			)

			consulDownModeNotifier = consuldownmodenotifier.NewConsulDownModeNotifier(
				logger,
				0,
				clock,
				time.Duration(cfg.ConsulDownModeNotificationInterval),
				metronClient,
			)

			// we are running in global mode
			consulLocks = append(consulLocks, grouper.Member{"lock-maintainer", lockMaintainer})
			consulLocks = append(consulLocks, grouper.Member{"consul-down-mode-notifier", consulDownModeNotifier})
		}

		if cfg.LocketEnabled {
			locketClient, err := locket.NewClient(logger, cfg.ClientLocketConfig)
			if err != nil {
				logger.Fatal("failed-to-create-locket-client", err)
			}

			if cfg.UUID == "" {
				logger.Fatal("invalid-uuid", errors.New("invalid-uuid-from-config"))
			}

			lockIdentifier := &locketmodels.Resource{
				Key:      routeEmitterLockKey,
				Owner:    cfg.UUID,
				TypeCode: locketmodels.LOCK,
				Type:     locketmodels.LockType,
			}

			locketLocks = append(locketLocks, grouper.Member{"sql-lock", lock.NewLockRunner(
				logger,
				locketClient,
				lockIdentifier,
				locket.DefaultSessionTTLInSeconds,
				clock,
				locket.SQLRetryInterval,
			)})
		}

		lock := lockRunner(logger, clock, consulLocks, locketLocks)

		fmt.Println("$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$", cfg.ReportInterval)
		metricsTicker := clock.NewTicker(time.Duration(cfg.ReportInterval))
		lockHeldMetronNotifier := lockheldmetrics.NewLockHeldMetronNotifier(logger, metricsTicker, metronClient)

		members = append(members,
			grouper.Member{"lock-held-metrics", lockHeldMetronNotifier},
			grouper.Member{"lock", lock},
			grouper.Member{"set-lock-held-metrics", lockheldmetrics.SetLockHeldRunner(logger, *lockHeldMetronNotifier)},
		)
	}

	members = append(members,
		grouper.Member{"watcher", watcher},
		grouper.Member{"external-scheduler", externalScheduler},
		grouper.Member{"syncer", syncer},
	)

	if cfg.EnableInternalEmitter {
		members = append(members, grouper.Member{"internal-scheduler", internalScheduler})
	}

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

	if cfg.ConsulEnabled && cfg.CellID == "" {
		// ConsulDown mode
		logger = logger.Session("consul-down-mode")

		consulClient = initializeConsulClient(logger, cfg.ConsulCluster)
		consulDownChecker := consuldownchecker.NewConsulDownChecker(
			logger,
			clock,
			consulClient,
			time.Duration(cfg.LockRetryInterval),
		)

		consulDownModeNotifier := consuldownmodenotifier.NewConsulDownModeNotifier(
			logger,
			1,
			clock,
			time.Duration(cfg.ConsulDownModeNotificationInterval),
			metronClient,
		)

		consulLocks = []grouper.Member{
			grouper.Member{"consul-down-checker", consulDownChecker},
			grouper.Member{"consul-down-mode-notifier", consulDownModeNotifier},
		}

		lock := lockRunner(logger, clock, consulLocks, locketLocks)

		// we are running in global mode
		members = grouper.Members{
			{"nats-client", natsClientRunner},
			{"lock", lock},
			{"watcher", watcher},
			{"external-scheduler", externalScheduler},
			{"syncer", syncer},
		}

		if cfg.EnableInternalEmitter {
			members = append(members, grouper.Member{"internal-scheduler", internalScheduler})
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
	}

	logger.Info("exited")
}

func lockRunner(logger lager.Logger, clk clock.Clock, consulLocks []grouper.Member, locketLocks []grouper.Member) ifrit.Runner {
	var lock ifrit.Runner
	lockMembers := []grouper.Member{}

	if len(consulLocks) != 0 {
		logger.Info("consul-configured")
		lockMembers = append(lockMembers, consulLocks...)
	}

	if len(locketLocks) != 0 {
		logger.Info("locket-configured")
		lockMembers = append(lockMembers, locketLocks...)
	}

	if len(consulLocks) == 0 && len(locketLocks) == 0 {
		logger.Fatal("no-locks-configured", errors.New("Lock configuration must be provided"))
	} else if len(consulLocks) > 0 && len(locketLocks) > 0 {
		lock = jointlock.NewJointLock(clk, locket.DefaultSessionTTL, lockMembers...)
	} else {
		lock = grouper.NewOrdered(os.Interrupt, lockMembers)
	}
	return lock
}

func newUaaClient(logger lager.Logger, c *config.RouteEmitterConfig, klok clock.Clock) uaaclient.Client {
	if !c.RoutingAPI.AuthEnabled {
		logger.Debug("creating-noop-uaa-client")
		client := uaaclient.NewNoOpUaaClient()
		return client
	}

	logger.Debug("creating-uaa-client")
	cfg := uaaconfig.Config{
		UaaEndpoint:      c.OAuth.UaaURL,
		ClientName:       c.OAuth.ClientName,
		ClientSecret:     c.OAuth.ClientSecret,
		SkipVerification: c.OAuth.SkipCertVerify,
		CACerts:          c.OAuth.CACerts,
	}
	uaaClient, err := uaaclient.NewClient(logger, &cfg, klok)
	if err != nil {
		logger.Fatal("initialize-token-fetcher-error", err)
	}

	_, err = uaaClient.FetchKey()
	if err != nil {
		logger.Fatal("failed-fetching-uaa-key", err)
	}

	return uaaClient
}

func initializeDropsonde(logger lager.Logger, dropsondePort int) {
	dropsondeDestination := fmt.Sprint("localhost:", dropsondePort)
	err := dropsonde.Initialize(dropsondeDestination, dropsondeOrigin)
	if err != nil {
		logger.Error("failed to initialize dropsonde: %v", err)
	}
}

func initializeMetron(logger lager.Logger, cfg config.RouteEmitterConfig) (loggingclient.IngressClient, error) {
	client, err := loggingclient.NewIngressClient(cfg.LoggregatorConfig)
	if err != nil {
		return nil, err
	}

	if cfg.LoggregatorConfig.UseV2API {
		emitter := runtimeemitter.NewV1(client)
		go emitter.Run()
	} else {
		initializeDropsonde(logger, cfg.DropsondePort)
	}

	return client, nil
}

func initializeNatsEmitter(
	logger lager.Logger,
	natsClient diegonats.NATSClient,
	routeEmittingWorkers int,
	metronClient loggingclient.IngressClient,
	emitInternalRoutes bool,
) emitter.NATSEmitter {
	workPool, err := workpool.NewWorkPool(routeEmittingWorkers)
	if err != nil {
		logger.Fatal("failed-to-construct-nats-emitter-workpool", err, lager.Data{"num-workers": routeEmittingWorkers}) // should never happen
	}

	return emitter.NewNATSEmitter(natsClient, workPool, logger, metronClient, emitInternalRoutes)
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
	metronClient loggingclient.IngressClient,
) ifrit.Runner {
	uuid, err := uuid.NewV4()
	if err != nil {
		logger.Fatal("Couldn't generate uuid", err)
	}

	serviceClient := route_emitter.NewServiceClient(consulClient, clock)

	return serviceClient.NewRouteEmitterLockRunner(logger, uuid.String(), lockRetryInterval, lockTTL, metronClient)
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
