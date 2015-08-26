package main_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cloudfoundry/gunk/diegonats"
	"github.com/cloudfoundry/gunk/diegonats/gnatsdrunner"
	"github.com/cloudfoundry/storeadapter"
	"github.com/cloudfoundry/storeadapter/storerunner/etcdstorerunner"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/pivotal-golang/clock"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

	"github.com/cloudfoundry-incubator/bbs"
	bbstestrunner "github.com/cloudfoundry-incubator/bbs/cmd/bbs/testrunner"
	"github.com/cloudfoundry-incubator/consuladapter/consulrunner"
	"github.com/cloudfoundry-incubator/receptor/cmd/receptor/testrunner"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
)

const heartbeatInterval = 1 * time.Second

var (
	emitterPath string

	receptorPath string
	receptorPort int

	etcdPort int

	natsPort int

	bbsPath string
	bbsURL  *url.URL
)

var bbsArgs bbstestrunner.Args
var bbsRunner *ginkgomon.Runner
var bbsProcess ifrit.Process

var etcdRunner *etcdstorerunner.ETCDClusterRunner
var consulRunner *consulrunner.ClusterRunner
var gnatsdRunner ifrit.Process
var receptorRunner ifrit.Process
var natsClient diegonats.NATSClient
var store storeadapter.StoreAdapter
var legacyBBS *Bbs.BBS
var bbsClient bbs.Client
var logger *lagertest.TestLogger
var syncInterval time.Duration

func TestRouteEmitter(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(2 * time.Second)
	RunSpecs(t, "Route Emitter Suite")
}

func createEmitterRunner(sessionName string) *ginkgomon.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Command: exec.Command(
			string(emitterPath),
			"-sessionName", sessionName,
			"-natsAddresses", fmt.Sprintf("127.0.0.1:%d", natsPort),
			"-diegoAPIURL", fmt.Sprintf("http://127.0.0.1:%d", receptorPort),
			"-communicationTimeout", "100ms",
			"-syncInterval", syncInterval.String(),
			"-lockRetryInterval", "1s",
			"-consulCluster", consulRunner.ConsulCluster(),
		),

		StartCheck: "route-emitter.started",

		AnsiColorCode: "97m",
	})
}

var _ = SynchronizedBeforeSuite(func() []byte {
	emitter, err := gexec.Build("github.com/cloudfoundry-incubator/route-emitter/cmd/route-emitter", "-race")
	Expect(err).NotTo(HaveOccurred())

	receptor, err := gexec.Build("github.com/cloudfoundry-incubator/receptor/cmd/receptor", "-race")
	Expect(err).NotTo(HaveOccurred())

	bbs, err := gexec.Build("github.com/cloudfoundry-incubator/bbs/cmd/bbs", "-race")
	Expect(err).NotTo(HaveOccurred())

	payload, err := json.Marshal(map[string]string{
		"emitter":  emitter,
		"receptor": receptor,
		"bbs":      bbs,
	})

	Expect(err).NotTo(HaveOccurred())

	return payload
}, func(payload []byte) {
	binaries := map[string]string{}

	err := json.Unmarshal(payload, &binaries)
	Expect(err).NotTo(HaveOccurred())

	etcdPort = 5001 + GinkgoParallelNode()
	natsPort = 4001 + GinkgoParallelNode()
	receptorPort = 6001 + GinkgoParallelNode()

	etcdRunner = etcdstorerunner.NewETCDClusterRunner(etcdPort, 1, nil)

	emitterPath = string(binaries["emitter"])
	receptorPath = string(binaries["receptor"])
	store = etcdRunner.Adapter(nil)

	consulRunner = consulrunner.NewClusterRunner(
		9001+config.GinkgoConfig.ParallelNode*consulrunner.PortOffsetLength,
		1,
		"http",
	)

	logger = lagertest.NewTestLogger("test")

	syncInterval = 200 * time.Millisecond

	bbsPath = string(binaries["bbs"])
	bbsAddress := fmt.Sprintf("127.0.0.1:%d", 13000+GinkgoParallelNode())

	bbsURL = &url.URL{
		Scheme: "http",
		Host:   bbsAddress,
	}

	bbsClient = bbs.NewClient(bbsURL.String())

	bbsArgs = bbstestrunner.Args{
		Address:           bbsAddress,
		AuctioneerAddress: "some-address",
		EtcdCluster:       strings.Join(etcdRunner.NodeURLS(), ","),
		ConsulCluster:     consulRunner.ConsulCluster(),
	}
})

var _ = BeforeEach(func() {
	etcdRunner.Start()
	consulRunner.Start()
	consulRunner.WaitUntilReady()

	bbsRunner = bbstestrunner.New(bbsPath, bbsArgs)
	bbsProcess = ginkgomon.Invoke(bbsRunner)

	legacyBBS = Bbs.NewBBS(store, consulRunner.NewSession("a-session"), clock.NewClock(), logger)
	gnatsdRunner, natsClient = gnatsdrunner.StartGnatsd(natsPort)
	receptorRunner = ginkgomon.Invoke(testrunner.New(receptorPath, testrunner.Args{
		Address:       fmt.Sprintf("127.0.0.1:%d", receptorPort),
		BBSAddress:    bbsURL.String(),
		EtcdCluster:   strings.Join(etcdRunner.NodeURLS(), ","),
		ConsulCluster: consulRunner.ConsulCluster(),
	}))
})

var _ = AfterEach(func() {
	ginkgomon.Kill(bbsProcess)
	ginkgomon.Kill(receptorRunner, 5)
	etcdRunner.Stop()
	consulRunner.Stop()
	gnatsdRunner.Signal(os.Interrupt)
	Eventually(gnatsdRunner.Wait(), 5).Should(Receive())
})

var _ = SynchronizedAfterSuite(func() {
	if etcdRunner != nil {
		etcdRunner.Stop()
	}
}, func() {
	gexec.CleanupBuildArtifacts()
})
