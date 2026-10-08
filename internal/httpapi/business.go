package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vvvv11112222/cangjie/internal/academic"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/classroom"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

type listData[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func (s *server) registerBusinessRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/csrf", s.csrf)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("GET /api/v1/auth/me", s.me)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("GET /api/v1/users", s.listUsers)
	mux.HandleFunc("GET /api/v1/users/{id}", s.getUser)
	mux.HandleFunc("POST /api/v1/users", s.createUser)
	mux.HandleFunc("PATCH /api/v1/users/{id}", s.patchUser)
	mux.HandleFunc("POST /api/v1/users/{id}/role-bindings", s.createRole)
	mux.HandleFunc("DELETE /api/v1/users/{id}/role-bindings/{binding_id}", s.deleteRole)

	registerResource(s, mux, "org-units", s.academic.ListOrgUnits, s.academic.GetOrgUnit, s.academic.CreateOrgUnit, s.academic.PatchOrgUnit)
	registerResource(s, mux, "terms", s.academic.ListTerms, s.academic.GetTerm, s.academic.CreateTerm, s.academic.PatchTerm)
	registerResource(s, mux, "courses", s.academic.ListCourses, s.academic.GetCourse, s.academic.CreateCourse, s.academic.PatchCourse)
	registerResource(s, mux, "class-groups", s.academic.ListClassGroups, s.academic.GetClassGroup, s.academic.CreateClassGroup, s.academic.PatchClassGroup)
	registerResource(s, mux, "classrooms", s.academic.ListClassrooms, s.academic.GetClassroom, s.academic.CreateClassroom, s.academic.PatchClassroom)
	registerResource(s, mux, "offerings", s.academic.ListOfferings, s.academic.GetOffering, s.academic.CreateOffering, s.academic.PatchOffering)
	s.registerScheduleRoutes(mux)
	if s.classroom != nil {
		s.registerSessionRoutes(mux)
	}
	if s.media != nil && s.classroom != nil {
		s.registerMediaRoutes(mux)
	}
}

func (s *server) registerScheduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/schedules", s.listSchedules)
	mux.HandleFunc("GET /api/v1/schedules/{id}", s.getSchedule)
	mux.HandleFunc("POST /api/v1/schedules", s.createSchedule)
	mux.HandleFunc("PATCH /api/v1/schedules/{id}", s.patchSchedule)
	mux.HandleFunc("POST /api/v1/schedules/import", s.importSchedules)
}

func (s *server) registerSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/sessions", s.listSessions)
	mux.HandleFunc("POST /api/v1/sessions", s.createSession)
	mux.HandleFunc("PATCH /api/v1/sessions/{id}", s.patchSession)
}

func (s *server) listSchedules(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	after, limit, ok := s.pageAllowed(w, r, "teacher_id", "classroom_id", "class_group_id", "at", "from", "to")
	if !ok {
		return
	}
	query := academic.ScheduleQuery{After: after, Limit: limit}
	if query.TeacherID, ok = queryUUID(w, r, "teacher_id"); !ok {
		return
	}
	if query.ClassroomID, ok = queryUUID(w, r, "classroom_id"); !ok {
		return
	}
	if query.ClassGroupID, ok = queryUUID(w, r, "class_group_id"); !ok {
		return
	}
	if query.At, ok = queryTime(w, r, "at"); !ok {
		return
	}
	if query.From, ok = queryTime(w, r, "from"); !ok {
		return
	}
	if query.To, ok = queryTime(w, r, "to"); !ok {
		return
	}
	if query.At != nil && (query.From != nil || query.To != nil) || (query.From == nil) != (query.To == nil) || query.From != nil && !query.To.After(*query.From) {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "at is exclusive with a valid from/to pair", map[string]any{})
		return
	}
	items, next, err := s.academic.ListSchedules(r.Context(), p, query)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, page(items, next))
}

func (s *server) getSchedule(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	v, err := s.academic.GetSchedule(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, v)
}

func (s *server) createSchedule(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	var in academic.CreateSchedule
	if !s.decode(w, r, &in, false) {
		return
	}
	v, err := s.academic.CreateSchedule(r.Context(), p, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusCreated, v)
}

func (s *server) patchSchedule(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in academic.PatchSchedule
	if !s.decode(w, r, &in, true) {
		return
	}
	v, err := s.academic.PatchSchedule(r.Context(), p, id, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, v)
}

func (s *server) importSchedules(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	var in academic.ImportSchedules
	if !s.decode(w, r, &in, false) {
		return
	}
	v, err := s.academic.ImportSchedules(r.Context(), p, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusCreated, v)
}

