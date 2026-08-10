package api

import (
	"net/http"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func (s *Server) getLocomotive(w http.ResponseWriter, r *http.Request) {
	x, err := s.railway.Locomotive(r.Context(), r.PathValue("id"))
	if err != nil {
		writeProblem(w, statusFor(err), "locomotive_lookup_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, x)
}

func (s *Server) createLocomotive(w http.ResponseWriter, r *http.Request) {
	var req model.LocomotiveInput
	if !decodeJSON(w, r, &req) {
		return
	}
	x, err := s.railway.CreateLocomotive(r.Context(), userFrom(r), req)
	if err != nil {
		writeProblem(w, statusFor(err), "locomotive_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, x)
}

func (s *Server) updateLocomotive(w http.ResponseWriter, r *http.Request) {
	var req model.LocomotiveInput
	if !decodeJSON(w, r, &req) {
		return
	}
	x, err := s.railway.UpdateLocomotive(r.Context(), userFrom(r), r.PathValue("id"), req)
	if err != nil {
		writeProblem(w, statusFor(err), "locomotive_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, x)
}

func (s *Server) deleteLocomotive(w http.ResponseWriter, r *http.Request) {
	if err := s.railway.DeleteLocomotive(r.Context(), userFrom(r), r.PathValue("id")); err != nil {
		writeProblem(w, statusFor(err), "locomotive_delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
