package httpapi

import (
	"github.com/vvvv11112222/cangjie/internal/identity"
	"net/http"
)

func (s *server) registerManagementActions(mux *http.ServeMux) {
	for _, resource := range []string{"org-units", "terms", "courses", "class-groups", "classrooms", "offerings", "schedules"} {
		mux.HandleFunc("DELETE /api/v1/"+resource+"/{id}", func(w http.ResponseWriter, r *http.Request) {
			p, ok := s.writePrincipal(w, r)
			if !ok {
				return
			}
			id, ok := pathID(w, r, "id")
			if !ok {
				return
			}
			if err := s.academic.DeleteUnused(r.Context(), p, resource, id); err != nil {
				s.writeServiceError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
	mux.HandleFunc("POST /api/v1/auth/password", func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.writePrincipal(w, r)
		if !ok {
			return
		}
		var in identity.ChangePassword
		if !s.decode(w, r, &in, false) {
			return
		}
		if err := s.identity.ChangePassword(r.Context(), p, in); err != nil {
			s.writeServiceError(w, r, err)
			return
		}
		s.clearCookie(w, identity.SessionCookieName)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/users/{id}/password", func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.writePrincipal(w, r)
		if !ok {
			return
		}
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		var in identity.ResetPassword
		if !s.decode(w, r, &in, false) {
			return
		}
		if err := s.identity.ResetPassword(r.Context(), p, id, in); err != nil {
			s.writeServiceError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
