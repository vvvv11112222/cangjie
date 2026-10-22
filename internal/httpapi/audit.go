package httpapi

import (
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/vvvv11112222/cangjie/internal/governance"
)

func (s *server) listAudit(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	q := governance.AuditQuery{Limit: 20, Action: r.URL.Query().Get("action"), Result: r.URL.Query().Get("result")}
	bad := func() {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid audit query", map[string]any{})
	}
	for key, values := range r.URL.Query() {
		if (key != "cursor" && key != "limit" && key != "action" && key != "result") || len(values) != 1 {
			bad()
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			bad()
			return
		}
		q.Limit = n
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			bad()
			return
		}
		n, err := strconv.ParseInt(string(decoded), 10, 64)
		if err != nil || n <= 0 {
			bad()
			return
		}
		q.Before = n
	}
	items, next, err := s.governance.ListAudit(r.Context(), p, q)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, page(items, next))
}
