package main_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/route-emitter/diegonats"
	"code.cloudfoundry.org/route-emitter/diegonats/gnatsdrunner"
	"github.com/cloudfoundry/sonde-go/events"
	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

	"code.cloudfoundry.org/bbs"
	bbsconfig "code.cloudfoundry.org/bbs/cmd/bbs/config"
	bbstestrunner "code.cloudfoundry.org/bbs/cmd/bbs/testrunner"
	"code.cloudfoundry.org/bbs/encryption"
	"code.cloudfoundry.org/bbs/test_helpers"
	"code.cloudfoundry.org/bbs/test_helpers/sqlrunner"
	"code.cloudfoundry.org/consuladapter/consulrunner"
)

const heartbeatInterval = 1 * time.Second

var (
	emitterPath   string
	natsPort      int
	dropsondePort int

	bbsPath    string
	bbsURL     *url.URL
	bbsConfig  bbsconfig.BBSConfig
	bbsRunner  *ginkgomon.Runner
	bbsProcess ifrit.Process

	consulRunner         *consulrunner.ClusterRunner
	gnatsdRunner         ifrit.Process
	natsClient           diegonats.NATSClient
	bbsClient            bbs.InternalClient
	logger               *lagertest.TestLogger
	syncInterval         time.Duration
	consulClusterAddress string
	testMetricsListener  net.PacketConn
	testMetricsChan      chan *events.Envelope

	sqlProcess ifrit.Process
	sqlRunner  sqlrunner.SQLRunner
	bbsRunning = false
)

func TestRouteEmitter(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(5 * time.Second)
	RunSpecs(t, "Route Emitter Suite")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	emitter, err := gexec.Build("code.cloudfoundry.org/route-emitter/cmd/route-emitter", "-race")
	Expect(err).NotTo(HaveOccurred())

	bbs, err := gexec.Build("code.cloudfoundry.org/bbs/cmd/bbs", "-race")
	Expect(err).NotTo(HaveOccurred())

	payload, err := json.Marshal(map[string]string{
		"emitter": emitter,
		"bbs":     bbs,
	})

	Expect(err).NotTo(HaveOccurred())

	return payload
}, func(payload []byte) {
	binaries := map[string]string{}

	err := json.Unmarshal(payload, &binaries)
	Expect(err).NotTo(HaveOccurred())

	natsPort = 4001 + GinkgoParallelNode()

	emitterPath = string(binaries["emitter"])

	dbName := fmt.Sprintf("diego_%d", GinkgoParallelNode())
	sqlRunner = test_helpers.NewSQLRunner(dbName)

	consulRunner = consulrunner.NewClusterRunner(
		consulrunner.ClusterRunnerConfig{
			StartingPort: 9001 + GinkgoParallelNode()*consulrunner.PortOffsetLength,
			NumNodes:     1,
			Scheme:       "http",
		},
	)

	logger = lagertest.NewTestLogger("test")

	syncInterval = 200 * time.Millisecond

	bbsPath = string(binaries["bbs"])
	bbsPort := 13000 + GinkgoParallelNode()*2
	healthPort := bbsPort + 1
	bbsAddress := fmt.Sprintf("127.0.0.1:%d", bbsPort)
	healthAddress := fmt.Sprintf("127.0.0.1:%d", healthPort)

	bbsURL = &url.URL{
		Scheme: "http",
		Host:   bbsAddress,
	}

	bbsClient = bbs.NewClient(bbsURL.String())

	bbsConfig = bbsconfig.BBSConfig{
		ListenAddress:            bbsAddress,
		AdvertiseURL:             bbsURL.String(),
		AuctioneerAddress:        "some-address",
		DatabaseDriver:           sqlRunner.DriverName(),
		DatabaseConnectionString: sqlRunner.ConnectionString(),
		ConsulCluster:            consulRunner.ConsulCluster(),
		HealthAddress:            healthAddress,

		EncryptionConfig: encryption.EncryptionConfig{
			EncryptionKeys: map[string]string{"label": "key"},
			ActiveKeyLabel: "label",
		},
	}

	sqlProcess = ginkgomon.Invoke(sqlRunner)

	consulRunner.Start()
	consulRunner.WaitUntilReady()
	consulClusterAddress = consulRunner.ConsulCluster()
})

var _ = BeforeEach(func() {
	sqlRunner.Reset()
	consulRunner.Reset()

	startBBS()

	gnatsdRunner, natsClient = gnatsdrunner.StartGnatsd(natsPort)

	testMetricsListener, _ = net.ListenPacket("udp", "127.0.0.1:0")
	testMetricsChan = make(chan *events.Envelope, 1)
	go func() {
		defer GinkgoRecover()
		for {
			buffer := make([]byte, 1024)
			n, _, err := testMetricsListener.ReadFrom(buffer)
			if err != nil {
				close(testMetricsChan)
				return
			}

			var envelope events.Envelope
			err = proto.Unmarshal(buffer[:n], &envelope)
			Expect(err).NotTo(HaveOccurred())
			testMetricsChan <- &envelope
		}
	}()

	var err error
	dropsondePort, err = strconv.Atoi(strings.TrimPrefix(testMetricsListener.LocalAddr().String(), "127.0.0.1:"))
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterEach(func() {
	stopBBS()
	gnatsdRunner.Signal(os.Interrupt)
	Eventually(gnatsdRunner.Wait(), 5).Should(Receive())

	testMetricsListener.Close()
	Eventually(testMetricsChan).Should(BeClosed())
})

func stopBBS() {
	ginkgomon.Interrupt(bbsProcess)
}

func startBBS() {
	if bbsRunning {
		return
	}

	bbsRunner = bbstestrunner.New(bbsPath, bbsConfig)
	bbsProcess = ginkgomon.Invoke(bbsRunner)
	bbsRunning = true
}

var _ = SynchronizedAfterSuite(func() {
	consulRunner.Stop()
	ginkgomon.Interrupt(sqlProcess)
}, func() {
	gexec.CleanupBuildArtifacts()
})
