package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vvvv11112222/cangjie/internal/capture"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

type exchangeLimit struct {
	sync.Mutex
	start time.Time
	count int
}

func (s *server) registerCaptureRoutes(mux *http.ServeMux) {
	limit := &exchangeLimit{}
	wrap := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if s.capture == nil {
				writeError(w, r, 503, "SERVICE_UNAVAILABLE", "live capture is not configured", map[string]any{})
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
			next(w, r)
		}
	}
	mux.HandleFunc("POST /api/v1/sessions/{id}/live-captures", wrap(s.createCapture))
	mux.HandleFunc("GET /api/v1/sessions/{id}/live-captures/active", wrap(s.activeCapture))
	mux.HandleFunc("POST /api/v1/live-captures/{id}/pairings", wrap(s.pairCapture))
	mux.HandleFunc("POST /api/v1/capture-pairings/exchange", wrap(func(w http.ResponseWriter, r *http.Request) {
		// Global bound also protects the database from distributed failed exchanges.
		limit.Lock()
		if time.Since(limit.start) > time.Minute {
			limit.start = time.Now()
			limit.count = 0
		}
		limit.count++
		allowed := limit.count <= 30
		limit.Unlock()
		if !allowed {
			w.Header().Set("Retry-After", "60")
			writeError(w, r, 429, "RATE_LIMITED", "try pairing again later", map[string]any{})
			return
		}
		var in capture.Exchange
		if !s.decode(w, r, &in, false) {
			return
		}
		v, e := s.capture.Exchange(r.Context(), in)
		if e != nil {
			s.writeServiceError(w, r, e)
			return
		}
		writeData(w, r, 200, v)
	}))
	mux.HandleFunc("GET /api/v1/live-captures/{id}", wrap(s.getCapture))
	mux.HandleFunc("POST /api/v1/live-captures/{id}/viewer-sessions", wrap(s.viewCapture))
	mux.HandleFunc("POST /api/v1/live-captures/{id}/stop", wrap(s.stopCapture))
	mux.HandleFunc("POST /internal/v1/streams/auth", wrap(func(w http.ResponseWriter, r *http.Request) {
		host, _, e := net.SplitHostPort(r.RemoteAddr)
		if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			writeError(w, r, 403, "FORBIDDEN", "internal callback only", map[string]any{})
			return
		}
		var in capture.AuthRequest
		if !s.decode(w, r, &in, false) {
			return
		}
		if e := s.capture.Authenticate(r.Context(), in); e != nil {
			s.writeServiceError(w, r, e)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /internal/v1/streams/events", wrap(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || !identity.SecureEqual(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), s.capture.EventToken()) {
			writeError(w, r, 401, "UNAUTHENTICATED", "event authentication required", map[string]any{})
			return
		}
		var in capture.Event
		if !s.decode(w, r, &in, false) {
			return
		}
		if e := s.capture.Event(r.Context(), in); e != nil {
			s.writeServiceError(w, r, e)
			return
		}
		w.WriteHeader(204)
	}))
}
func (s *server) activeCapture(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	v, e := s.capture.Active(r.Context(), p, id)
	if e != nil {
		s.writeServiceError(w, r, e)
		return
	}
	writeData(w, r, 200, v)
}
func (s *server) capturePrincipal(w http.ResponseWriter, r *http.Request, id string, write bool) (identity.Principal, bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		if !strings.HasPrefix(h, "Bearer ") {
			writeError(w, r, 401, "UNAUTHENTICATED", "Bearer authentication required", map[string]any{})
			return identity.Principal{}, false
		}
		p, e := s.capture.ControlPrincipal(r.Context(), id, strings.TrimPrefix(h, "Bearer "))
		if e != nil {
			s.writeServiceError(w, r, e)
			return p, false
		}
		return p, true
	}
	if write {
		return s.writePrincipal(w, r)
	}
	return s.readPrincipal(w, r)
}
func (s *server) createCapture(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in capture.Create
	if !s.decode(w, r, &in, false) {
		return
	}
	if !validUUID(in.SourceID) {
		writeError(w, r, 400, "INVALID_ARGUMENT", "source_record_id must be a UUID", map[string]any{})
		return
	}
	v, created, e := s.capture.Create(r.Context(), p, id, r.Header.Get("Idempotency-Key"), in)
	if e != nil {
		s.writeServiceError(w, r, e)
		return
	}
	status := 200
	if created {
		status = 201
	}
	writeData(w, r, status, v)
}
func (s *server) pairCapture(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok || !s.decode(w, r, &struct{}{}, false) {
		return
	}
	v, e := s.capture.Pair(r.Context(), p, id)
	if e != nil {
		s.writeServiceError(w, r, e)
		return
	}
	writeData(w, r, 201, v)
}
func (s *server) getCapture(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, ok := s.capturePrincipal(w, r, id, false)
	if !ok {
		return
	}
	v, e := s.capture.Get(r.Context(), p, id)
	if e != nil {
		s.writeServiceError(w, r, e)
		return
	}
	if r.Header.Get("Authorization") != "" {
		actions := []string{}
		for _, a := range v.AllowedActions {
			if a == "read" || a == "stop" {
				actions = append(actions, a)
			}
		}
		v.AllowedActions = actions
	}
	writeData(w, r, 200, v)
}
func (s *server) viewCapture(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok || !s.decode(w, r, &struct{}{}, false) {
		return
	}
	v, e := s.capture.View(r.Context(), p, id)
	if e != nil {
		s.writeServiceError(w, r, e)
		return
	}
	writeData(w, r, 201, v)
}
func (s *server) stopCapture(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, ok := s.capturePrincipal(w, r, id, true)
	if !ok || !s.decode(w, r, &struct{}{}, false) {
		return
	}
	v, e := s.capture.Stop(r.Context(), p, id)
	if e != nil {
		s.writeServiceError(w, r, e)
		return
	}
	status := 202
	if v.State == "ready" || v.State == "failed" {
		status = 200
	}
	writeData(w, r, status, v)
}
