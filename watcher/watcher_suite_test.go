package watcher_test

import (
	"testing"

	"github.com/cloudfoundry/gosteno"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestWatcher(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Watcher Suite")
}

var _ = BeforeSuite(func() {
	gosteno.EnterTestMode()
})
