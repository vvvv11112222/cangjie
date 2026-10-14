package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/vvvv11112222/cangjie/internal/academic"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/classroom"
	"github.com/vvvv11112222/cangjie/internal/governance"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/media"
)

type CheckFunc func(context.Context) error
type ReadinessChecks map[string]CheckFunc

type Options struct {
	Logger             *slog.Logger
	Readiness          ReadinessChecks
	Identity           *identity.Service
	Academic           *academic.Service
	Classroom          *classroom.Service
	Media              *media.Service
	Analysis           *analysis.Service
	Governance         *governance.Service
	WorkerToken        string
	WorkerID           string
	WorkerCapabilities []string
	MaxArtifactBytes   int64
	PublicOrigin       string
	SecureCookie       bool
	SessionTTL         time.Duration
	MaxUploadBytes     int64
	UploadTimeout      time.Duration
}

type server struct {
	logger             *slog.Logger
	readiness          ReadinessChecks
	identity           *identity.Service
	academic           *academic.Service
	classroom          *classroom.Service
	media              *media.Service
	analysis           *analysis.Service
	governance         *governance.Service
	workerToken        string
	workerID           string
	workerCapabilities map[string]bool
	maxArtifactBytes   int64
	origin             string
	secureCookie       bool
	sessionTTL         time.Duration
	maxUploadBytes     int64
	uploadTimeout      time.Duration
}

type contextKey string

const requestIDKey contextKey = "request_id"

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func New(options Options) http.Handler {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &server{
		logger: logger, readiness: options.Readiness,
		identity: options.Identity, academic: options.Academic, classroom: options.Classroom, media: options.Media, analysis: options.Analysis, governance: options.Governance,
		workerToken: options.WorkerToken, workerID: options.WorkerID, maxArtifactBytes: options.MaxArtifactBytes,
		origin:       strings.TrimRight(options.PublicOrigin, "/"),
		secureCookie: options.SecureCookie, sessionTTL: options.SessionTTL,
		maxUploadBytes: options.MaxUploadBytes, uploadTimeout: options.UploadTimeout,
	}
	s.workerCapabilities = map[string]bool{}
	for _, capability := range options.WorkerCapabilities {
		s.workerCapabilities[capability] = true
	}
	if s.sessionTTL <= 0 {
		s.sessionTTL = 8 * time.Hour
	}
	if s.maxUploadBytes <= 0 {
		s.maxUploadBytes = 4 << 30
	}
	if s.uploadTimeout <= 0 {
		s.uploadTimeout = 30 * time.Minute
	}
	if s.maxArtifactBytes <= 0 {
		s.maxArtifactBytes = 4 << 30
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", requireGet(s.live))
	mux.HandleFunc("/health/ready", requireGet(s.ready))
	if s.identity != nil && s.academic != nil {
		s.registerBusinessRoutes(mux)
	}
	if s.analysis != nil {
		s.registerWorkerRoutes(mux)
	}
	mux.HandleFunc("/", s.notFound)
	return s.requestID(s.recoverPanic(s.accessLog(mux)))
}

func requireGet(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, r, http.StatusMethodNotAllowed, "INVALID_ARGUMENT", "method not allowed", map[string]any{})
			return
		}
		next(w, r)
	}
}

func (s *server) live(w http.ResponseWriter, r *http.Request) {
	writeData(w, r, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *server) ready(w http.ResponseWriter, r *http.Request) {
	names := make([]string, 0, len(s.readiness))
	for name := range s.readiness {
		names = append(names, name)
	}
	sort.Strings(names)
	checks := make(map[string]string, len(names))
	for _, name := range names {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		err := s.readiness[name](ctx)
		cancel()
		if err != nil {
			checks[name] = "unavailable"
			s.logger.Warn("readiness check failed", "request_id", requestID(r.Context()), "check", name, "error", err)
			w.Header().Set("Retry-After", "5")
			writeError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service is not ready", map[string]any{"checks": checks})
			return
		}
		checks[name] = "ok"
	}
	writeData(w, r, http.StatusOK, map[string]any{"status": "ready", "checks": checks})
}

func (s *server) notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found", map[string]any{})
}

func (s *server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if !validRequestID.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (s *server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		response := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(response, r)
		s.logger.Info("http request",
			"request_id", requestID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", response.status,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func (s *server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("request panic", "request_id", requestID(r.Context()), "panic", recovered, "stack", string(debug.Stack()))
				writeError(w, r, http.StatusInternalServerError, "INTERNAL", "internal server error", map[string]any{})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeData(w http.ResponseWriter, r *http.Request, status int, data any) {
	writeJSON(w, status, map[string]any{"data": data, "request_id": requestID(r.Context())})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	writeJSON(w, status, map[string]any{
		"error":      map[string]any{"code": code, "message": message, "details": details},
		"request_id": requestID(r.Context()),
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write JSON response", "error", err)
	}
}

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func newRequestID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}
