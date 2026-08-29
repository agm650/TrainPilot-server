package api

import (
	"io"
	"net/http"
	"strings"

	"github.com/agm650/TrainPilot-server/internal/transfer"
)

const archiveContentType = "application/vnd.dcc-control.package+zip"

func (s *Server) exportRollingStock(w http.ResponseWriter, r *http.Request) {
	data, err := s.transfer.ExportRollingStock(r.Context())
	if err != nil {
		writeOperationProblem(w, err, "rolling_stock_export_failed")
		return
	}
	writeArchive(w, "rolling-stock.dcclib", data)
}

func (s *Server) importRollingStock(w http.ResponseWriter, r *http.Request) {
	data, ok := readArchiveBody(w, r)
	if !ok {
		return
	}
	replace, ok := importMode(w, r)
	if !ok {
		return
	}
	if err := s.transfer.ImportRollingStock(r.Context(), userFrom(r), data, replace); err != nil {
		writeOperationProblem(w, err, "rolling_stock_import_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) exportLayout(w http.ResponseWriter, r *http.Request) {
	data, err := s.transfer.ExportLayout(r.Context())
	if err != nil {
		writeOperationProblem(w, err, "layout_export_failed")
		return
	}
	writeArchive(w, "layout.dcclayout", data)
}

func (s *Server) importLayout(w http.ResponseWriter, r *http.Request) {
	data, ok := readArchiveBody(w, r)
	if !ok {
		return
	}
	replace, ok := importMode(w, r)
	if !ok {
		return
	}
	if err := s.transfer.ImportLayout(r.Context(), userFrom(r), data, replace); err != nil {
		writeOperationProblem(w, err, "layout_import_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeArchive(w http.ResponseWriter, filename string, data []byte) {
	w.Header().Set("Content-Type", archiveContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", stringInt(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func readArchiveBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, transfer.MaxArchiveSize)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_archive", err.Error())
		return nil, false
	}
	if len(data) == 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_archive", "archive body is empty")
		return nil, false
	}
	return data, true
}

func importMode(w http.ResponseWriter, r *http.Request) (bool, bool) {
	switch strings.ToLower(r.URL.Query().Get("mode")) {
	case "", "merge":
		return false, true
	case "replace":
		return true, true
	default:
		writeProblem(w, http.StatusBadRequest, "invalid_import_mode", "mode must be merge or replace")
		return false, false
	}
}

func stringInt(v int) string {
	// Avoid fmt on this hot path and keep headers predictable.
	if v == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