func (s *server) listSessions(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	after, limit, ok := s.pageAllowed(w, r, "college_id", "enrollment_year", "class_group_id", "teacher_id", "from", "to", "status")
	if !ok {
		return
	}
	query := classroom.SessionQuery{After: after, Limit: limit, Status: r.URL.Query().Get("status")}
	if query.CollegeID, ok = queryUUID(w, r, "college_id"); !ok {
		return
	}
	if query.ClassGroupID, ok = queryUUID(w, r, "class_group_id"); !ok {
		return
	}
	if query.TeacherID, ok = queryUUID(w, r, "teacher_id"); !ok {
		return
	}
	if query.From, ok = queryTime(w, r, "from"); !ok {
		return
	}
	if query.To, ok = queryTime(w, r, "to"); !ok {
		return
	}
	if (query.From == nil) != (query.To == nil) || query.From != nil && !query.To.After(*query.From) {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "from/to must be a valid pair", map[string]any{})
		return
	}
	if raw := r.URL.Query().Get("enrollment_year"); raw != "" {
		if raw == "unknown" {
			query.UnknownYear = true
		} else if year, err := strconv.Atoi(raw); err != nil || year < 1900 || year > 2200 {
			writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "enrollment_year is invalid", map[string]any{})
			return
		} else {
			query.EnrollmentYear = &year
		}
	}
	if query.Status != "" && query.Status != "planned" && query.Status != "ready" && query.Status != "archived" && query.Status != "deleting" && query.Status != "deleted" {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "status is invalid", map[string]any{})
		return
	}
	items, next, err := s.classroom.List(r.Context(), p, query)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, page(items, next))
}

func (s *server) createSession(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	var in classroom.CreateSession
	if !s.decode(w, r, &in, false) {
		return
	}
	v, err := s.classroom.Create(r.Context(), p, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusCreated, v)
}

func (s *server) patchSession(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in classroom.PatchSession
	if !s.decode(w, r, &in, true) {
		return
	}
	v, err := s.classroom.Patch(r.Context(), p, id, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, v)
}

func registerResource[C, P, V any](s *server, mux *http.ServeMux, name string, list func(context.Context, identity.Principal, string, int) ([]V, string, error), get func(context.Context, identity.Principal, string) (V, error), create func(context.Context, identity.Principal, C) (V, error), patch func(context.Context, identity.Principal, string, P) (V, error)) {
	base := "/api/v1/" + name
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.readPrincipal(w, r)
		if !ok {
			return
		}
		after, limit, ok := s.page(w, r)
		if !ok {
			return
		}
		items, next, err := list(r.Context(), p, after, limit)
		if err != nil {
			s.writeServiceError(w, r, err)
			return
		}
		writeData(w, r, http.StatusOK, page(items, next))
	})
	mux.HandleFunc("GET "+base+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.readPrincipal(w, r)
		if !ok {
			return
		}
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		value, err := get(r.Context(), p, id)
		if err != nil {
			s.writeServiceError(w, r, err)
			return
		}
		writeData(w, r, http.StatusOK, value)
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.writePrincipal(w, r)
		if !ok {
			return
		}
		var input C
		if !s.decode(w, r, &input, false) {
			return
		}
		value, err := create(r.Context(), p, input)
		if err != nil {
			s.writeServiceError(w, r, err)
			return
		}
		writeData(w, r, http.StatusCreated, value)
	})
	mux.HandleFunc("PATCH "+base+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.writePrincipal(w, r)
		if !ok {
			return
		}
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		var input P
		if !s.decode(w, r, &input, true) {
			return
		}
		value, err := patch(r.Context(), p, id, input)
		if err != nil {
			s.writeServiceError(w, r, err)
			return
		}
		writeData(w, r, http.StatusOK, value)
	})
}

