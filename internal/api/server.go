package api

import (
	"net/http"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

type Server struct {
	mux       *http.ServeMux
	auth      *service.AuthService
	control   *service.ControlService
	railway   *service.RailwayService
	routes    *service.RouteService
	transfer  *transfer.Service
	store     *store.Store
	events    *events.Bus
	station   station.CommandStation
	simulator *simulator.Simulator
}

func New(auth *service.AuthService, control *service.ControlService, railway *service.RailwayService, routes *service.RouteService, transferSvc *transfer.Service, s *store.Store, b *events.Bus, st station.CommandStation, sim *simulator.Simulator, testAPI bool) *Server {
	x := &Server{mux: http.NewServeMux(), auth: auth, control: control, railway: railway, routes: routes, transfer: transferSvc, store: s, events: b, station: st, simulator: sim}
	x.register(testAPI)
	return x
}
func (s *Server) Handler() http.Handler { return securityHeaders(s.mux) }
func (s *Server) register(testAPI bool) {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /api/v1/system/info", s.systemInfo)
	s.mux.HandleFunc("POST /api/v1/auth/login", s.login)
	s.mux.HandleFunc("POST /api/v1/auth/refresh", s.refresh)
	s.mux.Handle("POST /api/v1/auth/logout", s.requireAuth(http.HandlerFunc(s.logout)))
	s.mux.Handle("GET /api/v1/me", s.requireAuth(http.HandlerFunc(s.me)))
	s.mux.Handle("GET /api/v1/track-power", s.requireAuth(http.HandlerFunc(s.trackPowerStatus)))
	s.mux.Handle("GET /api/v1/station/status", s.requireAuth(http.HandlerFunc(s.trackPowerStatus)))
	s.mux.Handle("PUT /api/v1/track-power", s.requireAuth(http.HandlerFunc(s.setTrackPower)))
	s.mux.Handle("POST /api/v1/emergency-stop", s.requireAuth(http.HandlerFunc(s.emergencyStop)))
	s.mux.Handle("GET /api/v1/locomotives", s.requireAuth(http.HandlerFunc(s.listLocomotives)))
	s.mux.Handle("POST /api/v1/locomotives", s.requireAuth(http.HandlerFunc(s.createLocomotive)))
	s.mux.Handle("GET /api/v1/locomotives/{id}", s.requireAuth(http.HandlerFunc(s.getLocomotive)))
	s.mux.Handle("PUT /api/v1/locomotives/{id}", s.requireAuth(http.HandlerFunc(s.updateLocomotive)))
	s.mux.Handle("DELETE /api/v1/locomotives/{id}", s.requireAuth(http.HandlerFunc(s.deleteLocomotive)))
	s.mux.Handle("POST /api/v1/locomotives/{id}/control-lease", s.requireAuth(http.HandlerFunc(s.acquireLease)))
	s.mux.Handle("PUT /api/v1/control-leases/{id}/heartbeat", s.requireAuth(http.HandlerFunc(s.heartbeatLease)))
	s.mux.Handle("DELETE /api/v1/control-leases/{id}", s.requireAuth(http.HandlerFunc(s.releaseLease)))
	s.mux.Handle("PUT /api/v1/locomotives/{id}/throttle", s.requireAuth(http.HandlerFunc(s.throttle)))
	s.mux.Handle("PUT /api/v1/locomotives/{id}/functions/{function}", s.requireAuth(http.HandlerFunc(s.setFunction)))
	s.mux.Handle("GET /api/v1/blocks", s.requireAuth(http.HandlerFunc(s.listBlocks)))
	s.mux.Handle("GET /api/v1/turnouts", s.requireAuth(http.HandlerFunc(s.listTurnouts)))
	s.mux.Handle("PUT /api/v1/turnouts/{id}", s.requireAuth(http.HandlerFunc(s.setTurnout)))
	s.mux.Handle("GET /api/v1/exports/rolling-stock", s.requireAuth(http.HandlerFunc(s.exportRollingStock)))
	s.mux.Handle("POST /api/v1/imports/rolling-stock", s.requireAuth(http.HandlerFunc(s.importRollingStock)))
	s.mux.Handle("GET /api/v1/layout/export", s.requireAuth(http.HandlerFunc(s.exportLayout)))
	s.mux.Handle("POST /api/v1/layout/import", s.requireAuth(http.HandlerFunc(s.importLayout)))
	s.mux.Handle("GET /api/v1/routes", s.requireAuth(http.HandlerFunc(s.listRoutes)))
	s.mux.Handle("POST /api/v1/routes/{id}/reserve", s.requireAuth(http.HandlerFunc(s.reserveRoute)))
	s.mux.Handle("POST /api/v1/routes/{id}/activate", s.requireAuth(http.HandlerFunc(s.activateRoute)))
	s.mux.Handle("POST /api/v1/routes/{id}/release", s.requireAuth(http.HandlerFunc(s.releaseRoute)))
	s.mux.Handle("GET /api/v1/events", s.requireAuth(http.HandlerFunc(s.eventsWebSocket)))
	if testAPI && s.simulator != nil {
		s.mux.Handle("POST /test/v1/simulator/blocks/{id}/occupancy", s.requireAuth(http.HandlerFunc(s.testBlockOccupancy)))
	}
}
