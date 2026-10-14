package httpapi

import (
	"io"
	"net/http"
	"strconv"

	"github.com/vvvv11112222/cangjie/internal/governance"
)

func (s *server) registerGovernanceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/reports/{id}/exports", s.createExport)
	mux.HandleFunc("GET /api/v1/exports/{id}", s.getExport)
	mux.HandleFunc("GET /api/v1/exports/{id}/content", s.getExportContent)
	mux.HandleFunc("POST /api/v1/sessions/{id}/deletion-requests", s.createDeletion)
	mux.HandleFunc("GET /api/v1/deletion-requests/{id}", s.getDeletion)
	mux.HandleFunc("GET /api/v1/operations", s.getOperations)
}

func (s *server) createExport(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in governance.CreateExport
	if !s.decode(w, r, &in, false) {
		return
	}
	value, replay, err := s.governance.CreateReportExport(r.Context(), p, id, r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	status := http.StatusAccepted
	if replay {
		status = http.StatusOK
	}
	writeData(w, r, status, value)
}

func (s *server) getExport(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	value, err := s.governance.GetExport(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) getExportContent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	content, err := s.governance.OpenExport(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	defer content.File.Close()
	w.Header().Set("Content-Type", content.MIMEType)
	w.Header().Set("Content-Length", strconv.FormatInt(content.Size, 10))
	w.Header().Set("Content-Disposition", `attachment; filename="`+content.Filename+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if _, err = io.CopyN(w, content.File, content.Size); err != nil {
		s.logger.Warn("export response interrupted", "request_id", requestID(r.Context()), "error", err)
	}
}

func (s *server) createDeletion(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !s.decode(w, r, &in, false) {
		return
	}
	value, replay, err := s.governance.CreateDeletion(r.Context(), p, id, r.Header.Get("Idempotency-Key"), in.Reason)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	status := http.StatusAccepted
	if replay {
		status = http.StatusOK
	}
	writeData(w, r, status, value)
}

func (s *server) getDeletion(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	value, err := s.governance.GetDeletion(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) getOperations(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	value, err := s.governance.GetOperations(r.Context(), p)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}
