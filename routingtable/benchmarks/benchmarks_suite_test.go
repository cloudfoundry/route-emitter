package benchmarks_test

import (
	"os"
	"strconv"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func TestRoutingTable(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Benchmarks Suite")
}

func numSamples() int {
	numSamples := 20
	samples := os.Getenv("NUM_SAMPLES")
	if samples != "" {
		var err error
		numSamples, err = strconv.Atoi(samples)
		if err != nil {
			return 20
		}
	}

	return numSamples
}
