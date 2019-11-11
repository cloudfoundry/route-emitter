package main_test

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"testing"
	"time"

	bbsconfig "code.cloudfoundry.org/bbs/cmd/bbs/config"
	bbstestrunner "code.cloudfoundry.org/bbs/cmd/bbs/testrunner"
	"code.cloudfoundry.org/bbs/encryption"
	"code.cloudfoundry.org/bbs/test_helpers"
	"code.cloudfoundry.org/bbs/test_helpers/sqlrunner"
	"code.cloudfoundry.org/consuladapter/consulrunner"
	"code.cloudfoundry.org/diego-logging-client/testhelpers"
	"code.cloudfoundry.org/durationjson"
	"code.cloudfoundry.org/go-loggregator/rpc/loggregator_v2"
	"code.cloudfoundry.org/inigo/helpers/portauthority"
	"code.cloudfoundry.org/lager/lagerflags"
	"code.cloudfoundry.org/locket"
	"code.cloudfoundry.org/route-emitter/cmd/route-emitter/config"
	"code.cloudfoundry.org/route-emitter/diegonats"
	"code.cloudfoundry.org/route-emitter/diegonats/natsserverrunner"
	"code.cloudfoundry.org/tlsconfig"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/ghttp"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

var (
	cfgs []func(*config.RouteEmitterConfig)

	emitterPath        string
	locketPath         string
	natsPort           uint16
	healthCheckAddress string

	oauthServer *ghttp.Server

	bbsPath    string
	bbsURL     *url.URL
	bbsConfig  bbsconfig.BBSConfig
	bbsRunner  *ginkgomon.Runner
	bbsProcess ifrit.Process

	routingAPIPath string

	consulRunner         *consulrunner.ClusterRunner
	natsServerProcess    ifrit.Process
	natsClient           diegonats.NATSClient
	syncInterval         time.Duration
	consulClusterAddress string
	testMetricsChan      chan *loggregator_v2.Envelope
	signalMetricsChan    chan struct{}

	sqlProcess        ifrit.Process
	sqlRunner         sqlrunner.SQLRunner
	bbsRunning        = false
	useLoggregatorV2  bool
	testIngressServer *testhelpers.TestIngressServer

	portAllocator portauthority.PortAllocator
)

