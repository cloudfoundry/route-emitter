package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cloudfoundry/gunk/diegonats"
	"github.com/cloudfoundry/storeadapter"
	"github.com/cloudfoundry/storeadapter/storerunner/etcdstorerunner"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/pivotal-golang/clock"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

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
)

var etcdRunner *etcdstorerunner.ETCDClusterRunner
var gnatsdRunner ifrit.Process
var receptorRunner ifrit.Process
var natsClient diegonats.NATSClient
var store storeadapter.StoreAdapter
var bbs *Bbs.BBS
var logger *lagertest.TestLogger

func TestRouteEmitter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Route Emitter Suite")
}

func createEmitterRunner() *ginkgomon.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Command: exec.Command(
			string(emitterPath),
			"-etcdCluster", fmt.Sprintf("http://127.0.0.1:%d", etcdPort),
			"-natsAddresses", fmt.Sprintf("127.0.0.1:%d", natsPort),
			"-heartbeatInterval", heartbeatInterval.String(),
			"-diegoAPIURL", fmt.Sprintf("http://127.0.0.1:%d", receptorPort),
			"-communicationTimeout", "100ms",
		),

		StartCheck: "route-emitter.started",

		AnsiColorCode: "97m",
	})
}

var _ = SynchronizedBeforeSuite(func() []byte {
	emitter, err := gexec.Build("github.com/cloudfoundry-incubator/route-emitter/cmd/route-emitter", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	receptor, err := gexec.Build("github.com/cloudfoundry-incubator/receptor/cmd/receptor", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	payload, err := json.Marshal(map[string]string{
		"emitter":  emitter,
		"receptor": receptor,
	})

	Ω(err).ShouldNot(HaveOccurred())

	return payload
}, func(payload []byte) {
	binaries := map[string]string{}

	err := json.Unmarshal(payload, &binaries)
	Ω(err).ShouldNot(HaveOccurred())

	etcdPort = 5001 + GinkgoParallelNode()
	natsPort = 4001 + GinkgoParallelNode()
	receptorPort = 6001 + GinkgoParallelNode()

	etcdRunner = etcdstorerunner.NewETCDClusterRunner(etcdPort, 1)
	emitterPath = string(binaries["emitter"])
	receptorPath = string(binaries["receptor"])
	store = etcdRunner.Adapter()

	logger = lagertest.NewTestLogger("test")
	bbs = Bbs.NewBBS(store, clock.NewClock(), logger)
})

var _ = BeforeEach(func() {
	etcdRunner.Start()
	gnatsdRunner, natsClient = diegonats.StartGnatsd(natsPort)
	receptorRunner = ginkgomon.Invoke(testrunner.New(receptorPath, testrunner.Args{
		Address:     fmt.Sprintf("127.0.0.1:%d", receptorPort),
		EtcdCluster: strings.Join(etcdRunner.NodeURLS(), ","),
	}))
})

var _ = AfterEach(func() {
	ginkgomon.Kill(receptorRunner)
	etcdRunner.Stop()
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
