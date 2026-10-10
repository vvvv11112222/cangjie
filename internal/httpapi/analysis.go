package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

func (s *server) registerAnalysisRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/sessions/{id}/analysis-runs", s.createAnalysisRun)
	mux.HandleFunc("GET /api/v1/sessions/{id}/analysis-runs", s.listAnalysisRuns)
	mux.HandleFunc("GET /api/v1/analysis-runs/{id}", s.getAnalysisRun)
	mux.HandleFunc("POST /api/v1/analysis-runs/{id}/cancel", s.cancelAnalysisRun)
	mux.HandleFunc("GET /api/v1/analysis-runs/{id}/results", s.getAnalysisResults)
	mux.HandleFunc("GET /api/v1/sessions/{id}/reports", s.listReports)
	mux.HandleFunc("GET /api/v1/reports/{id}", s.getReport)
	mux.HandleFunc("POST /api/v1/reports/{id}/revisions", s.copyReport)
	mux.HandleFunc("PATCH /api/v1/reports/{id}", s.patchReport)
	mux.HandleFunc("POST /api/v1/reports/{id}/review", s.reviewReport)
	mux.HandleFunc("POST /api/v1/reports/{id}/publish", s.publishReport)
	mux.HandleFunc("POST /api/v1/reports/{id}/withdraw", s.withdrawReport)
	mux.HandleFunc("GET /api/v1/model-calls", s.listModelCalls)
	mux.HandleFunc("POST /api/v1/model-calls/{id}/reconcile", s.reconcileModelCall)
	mux.HandleFunc("GET /api/v1/sessions/{id}/transcript-revisions", s.listTranscriptRevisions)
	mux.HandleFunc("GET /api/v1/transcript-revisions/{id}", s.getTranscriptRevision)
	mux.HandleFunc("POST /api/v1/sessions/{id}/transcript-revisions", s.createTranscriptRevision)
}

func (s *server) listModelCalls(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	after, limit, ok := s.page(w, r)
	if !ok {
		return
	}
	items, next, err := s.analysis.ListModelCalls(r.Context(), p, after, limit)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, page(items, next))
}