func TestRouteEmitter(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(15 * time.Second)
	RunSpecs(t, "Route Emitter Suite")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	emitter, err := gexec.Build("code.cloudfoundry.org/route-emitter/cmd/route-emitter", "-race")
	Expect(err).NotTo(HaveOccurred())

	bbs, err := gexec.Build("code.cloudfoundry.org/bbs/cmd/bbs", "-race")
	Expect(err).NotTo(HaveOccurred())

	locket, err := gexec.Build("code.cloudfoundry.org/locket/cmd/locket", "-race")
	Expect(err).NotTo(HaveOccurred())

	routingAPI, err := gexec.Build("code.cloudfoundry.org/routing-api/cmd/routing-api", "-race")
	Expect(err).NotTo(HaveOccurred())

	payload, err := json.Marshal(map[string]string{
		"emitter":     emitter,
		"bbs":         bbs,
		"locket":      locket,
		"routing-api": routingAPI,
	})

	Expect(err).NotTo(HaveOccurred())

	return payload
}, func(payload []byte) {
	binaries := map[string]string{}

	err := json.Unmarshal(payload, &binaries)
	Expect(err).NotTo(HaveOccurred())

	emitterPath = string(binaries["emitter"])

	dbName := fmt.Sprintf("diego_%d", GinkgoParallelNode())
	sqlRunner = test_helpers.NewSQLRunner(dbName)

	node := GinkgoParallelNode()
	startPort := 1050 * node
	portRange := 1000
	endPort := startPort + portRange

	portAllocator, err = portauthority.New(startPort, endPort)
	Expect(err).NotTo(HaveOccurred())

	port, err := portAllocator.ClaimPorts(consulrunner.PortOffsetLength)
	Expect(err).NotTo(HaveOccurred())

	consulRunner = consulrunner.NewClusterRunner(
		consulrunner.ClusterRunnerConfig{
			StartingPort: int(port),
			NumNodes:     1,
			Scheme:       "http",
		},
	)

	natsPort, err = portAllocator.ClaimPorts(1)
	Expect(err).NotTo(HaveOccurred())

	syncInterval = 200 * time.Millisecond

	bbsPath = string(binaries["bbs"])
	locketPath = string(binaries["locket"])
	bbsPort, err := portAllocator.ClaimPorts(2)
	Expect(err).NotTo(HaveOccurred())
	bbsAddress := fmt.Sprintf("127.0.0.1:%d", bbsPort)
	bbsHealthAddress := fmt.Sprintf("127.0.0.1:%d", bbsPort+1)
	routingAPIPath = string(binaries["routing-api"])

	bbsURL = &url.URL{
		Scheme: "https",
		Host:   bbsAddress,
	}

	basePath := path.Join(os.Getenv("GOPATH"), "src/code.cloudfoundry.org/route-emitter/cmd/route-emitter/fixtures")

	bbsConfig = bbsconfig.BBSConfig{
		SessionName:                     "bbs",
		CommunicationTimeout:            durationjson.Duration(10 * time.Second),
		RequireSSL:                      true,
		DesiredLRPCreationTimeout:       durationjson.Duration(1 * time.Minute),
		ExpireCompletedTaskDuration:     durationjson.Duration(2 * time.Minute),
		ExpirePendingTaskDuration:       durationjson.Duration(30 * time.Minute),
		EnableConsulServiceRegistration: false,
		ConvergeRepeatInterval:          durationjson.Duration(30 * time.Second),
		KickTaskDuration:                durationjson.Duration(30 * time.Second),
		LockTTL:                         durationjson.Duration(locket.DefaultSessionTTL),
		LockRetryInterval:               durationjson.Duration(locket.RetryInterval),
		ReportInterval:                  durationjson.Duration(1 * time.Minute),
		ConvergenceWorkers:              20,
		UpdateWorkers:                   1000,
		TaskCallbackWorkers:             1000,
		MaxOpenDatabaseConnections:      200,
		MaxIdleDatabaseConnections:      200,
		AuctioneerRequireTLS:            false,
		RepClientSessionCacheSize:       0,
		RepRequireTLS:                   false,
		LagerConfig:                     lagerflags.DefaultLagerConfig(),

		ListenAddress:            bbsAddress,
		AdvertiseURL:             bbsURL.String(),
		AuctioneerAddress:        "http://some-address",
		DatabaseDriver:           sqlRunner.DriverName(),
		DatabaseConnectionString: sqlRunner.ConnectionString(),
		ConsulCluster:            consulRunner.ConsulCluster(),
		HealthAddress:            bbsHealthAddress,

		EncryptionConfig: encryption.EncryptionConfig{
			EncryptionKeys: map[string]string{"label": "key"},
			ActiveKeyLabel: "label",
		},

		CaFile:   path.Join(basePath, "green-certs", "server-ca.crt"),
		CertFile: path.Join(basePath, "green-certs", "server.crt"),
		KeyFile:  path.Join(basePath, "green-certs", "server.key"),
	}
})

