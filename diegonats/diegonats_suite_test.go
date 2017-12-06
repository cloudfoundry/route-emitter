package diegonats_test

import (
	"testing"

	"code.cloudfoundry.org/inigo/helpers/portauthority"
	"code.cloudfoundry.org/route-emitter/diegonats/gnatsdrunner"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

func TestDiegoNATS(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Diego NATS Suite")
}

var (
	natsPort      uint16
	gnatsdProcess ifrit.Process
	portAllocator portauthority.PortAllocator
)

var _ = BeforeSuite(func() {
	node := GinkgoParallelNode()
	startPort := 1050 * node // make sure we don't conflict with etcd ports 4000+GinkgoParallelNode & 7000+GinkgoParallelNode (4000,7000,40001,70001...)
	portRange := 1000
	endPort := startPort + portRange*(node+1)
	var err error
	portAllocator, err = portauthority.New(startPort, endPort)
	Expect(err).NotTo(HaveOccurred())

	natsPort, err = portAllocator.ClaimPorts(1)
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
})

func startNATS() {
	gnatsdProcess = ginkgomon.Invoke(gnatsdrunner.NewGnatsdTestRunner(int(natsPort)))
}

func stopNATS() {
	ginkgomon.Kill(gnatsdProcess)
}
