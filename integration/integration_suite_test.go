package integration_test

import (
	"fmt"
	"testing"

	steno "github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/gunk/natsrunner"
	"github.com/cloudfoundry/gunk/timeprovider"
	"github.com/cloudfoundry/storeadapter"
	"github.com/cloudfoundry/storeadapter/storerunner/etcdstorerunner"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/cloudfoundry-incubator/route-emitter/integration/route_emitter_runner"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
)

var runner *route_emitter_runner.Runner
var etcdRunner *etcdstorerunner.ETCDClusterRunner
var natsRunner *natsrunner.NATSRunner
var store storeadapter.StoreAdapter
var bbs *Bbs.BBS

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Integration Suite")
}

var _ = BeforeSuite(func() {
	emitterPath, err := gexec.Build("github.com/cloudfoundry-incubator/route-emitter", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	etcdPort := 5001 + GinkgoParallelNode()
	natsPort := 4001 + GinkgoParallelNode()

	etcdRunner = etcdstorerunner.NewETCDClusterRunner(etcdPort, 1)
	natsRunner = natsrunner.NewNATSRunner(natsPort)

	store = etcdRunner.Adapter()

	logSink := steno.NewTestingSink()
	steno.Init(&steno.Config{
		Sinks: []steno.Sink{logSink},
	})
	logger := steno.NewLogger("the-logger")
	steno.EnterTestMode()

	bbs = Bbs.NewBBS(store, timeprovider.NewTimeProvider(), logger)

	runner = route_emitter_runner.New(
		emitterPath,
		[]string{fmt.Sprintf("http://127.0.0.1:%d", etcdPort)},
		[]string{fmt.Sprintf("127.0.0.1:%d", natsPort)},
	)
})

var _ = BeforeEach(func() {
	etcdRunner.Start()
	natsRunner.Start()
	//runner.Start()
})

var _ = AfterEach(func() {
	runner.KillWithFire()
	etcdRunner.Stop()
	natsRunner.Stop()
})

var _ = AfterSuite(func() {
	gexec.CleanupBuildArtifacts()
	if etcdRunner != nil {
		etcdRunner.Stop()
	}
	if natsRunner != nil {
		natsRunner.Stop()
	}
	if runner != nil {
		runner.KillWithFire()
	}
})