func (s *server) reconcileModelCall(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in analysis.ReconcileCall
	if !s.decode(w, r, &in, false) {
		return
	}
	value, err := s.analysis.ReconcileModelCall(r.Context(), p, id, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) createTranscriptRevision(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in analysis.CreateRevision
	if !s.decode(w, r, &in, false) {
		return
	}
	value, err := s.analysis.CreateRevision(r.Context(), p, id, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusCreated, value)
}

func (s *server) copyReport(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in analysis.VersionReason
	if !s.decode(w, r, &in, false) {
		return
	}
	value, err := s.analysis.CopyReport(r.Context(), p, id, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusCreated, value)
}

func (s *server) patchReport(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in analysis.PatchReport
	if !s.decode(w, r, &in, false) {
		return
	}
	value, err := s.analysis.PatchReport(r.Context(), p, id, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) reviewReport(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in analysis.ReviewReport
	if !s.decode(w, r, &in, false) {
		return
	}
	value, err := s.analysis.Review(r.Context(), p, id, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) publishReport(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in analysis.PublishReport
	if !s.decode(w, r, &in, false) {
		return
	}
	value, err := s.analysis.Publish(r.Context(), p, id, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) withdrawReport(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in analysis.VersionReason
	if !s.decode(w, r, &in, false) {
		return
	}
	value, err := s.analysis.Withdraw(r.Context(), p, id, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) getReport(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	value, err := s.analysis.GetReport(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}
func (s *server) listReports(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	after, limit, ok := s.page(w, r)
	if !ok {
		return
	}
	items, next, err := s.analysis.ListReports(r.Context(), p, id, after, limit)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, page(items, next))
}

func (s *server) getTranscriptRevision(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	value, err := s.analysis.GetRevision(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}
func (s *server) listTranscriptRevisions(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	after, limit, ok := s.pageAllowed(w, r, "media_asset_id")
	if !ok {
		return
	}
	mediaID, ok := queryUUID(w, r, "media_asset_id")
	if !ok {
		return
	}
	items, next, err := s.analysis.ListRevisions(r.Context(), p, id, mediaID, after, limit)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, page(items, next))
}

func (s *server) registerWorkerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/v1/jobs/claim", s.claimJob)
	mux.HandleFunc("POST /internal/v1/jobs/{id}/heartbeat", s.heartbeatJob)
	mux.HandleFunc("GET /internal/v1/jobs/{id}/input", s.getJobInput)
	mux.HandleFunc("POST /internal/v1/jobs/{id}/artifacts", s.uploadJobArtifact)
	mux.HandleFunc("POST /internal/v1/jobs/{id}/complete", s.completeJob)
	mux.HandleFunc("POST /internal/v1/jobs/{id}/fail", s.failJob)
}

func (s *server) createAnalysisRun(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	var in analysis.CreateRun
	if !s.decode(w, r, &in, false) {
		return
	}
	value, replay, err := s.analysis.Create(r.Context(), p, sessionID, key, in)
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

func (s *server) listAnalysisRuns(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	after, limit, ok := s.page(w, r)
	if !ok {
		return
	}
	items, next, err := s.analysis.List(r.Context(), p, sessionID, after, limit)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, page(items, next))
}

func (s *server) getAnalysisRun(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	value, err := s.analysis.Get(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) cancelAnalysisRun(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if !s.decode(w, r, &struct{}{}, false) {
		return
	}
	value, err := s.analysis.Cancel(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) getAnalysisResults(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	value, err := s.analysis.Results(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) claimJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorker(w, r) {
		return
	}
	var in analysis.ClaimRequest
	if !s.decode(w, r, &in, false) {
		return
	}
	if in.WorkerID != s.workerID {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "worker identity does not match its credential", map[string]any{})
		return
	}
	for _, capability := range in.Capabilities {
		if !s.workerCapabilities[capability] {
			writeError(w, r, http.StatusForbidden, "FORBIDDEN", "worker capability is not permitted", map[string]any{})
			return
		}
	}
	if s.governance != nil {
		if err := s.governance.RecordWorker(r.Context(), in.WorkerID, in.Capabilities); err != nil {
			s.writeServiceError(w, r, err)
			return
		}
	}
	claim, err := s.analysis.Claim(r.Context(), in.WorkerID, in.Capabilities)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if claim == nil {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeData(w, r, http.StatusOK, claim)
}

func (s *server) heartbeatJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorker(w, r) {
		return
	}
	id, token, ok := workerJob(w, r)
	if !ok {
		return
	}
	var in analysis.Heartbeat
	if !s.decode(w, r, &in, false) {
		return
	}
	value, err := s.analysis.Heartbeat(r.Context(), id, token, in.Progress)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if s.governance != nil {
		capabilities := make([]string, 0, len(s.workerCapabilities))
		for capability := range s.workerCapabilities {
			capabilities = append(capabilities, capability)
		}
		if err = s.governance.RecordWorker(r.Context(), s.workerID, capabilities); err != nil {
			s.writeServiceError(w, r, err)
			return
		}
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) getJobInput(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorker(w, r) {
		return
	}
	id, token, ok := workerJob(w, r)
	if !ok {
		return
	}
	content, err := s.analysis.OpenInput(r.Context(), id, token)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	defer content.File.Close()
	writeRangedContent(s, w, r, content.File, content.Size, content.MIMEType)
}

func (s *server) uploadJobArtifact(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorker(w, r) {
		return
	}
	id, token, ok := workerJob(w, r)
	if !ok {
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "Content-Type must be multipart/form-data", map[string]any{})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxArtifactBytes+(1<<20))
	if err = r.ParseMultipartForm(32 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE", "artifact exceeds MAX_ARTIFACT_BYTES", map[string]any{})
			return
		}
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart body is invalid", map[string]any{})
		return
	}
	defer r.MultipartForm.RemoveAll() //nolint:errcheck
	allowed := map[string]bool{"artifact_key": true, "kind": true, "timestamp_ms": true, "origin_offset_ms": true}
	for key := range r.MultipartForm.Value {
		if !allowed[key] || len(r.MultipartForm.Value[key]) != 1 {
			writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart body contains invalid fields", map[string]any{})
			return
		}
	}
	for key := range r.MultipartForm.File {
		if key != "file" {
			writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart body contains an unknown file field", map[string]any{})
			return
		}
	}
	files := r.MultipartForm.File["file"]
	if len(files) != 1 {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "one file is required", map[string]any{})
		return
	}
	file, err := files[0].Open()
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	defer file.Close()
	in := analysis.ArtifactInput{ArtifactKey: r.FormValue("artifact_key"), Kind: r.FormValue("kind")}
	origin, err := strconv.ParseInt(r.FormValue("origin_offset_ms"), 10, 64)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "origin_offset_ms must be an integer", map[string]any{})
		return
	}
	in.OriginOffsetMS = origin
	if raw := r.FormValue("timestamp_ms"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "timestamp_ms must be an integer or empty", map[string]any{})
			return
		}
		in.TimestampMS = &value
	}
	value, replay, err := s.analysis.UploadArtifact(r.Context(), id, token, in, file)
	if errors.Is(err, storage.ErrTooLarge) {
		writeError(w, r, http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE", "artifact exceeds MAX_ARTIFACT_BYTES", map[string]any{})
		return
	}
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	writeData(w, r, status, value)
}

func (s *server) completeJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorker(w, r) {
		return
	}
	id, token, ok := workerJob(w, r)
	if !ok {
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "Content-Type must be application/json", map[string]any{})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is invalid", map[string]any{})
		return
	}
	if !json.Valid(raw) {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is invalid", map[string]any{})
		return
	}
	value, err := s.analysis.Complete(r.Context(), id, token, raw)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) failJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorker(w, r) {
		return
	}
	id, token, ok := workerJob(w, r)
	if !ok {
		return
	}
	var in analysis.Fail
	if !s.decode(w, r, &in, false) {
		return
	}
	value, err := s.analysis.Fail(r.Context(), id, token, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, value)
}

func (s *server) requireWorker(w http.ResponseWriter, r *http.Request) bool {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || !identity.SecureEqual(strings.TrimPrefix(header, "Bearer "), s.workerToken) {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "valid worker credential required", map[string]any{})
		return false
	}
	return true
}

func workerJob(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return "", "", false
	}
	token := strings.TrimSpace(r.Header.Get("X-Lease-Token"))
	if !validUUID(token) {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "X-Lease-Token must be a UUID", map[string]any{})
		return "", "", false
	}
	return id, token, true
}

func writeRangedContent(s *server, w http.ResponseWriter, r *http.Request, file io.ReadSeeker, size int64, mimeType string) {
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	start, length, partial, err := parseByteRange(r.Header.Get("Range"), size)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		writeError(w, r, http.StatusRequestedRangeNotSatisfiable, "RANGE_NOT_SATISFIABLE", "only one satisfiable byte range is supported", map[string]any{})
		return
	}
	if _, err = file.Seek(start, io.SeekStart); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, size))
	}
	w.WriteHeader(status)
	if _, err = io.CopyN(w, file, length); err != nil {
		s.logger.Warn("worker input response interrupted", "request_id", requestID(r.Context()), "error", err)
	}
}