func (s *server) csrf(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if c, err := r.Cookie(identity.SessionCookieName); err == nil {
		if p, err := s.identity.Authenticate(r.Context(), c.Value); err == nil {
			writeData(w, r, http.StatusOK, map[string]any{"csrf_token": identity.CSRFToken(p.Token)})
			return
		}
		s.clearCookie(w, identity.SessionCookieName)
	}
	token, err := identity.RandomToken()
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	s.setCookie(w, identity.PreAuthCookieName, token)
	writeData(w, r, http.StatusOK, map[string]any{"csrf_token": token})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.requireOrigin(w, r) {
		return
	}
	csrf := r.Header.Get("X-CSRF-Token")
	previous := ""
	validCSRF := false
	if cookie, err := r.Cookie(identity.SessionCookieName); err == nil {
		if p, authErr := s.identity.Authenticate(r.Context(), cookie.Value); authErr == nil && identity.SecureEqual(identity.CSRFToken(p.Token), csrf) {
			previous = cookie.Value
			validCSRF = true
		}
	}
	if !validCSRF {
		if cookie, err := r.Cookie(identity.PreAuthCookieName); err == nil && identity.SecureEqual(cookie.Value, csrf) {
			validCSRF = true
		}
	}
	if !validCSRF {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "CSRF validation failed", map[string]any{})
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !s.decode(w, r, &input, false) {
		return
	}
	p, token, err := s.identity.Login(r.Context(), input.Username, input.Password, previous)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	s.setCookie(w, identity.SessionCookieName, token)
	s.clearCookie(w, identity.PreAuthCookieName)
	writeData(w, r, http.StatusOK, map[string]any{"user_id": p.UserID, "roles": p.Roles, "csrf_token": identity.CSRFToken(token)})
}
func (s *server) me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	writeData(w, r, http.StatusOK, map[string]any{"user_id": p.UserID, "display_name": p.DisplayName, "roles": p.Roles, "allowed_actions": p.AllowedActions()})
}
func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	if !s.decode(w, r, &struct{}{}, false) {
		return
	}
	if err := s.identity.Logout(r.Context(), p); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	s.clearCookie(w, identity.SessionCookieName)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	after, limit, ok := s.page(w, r)
	if !ok {
		return
	}
	items, next, err := s.identity.ListUsers(r.Context(), p, after, limit)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, page(items, next))
}
func (s *server) getUser(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	v, err := s.identity.GetUser(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, v)
}
func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	var in identity.CreateUser
	if !s.decode(w, r, &in, false) {
		return
	}
	v, err := s.identity.CreateUser(r.Context(), p, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusCreated, v)
}
func (s *server) patchUser(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in identity.PatchUser
	if !s.decode(w, r, &in, true) {
		return
	}
	v, err := s.identity.PatchUser(r.Context(), p, id, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, v)
}
func (s *server) createRole(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in identity.CreateRoleBinding
	if !s.decode(w, r, &in, false) {
		return
	}
	v, err := s.identity.CreateRole(r.Context(), p, id, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusCreated, v)
}
func (s *server) deleteRole(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	binding, ok := pathID(w, r, "binding_id")
	if !ok {
		return
	}
	if err := s.identity.DeleteRole(r.Context(), p, id, binding); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) readPrincipal(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	cookie, err := r.Cookie(identity.SessionCookieName)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required", map[string]any{})
		return identity.Principal{}, false
	}
	p, err := s.identity.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		s.writeServiceError(w, r, err)
		return identity.Principal{}, false
	}
	return p, true
}
func (s *server) writePrincipal(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return identity.Principal{}, false
	}
	if !s.requireOrigin(w, r) {
		return identity.Principal{}, false
	}
	if !identity.SecureEqual(identity.CSRFToken(p.Token), r.Header.Get("X-CSRF-Token")) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "CSRF validation failed", map[string]any{})
		return identity.Principal{}, false
	}
	return p, true
}
func (s *server) requireOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
	if origin == "" || origin != s.origin {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "request origin is not allowed", map[string]any{})
		return false
	}
	return true
}
func (s *server) setCookie(w http.ResponseWriter, name, value string) {
	maxAge := int(s.sessionTTL.Seconds())
	if name == identity.PreAuthCookieName {
		maxAge = 10 * 60
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/api/v1", HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func (s *server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/api/v1", HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func (s *server) decode(w http.ResponseWriter, r *http.Request, target any, nonEmpty bool) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "Content-Type must be application/json", map[string]any{})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is invalid", map[string]any{})
		return false
	}
	if nonEmpty {
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || len(fields) == 0 {
			writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "request must contain at least one field", map[string]any{})
			return false
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is invalid", map[string]any{"reason": err.Error()})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must contain one JSON object", map[string]any{})
		return false
	}
	return true
}
func (s *server) page(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	return s.pageAllowed(w, r)
}
func (s *server) pageAllowed(w http.ResponseWriter, r *http.Request, extra ...string) (string, int, bool) {
	allowed := map[string]bool{"cursor": true, "limit": true}
	for _, key := range extra {
		allowed[key] = true
	}
	for key, values := range r.URL.Query() {
		if !allowed[key] || len(values) != 1 {
			writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "unknown or repeated query parameter", map[string]any{})
			return "", 0, false
		}
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "limit must be between 1 and 100", map[string]any{})
			return "", 0, false
		}
		limit = n
	}
	after := ""
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || !validUUID(string(decoded)) {
			writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "cursor is invalid", map[string]any{})
			return "", 0, false
		}
		after = string(decoded)
	}
	return after, limit, true
}

func queryUUID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	value := r.URL.Query().Get(name)
	if value != "" && !validUUID(value) {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", name+" must be a UUID", map[string]any{})
		return "", false
	}
	return value, true
}

func queryTime(w http.ResponseWriter, r *http.Request, name string) (*time.Time, bool) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", name+" must be an RFC3339 date-time", map[string]any{})
		return nil, false
	}
	return &parsed, true
}
func page[T any](items []T, next string) listData[T] {
	var cursor *string
	if next != "" {
		value := base64.RawURLEncoding.EncodeToString([]byte(next))
		cursor = &value
	}
	return listData[T]{Items: items, NextCursor: cursor}
}
func pathID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id, err := url.PathUnescape(r.PathValue(name))
	if err != nil || !validUUID(id) {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", name+" must be a UUID", map[string]any{})
		return "", false
	}
	return id, true
}
func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
func (s *server) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var app *apperror.Error
	if errors.As(err, &app) {
		writeError(w, r, app.Status, app.Code, app.Message, app.Details)
		return
	}
	s.logger.Error("request failed", "request_id", requestID(r.Context()), "error", err)
	writeError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error", map[string]any{})
}
