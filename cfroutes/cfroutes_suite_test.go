package cfroutes_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func TestCfroutes(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cfroutes Suite")
}
