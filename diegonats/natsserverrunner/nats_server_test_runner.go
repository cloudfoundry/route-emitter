package natsserverrunner

import (
	"fmt"
	"os/exec"
	"strconv"
	"time"

	"code.cloudfoundry.org/route-emitter/diegonats"

	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

func StartNatsServer(natsPort int) (ifrit.Process, diegonats.NATSClient) {
	ginkgomonRunner := NewNatsServerTestRunner(natsPort)
	natsServerProcess := ifrit.Invoke(ginkgomonRunner)
	Eventually(natsServerProcess.Ready(), "5s").Should(BeClosed())

	natsClient := diegonats.NewClient()
	_, err := natsClient.Connect([]string{fmt.Sprintf("nats://127.0.0.1:%d", natsPort)})
	Expect(err).ShouldNot(HaveOccurred())

	return natsServerProcess, natsClient
}

func NewNatsServerTestRunner(natsPort int) *ginkgomon.Runner {
	natsServerPath, err := exec.LookPath("nats-server")
	Expect(err).NotTo(HaveOccurred(), "You need nats-server installed!")

	return ginkgomon.New(ginkgomon.Config{
		Name:              "nats-server",
		AnsiColorCode:     "99m",
		StartCheck:        "Server is ready",
		StartCheckTimeout: 5 * time.Second,
		Command: exec.Command(
			natsServerPath,
			"-p", strconv.Itoa(natsPort),
		),
	})
}
