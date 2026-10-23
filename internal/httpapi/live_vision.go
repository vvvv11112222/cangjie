package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"image/jpeg"
	"math"
	"net/http"
	"sync"
	"time"
)

type liveFrame struct {
	Sequence int64  `json:"sequence"`
	Image    string `json:"image_base64"`
}
type livePerson struct {
	ID   string     `json:"track_id"`
	Box  [4]float64 `json:"bbox"`
	Hand *bool      `json:"hand_raise"`
	Head *bool      `json:"head_down"`
}
type liveResult struct {
	Sequence int64        `json:"sequence"`
	Width    int          `json:"width"`
	Height   int          `json:"height"`
	People   []livePerson `json:"people"`
	Error    *string      `json:"error_code"`
}
type liveWork struct {
	ID        string `json:"request_id"`
	Token     string `json:"lease_token"`
	CaptureID string `json:"capture_id"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	liveFrame
	done      chan liveResult
	expires   time.Time
	claimed   bool
	completed bool
	userID    string
}

// One frame globally; no growing video backlog or disk cache. A timeout or
// disconnect drops the frame. Existing sequential Worker is the sole consumer.
type liveVisionBroker struct {
	sync.Mutex
	work *liveWork
	wake chan struct{}
}

func (b *liveVisionBroker) offer(captureID string, frame liveFrame, width, height int, userID string) *liveWork {
	b.Lock()
	defer b.Unlock()
	if b.work != nil && time.Now().Before(b.work.expires) {
		return nil
	}
	var id [16]byte
	if _, e := rand.Read(id[:]); e != nil {
		return nil
	}
	var token [24]byte
	if _, e := rand.Read(token[:]); e != nil {
		return nil
	}
	w := &liveWork{ID: hex.EncodeToString(id[:]), Token: hex.EncodeToString(token[:]), CaptureID: captureID,
		liveFrame: frame, done: make(chan liveResult, 1), expires: time.Now().Add(40 * time.Second), Width: width, Height: height}
	w.userID = userID
	b.work = w
	if b.wake == nil {
		b.wake = make(chan struct{}, 1)
	}
	select {
	case b.wake <- struct{}{}:
	default:
	}
	return w
}
func (b *liveVisionBroker) drop(w *liveWork) {
	b.Lock()
	defer b.Unlock()
	if b.work == w {
		b.work = nil
	}
}
func (s *server) registerLiveVisionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/live-captures/{id}/vision-frames", s.liveVisionFrame)
	mux.HandleFunc("POST /internal/v1/live-vision/claim", s.claimLiveVision)
	mux.HandleFunc("POST /internal/v1/live-vision/{id}/complete", s.completeLiveVision)
}
func (s *server) liveVisionFrame(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.capture == nil || s.liveVision == nil {
		writeError(w, r, 503, "SERVICE_UNAVAILABLE", "live detection is not enabled", map[string]any{})
		return
	}
	p, ok := s.writePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if e := s.capture.AuthorizeVision(r.Context(), p, id); e != nil {
		s.writeServiceError(w, r, e)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1500000)
	var in liveFrame
	if !s.decode(w, r, &in, false) {
		return
	}
	data, e := base64.StdEncoding.DecodeString(in.Image)
	if e != nil || len(in.Image) > 1000000 || len(data) > 750000 || in.Sequence < 0 || in.Sequence > 9007199254740991 {
		writeError(w, r, 400, "INVALID_ARGUMENT", "bounded JPEG and nonnegative sequence required", map[string]any{})
		return
	}
	meta, e := jpeg.DecodeConfig(bytes.NewReader(data))
	if e != nil || meta.Width < 16 || meta.Height < 16 || meta.Width > 1920 || meta.Height > 1080 {
		writeError(w, r, 400, "INVALID_ARGUMENT", "JPEG dimensions exceed live preview limits", map[string]any{})
		return
	}
	if _, e = jpeg.Decode(bytes.NewReader(data)); e != nil {
		writeError(w, r, 400, "INVALID_ARGUMENT", "JPEG data is incomplete", map[string]any{})
		return
	}
	task := s.liveVision.offer(id, in, meta.Width, meta.Height, p.UserID)
	if task == nil {
		w.Header().Set("Retry-After", "1")
		writeError(w, r, 429, "RATE_LIMITED", "live worker is busy; drop this frame", map[string]any{})
		return
	}
	defer s.liveVision.drop(task)
	timer := time.NewTimer(time.Until(task.expires))
	defer timer.Stop()
	select {
	case result := <-task.done:
		if e := s.capture.AuthorizeVision(r.Context(), p, id); e != nil {
			s.writeServiceError(w, r, e)
			return
		}
		writeData(w, r, 200, result)
	case <-r.Context().Done():
		return
	case <-timer.C:
		writeError(w, r, 503, "SERVICE_UNAVAILABLE", "live worker unavailable or processing offline work", map[string]any{})
	}
}
func (s *server) claimLiveVision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.requireWorker(w, r) {
		return
	}
	if s.liveVision == nil || !s.workerCapabilities["video_analysis"] {
		w.WriteHeader(204)
		return
	}
	var empty struct{}
	if !s.decode(w, r, &empty, false) {
		return
	}
	b := s.liveVision
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	for {
		b.Lock()
		if b.wake == nil {
			b.wake = make(chan struct{}, 1)
		}
		task, wake := b.work, b.wake
		if task != nil && time.Now().Before(task.expires) {
			if task.claimed {
				b.Unlock()
				w.WriteHeader(204)
				return
			}
			task.claimed = true
			// Copy serialized fields; the broker retains synchronization-only state.
			out := *task
			b.Unlock()
			if task.userID != "" {
				if s.capture == nil {
					b.drop(task)
					w.WriteHeader(204)
					return
				}
				if e := s.capture.AuthorizeVision(r.Context(), identity.Principal{UserID: task.userID}, task.CaptureID); e != nil {
					code := "PROCESSING_FAILED"
					select {
					case task.done <- liveResult{Sequence: task.Sequence, Width: task.Width, Height: task.Height, People: []livePerson{}, Error: &code}:
					default:
					}
					b.drop(task)
					w.WriteHeader(204)
					return
				}
			}
			b.Lock()
			current := b.work == task && time.Now().Before(task.expires)
			b.Unlock()
			if !current {
				w.WriteHeader(204)
				return
			}
			writeData(w, r, 200, &out)
			return
		}
		b.Unlock()
		select {
		case <-wake:
			continue
		case <-deadline.C:
			w.WriteHeader(204)
			return
		case <-r.Context().Done():
			return
		}
	}
}
func validLiveResult(v liveResult, width, height int) bool {
	if v.Width != width || v.Height != height || v.People == nil || len(v.People) > 80 {
		return false
	}
	if v.Error != nil && *v.Error != "PROCESSING_FAILED" && *v.Error != "PROCESSING_TIMEOUT" {
		return false
	}
	seen := map[string]bool{}
	for _, p := range v.People {
		if len(p.ID) == 0 || len(p.ID) > 32 || seen[p.ID] {
			return false
		}
		seen[p.ID] = true
		for _, n := range p.Box {
			if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1 {
				return false
			}
		}
		if p.Box[0] >= p.Box[2] || p.Box[1] >= p.Box[3] {
			return false
		}
	}
	return true
}
func (s *server) completeLiveVision(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorker(w, r) {
		return
	}
	if s.liveVision == nil {
		writeError(w, r, 503, "SERVICE_UNAVAILABLE", "live detection disabled", map[string]any{})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	var result liveResult
	if !s.decode(w, r, &result, false) {
		return
	}
	b := s.liveVision
	b.Lock()
	defer b.Unlock()
	task := b.work
	if task == nil || !task.claimed || task.completed || task.ID != r.PathValue("id") || task.Token != r.Header.Get("X-Lease-Token") || !time.Now().Before(task.expires) {
		writeError(w, r, 409, "COMPLETION_CONFLICT", "frame expired", map[string]any{})
		return
	}
	if result.Sequence != task.Sequence || !validLiveResult(result, task.Width, task.Height) {
		writeError(w, r, 400, "INVALID_ARGUMENT", "invalid live detection result", map[string]any{})
		return
	}
	select {
	case task.done <- result:
		task.completed = true
		w.WriteHeader(204)
	default:
		writeError(w, r, 409, "COMPLETION_CONFLICT", "frame already completed", map[string]any{})
	}
}
