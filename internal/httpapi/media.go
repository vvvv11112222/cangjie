package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vvvv11112222/cangjie/internal/media"
)

func (s *server) registerMediaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/sources", s.createSource)
	mux.HandleFunc("POST /api/v1/sources/{id}/verify", s.verifySource)
	mux.HandleFunc("POST /api/v1/sources/{id}/assign-session", s.assignSource)
	mux.HandleFunc("GET /api/v1/sources/{id}", s.getSource)
	mux.HandleFunc("GET /api/v1/sessions/{id}/sources", s.listSources)
	mux.HandleFunc("POST /api/v1/sessions/{id}/media", s.uploadMedia)
	mux.HandleFunc("GET /api/v1/sessions/{id}/media", s.listMedia)
	mux.HandleFunc("GET /api/v1/media/{id}", s.getMedia)
	mux.HandleFunc("GET /api/v1/media/{id}/content", s.getMediaContent)
	mux.HandleFunc("POST /api/v1/sessions/{id}/primary-media", s.selectPrimaryMedia)
}

func (s *server) createSource(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	var in media.CreateSource
	if !s.decode(w, r, &in, false) {
		return
	}
	result, err := s.media.CreateSource(r.Context(), p, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusCreated, result)
}

func (s *server) verifySource(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in media.VerifySource
	if !s.decode(w, r, &in, false) {
		return
	}
	result, err := s.media.VerifySource(r.Context(), p, id, in)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, result)
}

func (s *server) assignSource(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		SessionID string `json:"session_id"`
	}
	if !s.decode(w, r, &in, false) {
		return
	}
	if !validUUID(in.SessionID) {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "session_id must be a UUID", map[string]any{})
		return
	}
	result, err := s.media.AssignSource(r.Context(), p, id, in.SessionID)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, result)
}

func (s *server) getSource(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	result, err := s.media.GetSource(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, result)
}

func (s *server) listSources(w http.ResponseWriter, r *http.Request) {
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
	items, next, err := s.media.ListSources(r.Context(), p, id, after, limit)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, page(items, next))
}

func (s *server) uploadMedia(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.media.AuthorizeUpload(r.Context(), p, sessionID); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Now().Add(s.uploadTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.writeServiceError(w, r, err)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "Content-Type must be multipart/form-data", map[string]any{})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE", "media exceeds MAX_UPLOAD_BYTES", map[string]any{})
			return
		}
		var timeoutErr net.Error
		if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
			writeError(w, r, http.StatusRequestTimeout, "INVALID_ARGUMENT", "upload timed out", map[string]any{})
			return
		}
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart body is invalid", map[string]any{})
		return
	}
	defer r.MultipartForm.RemoveAll() //nolint:errcheck
	for key := range r.MultipartForm.Value {
		if key != "source_record_id" {
			writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart body contains an unknown field", map[string]any{})
			return
		}
	}
	for key := range r.MultipartForm.File {
		if key != "file" {
			writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart body contains an unknown file field", map[string]any{})
			return
		}
	}
	sources, files := r.MultipartForm.Value["source_record_id"], r.MultipartForm.File["file"]
	if len(sources) != 1 || !validUUID(sources[0]) || len(files) != 1 {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "one source_record_id and one file are required", map[string]any{})
		return
	}
	if files[0].Size > s.maxUploadBytes {
		writeError(w, r, http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE", "media exceeds MAX_UPLOAD_BYTES", map[string]any{})
		return
	}
	file, err := files[0].Open()
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(r.Context(), s.uploadTimeout)
	defer cancel()
	result, err := s.media.Upload(ctx, p, sessionID, sources[0], files[0].Filename, files[0].Header.Get("Content-Type"), file)
	if errors.Is(err, context.DeadlineExceeded) {
		writeError(w, r, http.StatusRequestTimeout, "INVALID_ARGUMENT", "upload timed out", map[string]any{})
		return
	}
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusCreated, result)
}

func (s *server) listMedia(w http.ResponseWriter, r *http.Request) {
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
	items, next, err := s.media.ListMedia(r.Context(), p, id, after, limit)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, page(items, next))
}

func (s *server) getMedia(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	result, err := s.media.GetMedia(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, result)
}

func (s *server) selectPrimaryMedia(w http.ResponseWriter, r *http.Request) {
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		MediaAssetID string `json:"media_asset_id"`
	}
	if !s.decode(w, r, &in, false) {
		return
	}
	if !validUUID(in.MediaAssetID) {
		writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "media_asset_id must be a UUID", map[string]any{})
		return
	}
	result, err := s.classroom.SelectPrimaryMedia(r.Context(), p, sessionID, in.MediaAssetID)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, result)
}

func (s *server) getMediaContent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	content, err := s.media.OpenContent(r.Context(), p, id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	defer content.File.Close()
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	start, length, partial, err := parseByteRange(r.Header.Get("Range"), content.Size)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", content.Size))
		writeError(w, r, http.StatusRequestedRangeNotSatisfiable, "RANGE_NOT_SATISFIABLE", "only one satisfiable byte range is supported", map[string]any{})
		return
	}
	if _, err := content.File.Seek(start, io.SeekStart); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", content.MIMEType)
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.Header().Set("Cache-Control", "private, no-store")
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, content.Size))
	}
	w.WriteHeader(status)
	if _, err := io.CopyN(w, content.File, length); err != nil {
		s.logger.Warn("media response interrupted", "request_id", requestID(r.Context()), "error", err)
	}
}

func parseByteRange(header string, size int64) (start, length int64, partial bool, err error) {
	if header == "" {
		return 0, size, false, nil
	}
	if size <= 0 || !strings.HasPrefix(header, "bytes=") || strings.Contains(header, ",") {
		return 0, 0, false, errors.New("invalid range")
	}
	value := strings.TrimSpace(strings.TrimPrefix(header, "bytes="))
	parts := strings.Split(value, "-")
	if len(parts) != 2 {
		return 0, 0, false, errors.New("invalid range")
	}
	if parts[0] == "" {
		suffix, parseErr := strconv.ParseInt(parts[1], 10, 64)
		if parseErr != nil || suffix <= 0 {
			return 0, 0, false, errors.New("invalid suffix")
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, suffix, true, nil
	}
	start, parseErr := strconv.ParseInt(parts[0], 10, 64)
	if parseErr != nil || start < 0 || start >= size {
		return 0, 0, false, errors.New("invalid start")
	}
	end := size - 1
	if parts[1] != "" {
		end, parseErr = strconv.ParseInt(parts[1], 10, 64)
		if parseErr != nil || end < start {
			return 0, 0, false, errors.New("invalid end")
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end - start + 1, true, nil
}
