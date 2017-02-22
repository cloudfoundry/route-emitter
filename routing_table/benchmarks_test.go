package routing_table_test

import (
	"strconv"
	"time"

	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/route-emitter/routing_table"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type fakeBenchmarker struct{}

func (fb *fakeBenchmarker) Time(desc string, f func(), whatever ...interface{}) time.Duration {
	start := time.Now()
	f()
	return time.Now().Sub(start)
}
func (fb *fakeBenchmarker) RecordValue(desc string, f float64, whatever ...interface{}) {
}

var _ = FDescribe("Benchmarks", func() {
	var (
		rt                      routing_table.RoutingTable
		logger                  *lagertest.TestLogger
		registerationMessages   int
		unregisterationMessages int
	)

	BeforeEach(func() {
		logger = lagertest.NewTestLogger("whatever")
		rt = routing_table.NewTable(logger)
	})

	AfterEach(func() {
		Expect(registerationMessages).To(BeNumerically(">=", 250000))
	})

	benchmark := func(startIndex int) func(Benchmarker) {
		return func(b Benchmarker) {
			for i := startIndex; i < startIndex+250000; i++ {
				guid := "app-" + strconv.Itoa(i)
				key := routing_table.RoutingKey{
					ProcessGuid:   guid,
					ContainerPort: 8080,
				}
				routes := []routing_table.Route{
					{
						Hostname: guid,
						LogGuid:  guid,
					},
				}
				endpoint := routing_table.Endpoint{
					InstanceGuid:  guid,
					Index:         0,
					Host:          routes[0].Hostname,
					Domain:        "test.domain",
					Port:          1024 + uint32(i),
					ContainerPort: key.ContainerPort,
					Evacuating:    false,
				}
				b.Time("adding route", func() {
					rt.SetRoutes(key, routes, nil)
				})
				b.Time("adding endpoint", func() {
					msgs := rt.AddEndpoint(key, endpoint)
					registerationMessages += len(msgs.RegistrationMessages)
					unregisterationMessages += len(msgs.UnregistrationMessages)
				})
			}
		}
	}

	Measure("route addition", benchmark(0), 1)

	Context("when the table is already populated", func() {
		BeforeEach(func() {
			benchmark(0)(&fakeBenchmarker{})
		})

		Measure("route heartbeats", benchmark(0), 1)
		Measure("more route additions", benchmark(250000), 1)
	})
})
