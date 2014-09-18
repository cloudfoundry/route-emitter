package main

import (
	"flag"
	"net/url"
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

	natsClient := initializeNatsClient(logger)
	bbs := initializeBbs(logger)
	emitter := initializeNatsEmitter(natsClient, logger)
	table := initializeRoutingTable()

	watcher := watcher.NewWatcher(bbs, table, emitter, logger)
	syncer := syncer.NewSyncer(bbs, table, emitter, *syncInterval, natsClient, logger)

	group := group_runner.New([]group_runner.Member{
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

func initializeNatsClient(logger lager.Logger) yagnats.ApceraWrapperNATSClient {
	natsMembers := []string{}
	for _, addr := range strings.Split(*natsAddresses, ",") {
		uri := url.URL{
			Scheme: "nats",
			User:   url.UserPassword(*natsUsername, *natsPassword),
			Host:   addr,
		}
		natsMembers = append(natsMembers, uri.String())
	}
	natsClient := yagnats.NewApceraClientWrapper(natsMembers)

	err := natsClient.Connect()
	if err != nil {
		logger.Fatal("failed-to-connect-to-nats", err)
	}

	return natsClient
}

func initializeNatsEmitter(natsClient yagnats.ApceraWrapperNATSClient, logger lager.Logger) *nats_emitter.NATSEmitter {
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
