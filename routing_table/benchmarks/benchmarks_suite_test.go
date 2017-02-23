package benchmarks_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func TestRoutingTable(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Benchmarks Suite")
}
