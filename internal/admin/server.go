package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
)

type Server struct {
	socket   string
	mode     os.FileMode
	users    *service.UserService
	server   *http.Server
	listener net.Listener
}

func NewServer(socket string, mode os.FileMode, users *service.UserService) *Server {
	return &Server{socket: socket, mode: mode, users: users}
}
func (s *Server) Start() error {
	_ = os.Remove(s.socket)
	ln, err := net.Listen("unix", s.socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.socket, s.mode); err != nil {
		ln.Close()
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/users", s.listUsers)
	mux.HandleFunc("POST /admin/v1/users", s.createUser)
	mux.HandleFunc("POST /admin/v1/users/{username}/enable", s.enableUser)
	mux.HandleFunc("POST /admin/v1/users/{username}/disable", s.disableUser)
	mux.HandleFunc("PUT /admin/v1/users/{username}/role", s.setRole)
	mux.HandleFunc("PUT /admin/v1/users/{username}/password", s.setPassword)
	s.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	s.listener = ln
	go func() { _ = s.server.Serve(ln) }()
	return nil
}
func (s *Server) Close(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	err := s.server.Shutdown(ctx)
	_ = os.Remove(s.socket)
	return err
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return false
	}
	return true
}
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.users.List(r.Context())
	if err != nil {
		write(w, 500, map[string]string{"error": err.Error()})
		return
	}
	write(w, 200, map[string]any{"items": users})
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username, DisplayName, Password string
		Role                            model.Role
		MustChangePassword              bool
		Bootstrap                       bool
	}
	if !decode(w, r, &req) {
		return
	}
	u, err := s.users.Create(r.Context(), req.Username, req.DisplayName, req.Password, req.Role, req.MustChangePassword, req.Bootstrap)
	if err != nil {
		status := 409
		if errors.Is(err, context.Canceled) {
			status = 500
		}
		write(w, status, map[string]string{"error": err.Error()})
		return
	}
	write(w, 201, u)
}
func (s *Server) enableUser(w http.ResponseWriter, r *http.Request) {
	if err := s.users.SetEnabled(r.Context(), r.PathValue("username"), true); err != nil {
		write(w, 404, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(204)
}
func (s *Server) disableUser(w http.ResponseWriter, r *http.Request) {
	if err := s.users.SetEnabled(r.Context(), r.PathValue("username"), false); err != nil {
		write(w, 404, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(204)
}
func (s *Server) setRole(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role model.Role `json:"role"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.users.SetRole(r.Context(), r.PathValue("username"), req.Role); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(204)
}
func (s *Server) setPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password   string `json:"password"`
		MustChange bool   `json:"mustChange"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.users.SetPassword(r.Context(), r.PathValue("username"), req.Password, req.MustChange); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(204)
}
