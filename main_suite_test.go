package main_test

import (
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/cloudfoundry/gunk/natsrunner"
	"github.com/cloudfoundry/gunk/timeprovider"
	"github.com/cloudfoundry/storeadapter"
	"github.com/cloudfoundry/storeadapter/storerunner/etcdstorerunner"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit/ginkgomon"

	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
)

const heartbeatInterval = 1 * time.Second

var (
	emitterPath string

	etcdPort int
	natsPort int
)

var etcdRunner *etcdstorerunner.ETCDClusterRunner
var natsRunner *natsrunner.NATSRunner
var store storeadapter.StoreAdapter
var bbs *Bbs.BBS

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
		),

		StartCheck: "route-emitter.started",

		AnsiColorCode: "97m",
	})
}

var _ = SynchronizedBeforeSuite(func() []byte {
	emitterPath, err := gexec.Build("github.com/cloudfoundry-incubator/route-emitter", "-race")
	Ω(err).ShouldNot(HaveOccurred())
	return []byte(emitterPath)
}, func(builtEmitterPath []byte) {
	emitterPath = string(builtEmitterPath)
	etcdPort = 5001 + GinkgoParallelNode()
	natsPort = 4001 + GinkgoParallelNode()

	etcdRunner = etcdstorerunner.NewETCDClusterRunner(etcdPort, 1)
	natsRunner = natsrunner.NewNATSRunner(natsPort)

	store = etcdRunner.Adapter()

	bbs = Bbs.NewBBS(store, timeprovider.NewTimeProvider(), lagertest.NewTestLogger("test"))
})

var _ = BeforeEach(func() {
	etcdRunner.Start()
	natsRunner.Start()
})

var _ = AfterEach(func() {
	etcdRunner.Stop()
	natsRunner.Stop()
})

var _ = SynchronizedAfterSuite(func() {
	if etcdRunner != nil {
		etcdRunner.Stop()
	}
	if natsRunner != nil {
		natsRunner.Stop()
	}
}, func() {
	gexec.CleanupBuildArtifacts()
})
