package httpapi

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type featureStatus struct {
	Available bool     `json:"available"`
	Reasons   []string `json:"reasons"`
}
type serviceCapabilities struct {
	CheckedAt        time.Time     `json:"checked_at"`
	MediaPrepare     featureStatus `json:"media_prepare"`
	VisualAnalysis   featureStatus `json:"visual_analysis"`
	AudioReport      featureStatus `json:"audio_report"`
	ReportGeneration featureStatus `json:"report_generation"`
	LiveCapture      featureStatus `json:"live_capture"`
}

// Announcements are ephemeral and fail closed after a restart or 90 seconds
// without authenticated claims/lease heartbeats. Individual capabilities expire
// independently; the server's permission list is never treated as live support.
type workerAnnouncements struct {
	mu     sync.Mutex
	groups map[string]workerGroup
	jobs   map[string]string
}
type workerGroup struct {
	seen         time.Time
	capabilities []string
	visual       bool
}

func (a *workerAnnouncements) announce(capabilities []string, visual bool, now time.Time, jobID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.groups == nil {
		a.groups = map[string]workerGroup{}
		a.jobs = map[string]string{}
	}
	caps := append([]string{}, capabilities...)
	sort.Strings(caps)
	key := strings.Join(caps, ",")
	// One sequential process per declaration. Its next poll retires its old job.
	for id, group := range a.jobs {
		if group == key || now.Sub(a.groups[group].seen) > 90*time.Second {
			delete(a.jobs, id)
		}
	}
	for group, value := range a.groups {
		if now.Sub(value.seen) > 90*time.Second {
			delete(a.groups, group)
		}
	}
	a.groups[key] = workerGroup{seen: now, capabilities: caps, visual: visual}
	if jobID != "" {
		a.jobs[jobID] = key
	}
}
func (a *workerAnnouncements) heartbeat(jobID string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key, ok := a.jobs[jobID]
	if !ok {
		return
	}
	group, ok := a.groups[key]
	if !ok {
		return
	}
	group.seen = now
	a.groups[key] = group
}
func (a *workerAnnouncements) snapshot(now time.Time) (map[string]bool, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := map[string]bool{}
	visual := false
	for _, group := range a.groups {
		if now.Sub(group.seen) > 90*time.Second {
			continue
		}
		for _, capability := range group.capabilities {
			seen[capability] = true
			if capability == "video_analysis" && group.visual {
				visual = true
			}
		}
	}
	return seen, visual
}
func status(reasons ...string) featureStatus {
	if reasons == nil {
		reasons = []string{}
	}
	return featureStatus{Available: len(reasons) == 0, Reasons: reasons}
}
func (s *server) getServiceCapabilities(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.readPrincipal(w, r); !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, r, 400, "INVALID_ARGUMENT", "this endpoint takes no query parameters", map[string]any{})
		return
	}
	now := time.Now().UTC()
	workers, visualWorker := s.announcements.snapshot(now)
	visual, audio, report, budget, reportBudget := false, false, false, false, false
	if s.analysis != nil {
		var err error
		visual, audio, report, budget, reportBudget, err = s.analysis.Availability(r.Context())
		if err != nil {
			s.writeServiceError(w, r, err)
			return
		}
	}
	value := serviceCapabilities{CheckedAt: now, MediaPrepare: status(), VisualAnalysis: status(), AudioReport: status(), ReportGeneration: status(), LiveCapture: status()}
	if !workers["probe"] {
		value.MediaPrepare.Reasons = append(value.MediaPrepare.Reasons, "MEDIA_WORKER_UNAVAILABLE")
	}
	value.VisualAnalysis.Reasons = append(value.VisualAnalysis.Reasons, value.MediaPrepare.Reasons...)
	value.AudioReport.Reasons = append(value.AudioReport.Reasons, value.MediaPrepare.Reasons...)
	if !visual {
		value.VisualAnalysis.Reasons = append(value.VisualAnalysis.Reasons, "VISION_NOT_CONFIGURED")
	}
	if !workers["video_analysis"] || !visualWorker {
		value.VisualAnalysis.Reasons = append(value.VisualAnalysis.Reasons, "VISUAL_WORKER_UNAVAILABLE")
	}
	if !audio {
		value.AudioReport.Reasons = append(value.AudioReport.Reasons, "ASR_NOT_CONFIGURED")
	}
	if !report {
		value.AudioReport.Reasons = append(value.AudioReport.Reasons, "REPORT_NOT_CONFIGURED")
	}
	if !workers["audio_analysis"] || !workers["video_analysis"] {
		value.AudioReport.Reasons = append(value.AudioReport.Reasons, "AUDIO_WORKER_UNAVAILABLE")
	}
	if audio && report && !budget {
		value.AudioReport.Reasons = append(value.AudioReport.Reasons, "BUDGET_UNAVAILABLE")
	}
	if !report {
		value.ReportGeneration.Reasons = append(value.ReportGeneration.Reasons, "REPORT_NOT_CONFIGURED")
	} else if !reportBudget {
		value.ReportGeneration.Reasons = append(value.ReportGeneration.Reasons, "BUDGET_UNAVAILABLE")
	}
	if s.capture == nil {
		value.LiveCapture.Reasons = append(value.LiveCapture.Reasons, "CAPTURE_NOT_CONFIGURED")
	}
	for _, feature := range []*featureStatus{&value.MediaPrepare, &value.VisualAnalysis, &value.AudioReport, &value.ReportGeneration, &value.LiveCapture} {
		feature.Available = len(feature.Reasons) == 0
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeData(w, r, http.StatusOK, value)
}
