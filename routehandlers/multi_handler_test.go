package routehandlers_test

import (
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/route-emitter/routehandlers"
	"code.cloudfoundry.org/route-emitter/watcher"
	"code.cloudfoundry.org/route-emitter/watcher/fakes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("MultiHandler", func() {
	var (
		fakeHandlers []*fakes.FakeRouteHandler
		multiHandler *routehandlers.MultiHandler
		logger       *lagertest.TestLogger
	)

	BeforeEach(func() {
		fakeHandlers = []*fakes.FakeRouteHandler{
			&fakes.FakeRouteHandler{},
			&fakes.FakeRouteHandler{},
		}

		var hs []watcher.RouteHandler
		for _, h := range fakeHandlers {
			hs = append(hs, h)
		}
		multiHandler = routehandlers.NewMultiHandler(hs...)
		logger = lagertest.NewTestLogger("multihandler")
	})

	Describe("HandleEvent", func() {
		It("calls HandleEvent on sub handlers", func() {
			desiredLRP := &models.DesiredLRP{
				Action: models.WrapAction(&models.RunAction{
					User: "me",
					Path: "ls",
				}),
				Domain:      "tests",
				ProcessGuid: "guid",
				Ports:       []uint32{1111},
				Routes:      nil,
				LogGuid:     "log",
			}

			event := models.NewDesiredLRPRemovedEvent(desiredLRP)
			multiHandler.HandleEvent(logger, event)

			for _, h := range fakeHandlers {
				Expect(h.HandleEventCallCount()).To(Equal(1))
			}
		})
	})

	Describe("Sync", func() {
		It("calls Sync on sub handlers", func() {
			multiHandler.Sync(logger, nil, nil, nil, nil)

			for _, h := range fakeHandlers {
				Expect(h.SyncCallCount()).To(Equal(1))
			}
		})

		Context("when there are cached events", func() {

			It("The sync processes cached events", func() {

				desiredLRP := &models.DesiredLRP{
					Action: models.WrapAction(&models.RunAction{
						User: "me",
						Path: "ls",
					}),
					Domain:      "tests",
					ProcessGuid: "guid",
					Ports:       []uint32{1111},
					Routes:      nil,
					LogGuid:     "log",
				}
				event := models.NewDesiredLRPRemovedEvent(desiredLRP)
				cachedEvents := make(map[string]models.Event)
				cachedEvents[event.Key()] = event

				multiHandler.Sync(logger, nil, nil, nil, cachedEvents)

				for _, h := range fakeHandlers {
					Expect(h.SyncCallCount()).To(Equal(1))
					_, _, _, _, events := h.SyncArgsForCall(0)
					Expect(events).To(Equal(cachedEvents))
				}
			})
		})
	})

	Describe("Emit", func() {
		It("calls Emit on sub handlers", func() {
			multiHandler.Emit(logger)

			for _, h := range fakeHandlers {
				Expect(h.EmitCallCount()).To(Equal(1))
			}
		})
	})

	Describe("ShouldRefreshDesired", func() {
		It("calls ShouldRefreshDesired on sub handlers", func() {
			Expect(multiHandler.ShouldRefreshDesired(nil)).To(BeFalse())

			for _, h := range fakeHandlers {
				Expect(h.ShouldRefreshDesiredCallCount()).To(Equal(1))
			}
		})

		It("returns true if any of the sub handlers return true", func() {
			fakeHandlers[1].ShouldRefreshDesiredReturns(true)
			Expect(multiHandler.ShouldRefreshDesired(nil)).To(BeTrue())
		})
	})

	Describe("RefreshDesired", func() {
		It("calls RefreshDesired on sub handlers", func() {
			multiHandler.RefreshDesired(logger, nil)

			for _, h := range fakeHandlers {
				Expect(h.RefreshDesiredCallCount()).To(Equal(1))
			}
		})
	})
})
