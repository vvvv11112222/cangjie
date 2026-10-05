package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiveness(t *testing.T) {
	handler := New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("X-Request-ID") == "" || !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected response: headers=%v body=%s", response.Header(), response.Body.String())
	}
}

func TestReadinessFailure(t *testing.T) {
	handler := New(Options{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Readiness: ReadinessChecks{"database": func(context.Context) error { return errors.New("offline") }},
	})
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "5" || !strings.Contains(response.Body.String(), "SERVICE_UNAVAILABLE") {
		t.Fatalf("unexpected response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNotFoundUsesStandardEnvelope(t *testing.T) {
	handler := New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"NOT_FOUND"`) {
		t.Fatalf("unexpected response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMethodNotAllowedUsesStandardEnvelope(t *testing.T) {
	handler := New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	request := httptest.NewRequest(http.MethodPost, "/health/live", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet || !strings.Contains(response.Body.String(), `"code":"INVALID_ARGUMENT"`) {
		t.Fatalf("unexpected response: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestInvalidIncomingRequestIDIsReplaced(t *testing.T) {
	handler := New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set("X-Request-ID", "contains spaces")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get("X-Request-ID"); got == "" || got == "contains spaces" {
		t.Fatalf("invalid request ID was not replaced: %q", got)
	}
}
