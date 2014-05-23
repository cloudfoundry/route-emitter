package syncer_test

import (
	"testing"

	"github.com/cloudfoundry/gosteno"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestSyncer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Syncer Suite")
}

var _ = BeforeSuite(func() {
	gosteno.EnterTestMode()
})