func startOAuthServer() *ghttp.Server {
	server := ghttp.NewUnstartedServer()
	tlsConfig, err := tlsconfig.Build(
		tlsconfig.WithInternalServiceDefaults(),
		tlsconfig.WithIdentityFromFile("fixtures/server.crt", "fixtures/server.key"),
	).Server()
	Expect(err).NotTo(HaveOccurred())
	tlsConfig.ClientAuth = tls.NoClientCert

	server.HTTPTestServer.TLS = tlsConfig
	server.AllowUnhandledRequests = true
	server.UnhandledRequestStatusCode = http.StatusOK

	server.HTTPTestServer.StartTLS()

	publicKey := "-----BEGIN PUBLIC KEY-----\\n" +
		"MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDHFr+KICms+tuT1OXJwhCUmR2d\\n" +
		"KVy7psa8xzElSyzqx7oJyfJ1JZyOzToj9T5SfTIq396agbHJWVfYphNahvZ/7uMX\\n" +
		"qHxf+ZH9BL1gk9Y6kCnbM5R60gfwjyW1/dQPjOzn9N394zd2FJoFHwdq9Qs0wBug\\n" +
		"spULZVNRxq7veq/fzwIDAQAB\\n" +
		"-----END PUBLIC KEY-----"

	data := fmt.Sprintf("{\"alg\":\"rsa\", \"value\":\"%s\"}", publicKey)
	server.RouteToHandler("GET", "/token_key",
		ghttp.CombineHandlers(
			ghttp.VerifyRequest("GET", "/token_key"),
			ghttp.RespondWith(http.StatusOK, data)),
	)
	server.RouteToHandler("POST", "/oauth/token",
		ghttp.CombineHandlers(
			ghttp.VerifyBasicAuth("someclient", "somesecret"),
			func(w http.ResponseWriter, req *http.Request) {
				jsonBytes := []byte(`{"access_token":"some-token", "expires_in":10}`)
				w.Write(jsonBytes)
			}))

	return server
}

var _ = BeforeEach(func() {
	cfgs = nil
	useLoggregatorV2 = false

	oauthServer = startOAuthServer()

	consulRunner.Start()
	consulRunner.WaitUntilReady()
	consulClusterAddress = consulRunner.ConsulCluster()

	sqlProcess = ginkgomon.Invoke(sqlRunner)

	startBBS()

	natsServerProcess, natsClient = natsserverrunner.StartNatsServer(int(natsPort))

	healthCheckPort, err := portAllocator.ClaimPorts(1)
	Expect(err).NotTo(HaveOccurred())
	healthCheckAddress = fmt.Sprintf("127.0.0.1:%d", healthCheckPort)
})

var _ = JustBeforeEach(func() {
	var err error
	testIngressServer, err = testhelpers.NewTestIngressServer(
		"fixtures/metron/metron.crt",
		"fixtures/metron/metron.key",
		"fixtures/metron/CA.crt",
	)
	Expect(err).NotTo(HaveOccurred())
	receiversChan := testIngressServer.Receivers()
	Expect(testIngressServer.Start()).To(Succeed())
	port, err := testIngressServer.Port()
	Expect(err).NotTo(HaveOccurred())
	cfgs = append(cfgs, func(cfg *config.RouteEmitterConfig) {
		cfg.LoggregatorConfig.BatchFlushInterval = 10 * time.Millisecond
		cfg.LoggregatorConfig.BatchMaxSize = 1
		cfg.LoggregatorConfig.UseV2API = useLoggregatorV2
		cfg.LoggregatorConfig.APIPort = port
		cfg.LoggregatorConfig.CACertPath = "fixtures/metron/CA.crt"
		cfg.LoggregatorConfig.KeyPath = "fixtures/metron/client.key"
		cfg.LoggregatorConfig.CertPath = "fixtures/metron/client.crt"
	})
	testMetricsChan, signalMetricsChan = testhelpers.TestMetricChan(receiversChan)
})

var _ = AfterEach(func() {
	stopBBS()
	consulRunner.Stop()
	natsServerProcess.Signal(os.Kill)
	Eventually(natsServerProcess.Wait(), 5).Should(Receive())

	testIngressServer.Stop()
	close(signalMetricsChan)

	ginkgomon.Kill(sqlProcess, 5*time.Second)
})

var _ = SynchronizedAfterSuite(func() {
	oauthServer.Close()
}, func() {
	gexec.CleanupBuildArtifacts()
})

func stopBBS() {
	if !bbsRunning {
		return
	}

	bbsRunning = false
	ginkgomon.Kill(bbsProcess)
	Eventually(bbsProcess.Wait()).Should(Receive())
}

func startBBS() {
	if bbsRunning {
		return
	}

	bbsRunner = bbstestrunner.New(bbsPath, bbsConfig)
	bbsProcess = ginkgomon.Invoke(bbsRunner)
	bbsRunning = true
}
