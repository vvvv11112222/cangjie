package httpapi

import (
	"github.com/vvvv11112222/cangjie/internal/modelsettings"
	"net/http"
)

func (s *server) getModelSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if !p.Has("sys_admin") {
		writeError(w, r, 403, "FORBIDDEN", "system administrator required", map[string]any{})
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, r, 400, "INVALID_ARGUMENT", "this endpoint takes no query parameters", map[string]any{})
		return
	}
	if s.modelSettings == nil {
		writeError(w, r, 503, "SERVICE_UNAVAILABLE", "web model configuration is not enabled on this API", map[string]any{})
		return
	}
	value, err := s.modelSettings.Get(r.Context(), p)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, 200, value)
}
func (s *server) putModelSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if !p.Has("sys_admin") {
		writeError(w, r, 403, "FORBIDDEN", "system administrator required", map[string]any{})
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, r, 400, "INVALID_ARGUMENT", "this endpoint takes no query parameters", map[string]any{})
		return
	}
	if s.modelSettings == nil {
		writeError(w, r, 503, "SERVICE_UNAVAILABLE", "web model configuration is not enabled on this API", map[string]any{})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	var input modelsettings.Update
	if !s.decode(w, r, &input, false) {
		return
	}
	value, err := s.modelSettings.Apply(r.Context(), p, input, requestID(r.Context()))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, 200, value)
}
