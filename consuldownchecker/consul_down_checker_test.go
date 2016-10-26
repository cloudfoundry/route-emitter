package consuldownchecker_test

import (
	"errors"
	"os"
	"time"

	"code.cloudfoundry.org/clock/fakeclock"
	"code.cloudfoundry.org/consuladapter/fakes"
	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/route-emitter/consuldownchecker"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("ConsulDownChecker", func() {
	var (
		logger        *lagertest.TestLogger
		clock         *fakeclock.FakeClock
		consulClient  *fakes.FakeClient
		statusClient  *fakes.FakeStatus
		retryInterval time.Duration
		signals       chan os.Signal
		ready         chan struct{}

		consulDownChecker *consuldownchecker.ConsulDownChecker
	)

	BeforeEach(func() {
		clock = fakeclock.NewFakeClock(time.Now())
		logger = lagertest.NewTestLogger("test")
		retryInterval = 100 * time.Millisecond
		consulClient = new(fakes.FakeClient)
		statusClient = new(fakes.FakeStatus)
		consulClient.StatusReturns(statusClient)
		signals = make(chan os.Signal)
		ready = make(chan struct{})

		consulDownChecker = consuldownchecker.NewConsulDownChecker(logger, clock, consulClient, retryInterval)
	})

	It("exits quickly if consul has a leader", func() {
		statusClient.LeaderReturns("Pompeius", nil)
		err := consulDownChecker.Run(signals, ready)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).NotTo(BeClosed())
	})

	It("exits quickly with error if consul agent is unreachable", func() {
		statusClient.LeaderReturns("", errors.New("not a five hundred"))
		err := consulDownChecker.Run(signals, ready)
		Expect(err).To(HaveOccurred())
		Expect(ready).NotTo(BeClosed())
	})

	Context("when consul does not have leader", func() {
		var runErrCh chan error

		BeforeEach(func() {
			statusClient.LeaderReturns("", errors.New("Unexpected response code: 500 (rpc error: No cluster leader)"))
			runErrCh = make(chan error)

			go func() {
				defer GinkgoRecover()
				runErrCh <- consulDownChecker.Run(signals, ready)
			}()
			Eventually(ready).Should(BeClosed())
		})

		It("continuously checks for the leader", func() {
			Eventually(logger).Should(gbytes.Say("still-down"))
			clock.WaitForWatcherAndIncrement(retryInterval)
			Eventually(logger).Should(gbytes.Say("still-down"))
		})

		It("exits gracefully when interrupted", func() {
			signals <- os.Interrupt
			Eventually(logger).Should(gbytes.Say("received-signal"))
			var err error
			Eventually(runErrCh).Should(Receive(&err))
			Expect(err).NotTo(HaveOccurred())
		})

		It("exits when there is a leader", func() {
			statusClient.LeaderReturns("Ceasar", nil)
			clock.WaitForWatcherAndIncrement(retryInterval)
			Eventually(logger).Should(gbytes.Say("consul-has-leader"))
			var err error
			Eventually(runErrCh).Should(Receive(&err))
			Expect(err).NotTo(HaveOccurred())
		})

		It("exits with error when consul agent is unreachable", func() {
			statusClient.LeaderReturns("", errors.New("not a five hundred"))
			clock.WaitForWatcherAndIncrement(retryInterval)
			Eventually(logger).Should(gbytes.Say("failed-getting-leader"))
			var err error
			Eventually(runErrCh).Should(Receive(&err))
			Expect(err).To(HaveOccurred())
		})
	})
})
