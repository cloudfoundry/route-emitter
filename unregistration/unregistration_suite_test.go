package unregistration_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestUnregistration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Unregistration Suite")
}
