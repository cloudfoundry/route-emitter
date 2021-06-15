package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"code.cloudfoundry.org/bbs"
	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/consuladapter"
	"code.cloudfoundry.org/debugserver"
	loggingclient "code.cloudfoundry.org/diego-logging-client"
	"code.cloudfoundry.org/go-loggregator/v8/runtimeemitter"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/lager/lagerflags"
	"code.cloudfoundry.org/locket"
	"code.cloudfoundry.org/locket/jointlock"
	"code.cloudfoundry.org/locket/lock"
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
	"code.cloudfoundry.org/route-emitter/unregistration"
	"code.cloudfoundry.org/route-emitter/watcher"
	routing_api "code.cloudfoundry.org/routing-api"
	"code.cloudfoundry.org/tlsconfig"
	uaaclient "code.cloudfoundry.org/uaa-go-client"
	uaaconfig "code.cloudfoundry.org/uaa-go-client/config"
	"code.cloudfoundry.org/workpool"
	uuid "github.com/nu7hatch/gouuid"
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
	routeEmitterLockKey = "route_emitter"
)

func main() {
	flag.Parse()

	cfg, err := config.NewRouteEmitterConfig(*configFilePath)
	if err != nil {
		logger, _ := lagerflags.NewFromConfig("route-emitter", lagerflags.DefaultLagerConfig())
		logger.Fatal("failed-to-parse-config", err)
	}

	logger, reconfigurableSink := lagerflags.NewFromConfig(cfg.ConsulSessionName, cfg.LagerConfig)

	natsClient, err := initializeNATSClient(logger, cfg.NATSTLSEnabled, cfg.NATSCACertFile, cfg.NATSClientCertFile, cfg.NATSClientKeyFile)
	if err != nil {
		logger.Error("failed-to-initialize-nats-client", err)
		os.Exit(1)
	}

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
	table := routingtable.NewRoutingTable(cfg.RegisterDirectInstanceRoutes, metronClient)
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

		var routingAPIClient routing_api.Client
		if cfg.RoutingAPI.ClientCertFile != "" && cfg.RoutingAPI.ClientKeyFile != "" && cfg.RoutingAPI.CACertFile != "" {
			tlsConfig, err := tlsconfig.Build(
				tlsconfig.WithInternalServiceDefaults(),
				tlsconfig.WithIdentityFromFile(cfg.RoutingAPI.ClientCertFile, cfg.RoutingAPI.ClientKeyFile),
			).Client(
				tlsconfig.WithAuthorityFromFile(cfg.RoutingAPI.CACertFile),
			)
			if err != nil {
				logger.Fatal("failed-to-create-routing-api-tls-config", err)
			}
			routingAPIClient = routing_api.NewClientWithTLSConfig(routingAPIAddress, tlsConfig)
		} else {
			routingAPIClient = routing_api.NewClient(routingAPIAddress, false)
		}

		routingAPIEmitter = emitter.NewRoutingAPIEmitter(tcpLogger, routingAPIClient, uaaClient, int(routeTTL.Seconds()))
	}

	unregistrationCache := unregistration.NewCache(logger)

	handler := routehandlers.NewHandler(table, natsEmitter, routingAPIEmitter, localMode, metronClient, unregistrationCache)

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
	unregistrationSender := unregistration.NewSender(logger, clock, unregistrationCache, natsEmitter, time.Duration(cfg.UnregistrationInterval), cfg.UnregistrationSendCount)
	members := grouper.Members{
		{"nats-client", natsClientRunner},
		{"healthcheck", healthCheckServer},
		{"unregistration", unregistrationSender},
	}

	lockMembers := []grouper.Member{}
	if cfg.CellID == "" {
		if cfg.ConsulEnabled {
			consulClient := initializeConsulClient(logger, cfg.ConsulCluster)

			lockMaintainer := initializeLockMaintainer(
				logger,
				consulClient,
				cfg.ConsulSessionName,
				time.Duration(cfg.LockTTL),
				time.Duration(cfg.LockRetryInterval),
				clock,
				metronClient,
			)

			consulDownModeNotifier := consuldownmodenotifier.NewConsulDownModeNotifier(
				logger,
				0,
				clock,
				time.Duration(cfg.ConsulDownModeNotificationInterval),
				metronClient,
			)

			// we are running in global mode
			lockMembers = append(lockMembers, grouper.Member{"lock-maintainer", lockMaintainer})
			lockMembers = append(lockMembers, grouper.Member{"consul-down-mode-notifier", consulDownModeNotifier})
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

			lockMembers = append(lockMembers, grouper.Member{"sql-lock", lock.NewLockRunner(
				logger,
				locketClient,
				lockIdentifier,
				locket.DefaultSessionTTLInSeconds,
				clock,
				locket.SQLRetryInterval,
			)})
		}

		members = append(members,
			grouper.Member{"lock", lockRunner(logger, clock, lockMembers)},
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

		consulClient := initializeConsulClient(logger, cfg.ConsulCluster)
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

		// we are running in global mode
		members = grouper.Members{
			{"nats-client", natsClientRunner},
			{"consul-down-checker", consulDownChecker},
			{"consul-down-mode-notifier", consulDownModeNotifier},
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

func lockRunner(logger lager.Logger, clk clock.Clock, locks []grouper.Member) ifrit.Runner {
	switch len(locks) {
	case 0:
		logger.Fatal("no-locks-configured", errors.New("Lock configuration must be provided"))
		return nil
	case 1:
		return locks[0]
	default:
		return jointlock.NewJointLock(clk, locket.DefaultSessionTTL, locks...)
	}
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
		RequestTimeout:   time.Duration(c.OAuth.UaaRequestTimeout),
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

func initializeMetron(logger lager.Logger, locketConfig config.RouteEmitterConfig) (loggingclient.IngressClient, error) {
	client, err := loggingclient.NewIngressClient(locketConfig.LoggregatorConfig)
	if err != nil {
		return nil, err
	}

	if locketConfig.LoggregatorConfig.UseV2API {
		emitter := runtimeemitter.NewV1(client)
		go emitter.Run()
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
	bbsClient, err := bbs.NewClientWithConfig(bbs.ClientConfig{
		URL:                    cfg.BBSAddress,
		IsTLS:                  true,
		CAFile:                 cfg.BBSCACertFile,
		CertFile:               cfg.BBSClientCertFile,
		KeyFile:                cfg.BBSClientKeyFile,
		ClientSessionCacheSize: cfg.BBSClientSessionCacheSize,
		MaxIdleConnsPerHost:    cfg.BBSMaxIdleConnsPerHost,
		RequestTimeout:         time.Duration(cfg.CommunicationTimeout),
	})
	if err != nil {
		logger.Fatal("Failed to configure secure BBS client", err)
	}
	return bbsClient
}

func initializeNATSClient(logger lager.Logger, tlsEnabled bool, caFile, certFile, keyFile string) (diegonats.NATSClient, error) {
	var natsClient diegonats.NATSClient
	if tlsEnabled {
		tlsConfig, err := tlsconfig.Build(
			tlsconfig.WithInternalServiceDefaults(),
			tlsconfig.WithIdentityFromFile(certFile, keyFile),
		).Client(
			tlsconfig.WithAuthorityFromFile(caFile),
		)
		if err != nil {
			return nil, err
		}
		natsClient = diegonats.NewClientWithTLSConfig(tlsConfig)
	} else {
		natsClient = diegonats.NewClient()
	}

	natsPingDuration := 20 * time.Second
	logger.Info("setting-nats-ping-interval", lager.Data{"duration-in-seconds": natsPingDuration.Seconds()})
	natsClient.SetPingInterval(natsPingDuration)

	return natsClient, nil
}
