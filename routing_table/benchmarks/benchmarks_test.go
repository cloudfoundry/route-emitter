package benchmarks_test

import (
	"fmt"
	"strconv"
	"time"

	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/route-emitter/routing_table"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type fakeBenchmarker struct{}

const (
	MaxRoutes = 250000
)

func (fb *fakeBenchmarker) Time(desc string, f func(), whatever ...interface{}) time.Duration {
	start := time.Now()
	f()
	return time.Now().Sub(start)
}
func (fb *fakeBenchmarker) RecordValue(desc string, f float64, whatever ...interface{}) {
}

var _ = Describe("Benchmarks", func() {
	var (
		rt                     routing_table.RoutingTable
		logger                 *lagertest.TestLogger
		registrationMessages   int
		unregistrationMessages int
	)

	BeforeEach(func() {
		logger = lagertest.NewTestLogger("route-emitter-benchmarks")
		rt = routing_table.NewTable(logger)
		registrationMessages, unregistrationMessages = 0, 0
	})

	AfterEach(func() {
		Expect(registrationMessages).To(BeNumerically(">=", MaxRoutes))
	})

	benchmarkAdd := func(startIndex, endIndex, appIndex int) func(Benchmarker) {
		return func(b Benchmarker) {
			for i := startIndex; i < endIndex; i++ {
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
					InstanceGuid:  fmt.Sprintf("%s-%d", guid, appIndex),
					Index:         int32(appIndex),
					Host:          routes[0].Hostname,
					Domain:        "test.domain",
					Port:          1024 + uint32(i*(appIndex+1)),
					ContainerPort: key.ContainerPort,
					Evacuating:    false,
				}
				b.Time("adding route", func() {
					rt.SetRoutes(key, routes, nil)
				})
				b.Time("adding endpoint", func() {
					msgs := rt.AddEndpoint(key, endpoint)
					registrationMessages += len(msgs.RegistrationMessages)
					unregistrationMessages += len(msgs.UnregistrationMessages)
				})
			}
		}
	}

	benchmarkRemove := func(startIndex, endIndex, appIndex int) func(Benchmarker) {
		return func(b Benchmarker) {
			for i := startIndex; i < endIndex; i++ {
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
					InstanceGuid:  fmt.Sprintf("%s-%d", guid, appIndex),
					Index:         int32(appIndex),
					Host:          routes[0].Hostname,
					Domain:        "test.domain",
					Port:          1024 + uint32(i*(appIndex+1)),
					ContainerPort: key.ContainerPort,
					Evacuating:    false,
				}
				b.Time("removing route", func() {
					msgs := rt.RemoveRoutes(key, nil)
					registrationMessages += len(msgs.RegistrationMessages)
					unregistrationMessages += len(msgs.UnregistrationMessages)
				})
				b.Time("removing endpoint", func() {
					rt.RemoveEndpoint(key, endpoint)
					// registrationMessages += len(msgs.RegistrationMessages)
					// unregistrationMessages += len(msgs.UnregistrationMessages)
				})
			}
		}
	}

	newRouteTable := func(startIndex int) routing_table.RoutingTable {
		tmpRt := routing_table.NewTable(logger)
		for i := startIndex; i < startIndex+MaxRoutes; i++ {
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
			tmpRt.SetRoutes(key, routes, nil)
			tmpRt.AddEndpoint(key, endpoint)
		}
		return tmpRt
	}

	benchmarkEndpointsForIndex := func(startIndex, endIndex int) func(Benchmarker) {
		return func(b Benchmarker) {
			for i := startIndex; i < endIndex; i++ {
				guid := "app-" + strconv.Itoa(i)
				key := routing_table.RoutingKey{
					ProcessGuid:   guid,
					ContainerPort: 8080,
				}
				b.Time("adding route", func() {
					rt.EndpointsForIndex(key, int32(0))
				})
			}
		}
	}

	Measure("route addition", benchmarkAdd(0, MaxRoutes, 0), 1)

	Context("when the table is already populated", func() {
		BeforeEach(func() {
			benchmarkAdd(0, MaxRoutes, 0)(&fakeBenchmarker{})
		})

		Context("when adding routes", func() {
			Measure("route heartbeats", benchmarkAdd(0, MaxRoutes, 0), 1)
			Measure("more route additions", benchmarkAdd(MaxRoutes, 2*MaxRoutes, 0), 1)
		})

		Context("when removing routes", func() {
			AfterEach(func() {
				Expect(unregistrationMessages).To(BeNumerically(">=", MaxRoutes))
			})

			Measure("remove routes", benchmarkRemove(0, MaxRoutes, 0), 1)
		})

		Measure("getting route info", func(b Benchmarker) {
			for i := 0; i < MaxRoutes; i++ {
				guid := "app-" + strconv.Itoa(i)
				key := routing_table.RoutingKey{
					ProcessGuid:   guid,
					ContainerPort: 8080,
				}
				b.Time("get route", func() {
					rt.GetRoutes(key)
				})
			}
		}, 1)

	})

	Context("swap routing tables", func() {
		tempRoutingTable := newRouteTable(MaxRoutes)
		Measure("swap routes", func(b Benchmarker) {
			benchmarkAdd(0, MaxRoutes, 0)(&fakeBenchmarker{})
			b.Time("swapping table", func() {
				rt.Swap(tempRoutingTable, nil)
			})
		}, 10)
	})

	Context("get endpoint from index", func() {
		BeforeEach(func() {
			for i := 0; i < 5; i++ {
				benchmarkAdd(0, MaxRoutes/5, i)(&fakeBenchmarker{})
			}
		})

		Measure("fetch endpoints", benchmarkEndpointsForIndex(0, MaxRoutes/5), 1)
	})
})
