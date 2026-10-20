package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	jobQueued    = "queued"
	jobRunning   = "running"
	jobSucceeded = "succeeded"
	jobPartial   = "partial"
	jobFailed    = "failed"
	jobCancelled = "cancelled"
)

type Job struct {
	ASRStatus          string              `json:"asr_status"`
	ReportStatus       string              `json:"report_status"`
	FailureStage       string              `json:"failure_stage,omitempty"`
	EventRequest       ModelMeta           `json:"event_request"`
	ReportRequest      ModelMeta           `json:"text_request"`
	ID                 string              `json:"id"`
	Status             string              `json:"status"`
	Stage              string              `json:"stage"`
	Progress           int                 `json:"progress"`
	Message            string              `json:"message"`
	Error              string              `json:"error,omitempty"`
	VideoPath          string              `json:"video_path,omitempty"`
	VideoName          string              `json:"video_name"`
	Segments           []Segment           `json:"segments,omitempty"`
	AudioChunks        []AudioChunk        `json:"audio_chunks,omitempty"`
	CompletedASRChunks []int               `json:"completed_asr_chunks,omitempty"`
	Reading            ReadingResult       `json:"reading,omitempty"`
	Events             EventSet            `json:"events,omitempty"`
	Report             Report              `json:"report,omitempty"`
	Warnings           []string            `json:"warnings,omitempty"`
	RevisionNo         int                 `json:"revision_no"`
	TranscriptRevision string              `json:"transcript_revision_id,omitempty"`
	InputRevision      int                 `json:"input_revision_no,omitempty"`
	Mode               string              `json:"mode"`
	ReportVersions     []ReportVersion     `json:"report_versions,omitempty"`
	ReportDiagnostics  []ReportDiagnostic  `json:"report_diagnostics,omitempty"`
	ReportReviews      []ObservationReview `json:"report_reviews,omitempty"`
	ReviewTargets      []ReviewTarget      `json:"review_targets,omitempty"`
	ReportVersion      int                 `json:"report_version,omitempty"` // Derived for the current published result only.
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
	FinishedAt         *time.Time          `json:"finished_at,omitempty"`
	CancelRequested    bool                `json:"cancel_requested,omitempty"`
	Retryable          bool                `json:"retryable,omitempty"`
	WorkerLease        string              `json:"-"`
}

type ReportVersion struct {
	ASRStatus     string        `json:"asr_status"`
	ReportStatus  string        `json:"report_status"`
	Version       int           `json:"version"`
	InputRevision int           `json:"input_revision_no"`
	Report        Report        `json:"report"`
	Segments      []Segment     `json:"segments"`
	Reading       ReadingResult `json:"reading"`
	Events        EventSet      `json:"events"`
	RubricVersion string        `json:"rubric_version"`
	EventRequest  ModelMeta     `json:"event_request"`
	ReportRequest ModelMeta     `json:"text_request"`
	Warnings      []string      `json:"warnings,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
}

type revisionFile struct {
	ID             string    `json:"id"`
	RevisionNo     int       `json:"revision_no"`
	ParentRevision int       `json:"parent_revision_no,omitempty"`
	Reason         string    `json:"reason"`
	CreatedAt      time.Time `json:"created_at"`
	Segments       []Segment `json:"segments"`
}
type jobStore struct {
	mu        sync.Mutex
	root      string
	jobs      map[string]*Job
	revisions map[string][]revisionFile
}

func newJobStore(root string) (*jobStore, error) {
	if root == "" {
		root = "data"
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	s := &jobStore{root: root, jobs: map[string]*Job{}, revisions: map[string][]revisionFile{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *jobStore) jobsPath() string      { return filepath.Join(s.root, "jobs.json") }
func (s *jobStore) revisionsPath() string { return filepath.Join(s.root, "transcript-revisions.json") }

// A single snapshot commits jobs and immutable revisions together.
type storeState struct {
	Jobs      map[string]*Job           `json:"jobs"`
	Revisions map[string][]revisionFile `json:"revisions"`
}

func cloneValue[T any](v T) T {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}
func (s *jobStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(s.root, "state.json"))
	if err == nil {
		var state storeState
		if err = json.Unmarshal(b, &state); err != nil {
			return err
		}
		if state.Jobs == nil || state.Revisions == nil {
			return errors.New("任务状态文件不完整")
		}
		s.jobs = state.Jobs
		s.revisions = state.Revisions
	} else if os.IsNotExist(err) { // Import the earlier two-file demo format once.
		for path, target := range map[string]any{s.jobsPath(): &s.jobs, s.revisionsPath(): &s.revisions} {
			b, e := os.ReadFile(path)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return e
			}
			if e = json.Unmarshal(b, target); e != nil {
				return fmt.Errorf("read %s: %w", path, e)
			}
		}
	} else {
		return err
	}
	if s.jobs == nil || s.revisions == nil {
		return errors.New("任务状态文件不完整")
	}
	for _, j := range s.jobs {
		if j == nil {
			return errors.New("任务记录损坏")
		}
		if j.Status == jobRunning {
			j.Status = jobFailed
			j.Stage = "failed"
			j.Message = "服务重启中断了任务，请确认后重试"
			j.Error = "模型请求状态可能未知，未自动重试"
			j.Retryable = true
		}
		if j.VideoPath != "" {
			j.VideoPath, err = filepath.Abs(j.VideoPath)
			if err != nil {
				return err
			}
		}
	}
	// Older demo report versions did not carry source snapshots. Recover text
	// from the matching immutable revision without borrowing newer evidence.
	for id, j := range s.jobs {
		for i := range j.ReportVersions {
			v := &j.ReportVersions[i]
			if v.Segments == nil {
				for _, r := range s.revisions[id] {
					if r.RevisionNo == v.InputRevision {
						v.Segments = cloneValue(r.Segments)
						break
					}
				}
			}
			if v.RubricVersion == "" {
				v.RubricVersion = rubricVersion
			}
		}
		normalizeJobRecovery(j)
	}
	return s.persistLocked()
}
func (s *jobStore) persistLocked() error {
	b, e := json.MarshalIndent(storeState{s.jobs, s.revisions}, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(s.root, "state-*.tmp")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(name, filepath.Join(s.root, "state.json"))
}

// Mutate copies, then roll back on a failed disk write. Call with the mutex held.
func (s *jobStore) transaction(fn func() error) error {
	oldJobs, oldRevs := s.jobs, s.revisions
	s.jobs = cloneValue(oldJobs)
	s.revisions = cloneValue(oldRevs)
	if err := fn(); err != nil {
		s.jobs = oldJobs
		s.revisions = oldRevs
		return err
	}
	if err := s.persistLocked(); err != nil {
		s.jobs = oldJobs
		s.revisions = oldRevs
		return err
	}
	return nil
}
func (s *jobStore) snapshot(j *Job) Job {
	c := cloneValue(*j)
	normalizeJobRecovery(&c)
	c.VideoPath = ""
	c.WorkerLease = ""
	c.ReportDiagnostics = nil // Drafts are local diagnostics, not published reports.
	c.ReviewTargets = nil
	c.ReportVersion = 0
	inputRevision := c.InputRevision
	if inputRevision == 0 {
		inputRevision = c.RevisionNo
	}
	currentHash := reviewHash(ReportVersion{InputRevision: inputRevision, Report: c.Report, Segments: c.Segments, Events: c.Events})
	for _, v := range c.ReportVersions {
		if _, err := reviewableReport(&c, v.Version); err == nil {
			hash := reviewHash(v)
			c.ReviewTargets = append(c.ReviewTargets, ReviewTarget{v.Version, v.InputRevision, hash})
			if c.ReportStatus == reportReady && hash == currentHash && v.Version > c.ReportVersion {
				c.ReportVersion = v.Version
			}
		}
	}
	if len(c.Segments) > 0 && len(c.Reading.Paragraphs) == 0 {
		c.Reading = buildReading(c.Segments, conservativeBoundaries(c.Segments), "conservative", "reading_not_saved", ModelMeta{})
		c.Reading.Message = "已用保守规则连接明显的跨段残句；这是本地阅读整理，不代表六维分析已经完成。原始分段和时间保持不变。"
	}
	return c
}
func (s *jobStore) create(id, videoPath, name string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	err := s.transaction(func() error {
		if s.jobs[id] != nil {
			return errors.New("任务 ID 重复")
		}
		s.jobs[id] = &Job{ID: id, Status: jobQueued, Stage: "queued", Mode: "full", ASRStatus: asrIncomplete, ReportStatus: reportPending, Message: "等待处理", VideoPath: videoPath, VideoName: name, CreatedAt: now, UpdatedAt: now}
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return s.snapshot(s.jobs[id]), nil
}
func (s *jobStore) get(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	return s.snapshot(j), true
}
func (s *jobStore) internalGet(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	return cloneValue(*j), true
}
func (s *jobStore) update(id string, fn func(*Job) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transaction(func() error {
		j, ok := s.jobs[id]
		if !ok {
			return os.ErrNotExist
		}
		if e := fn(j); e != nil {
			return e
		}
		j.UpdatedAt = time.Now().UTC()
		return nil
	})
}
func (s *jobStore) list() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, s.snapshot(j))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
func (s *jobStore) cancel(id string) error {
	return s.update(id, func(j *Job) error {
		if j.Status != jobQueued && j.Status != jobRunning {
			return errors.New("任务已结束")
		}
		j.CancelRequested = true
		j.Message = "等待取消"
		if j.Status == jobQueued {
			j.Status = jobCancelled
			j.Stage = "cancelled"
			j.Message = "任务已取消"
		}
		return nil
	})
}
func (s *jobStore) initialRevision(id string, segments []Segment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transaction(func() error {
		j := s.jobs[id]
		if j == nil {
			return os.ErrNotExist
		}
		if len(s.revisions[id]) > 0 {
			return nil
		}
		r := revisionFile{ID: revisionID(id, 1), RevisionNo: 1, Reason: "ASR 原始转写", CreatedAt: time.Now().UTC(), Segments: cloneValue(segments)}
		s.revisions[id] = []revisionFile{r}
		j.RevisionNo = 1
		j.InputRevision = 1
		j.TranscriptRevision = r.ID
		j.Segments = cloneValue(segments)
		j.ASRStatus = asrComplete
		return nil
	})
}
func (s *jobStore) revise(id string, req createRevisionRequest) (revisionFile, Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rev revisionFile
	err := s.transaction(func() error {
		j := s.jobs[id]
		if j == nil {
			return os.ErrNotExist
		}
		if j.Status == jobRunning || j.Status == jobQueued {
			return errors.New("任务正在运行")
		}
		items := s.revisions[id]
		if len(items) == 0 {
			return errors.New("原始转写尚未完成")
		}
		parent := items[len(items)-1]
		if req.ParentRevisionNo != parent.RevisionNo {
			return errors.New("转写版本已更新，请重新打开任务后再修改")
		}
		if !validReason(req.Reason) {
			return errors.New("修订原因需要2至500个字符")
		}
		if err := validateRevisionSegments(req.Segments); err != nil {
			return err
		}
		if len(req.Segments) != len(parent.Segments) {
			return errors.New("只能修改文本，不能增删片段")
		}
		segments := cloneValue(parent.Segments)
		changed := false
		for i, orig := range parent.Segments {
			v := req.Segments[i]
			if v.ID != orig.ID || v.Start != orig.Start || v.End != orig.End || v.Boundary != orig.Boundary || v.ChunkIndex != orig.ChunkIndex {
				return fmt.Errorf("第%d段只能修改文字，不能修改来源和时间", i+1)
			}
			segments[i].Text = strings.TrimSpace(v.Text)
			changed = changed || segments[i].Text != orig.Text
		}
		if !changed {
			return errors.New("转写没有变化，无需重新分析")
		}
		n := parent.RevisionNo + 1
		rev = revisionFile{ID: revisionID(id, n), RevisionNo: n, ParentRevision: parent.RevisionNo, Reason: strings.TrimSpace(req.Reason), CreatedAt: time.Now().UTC(), Segments: segments}
		s.revisions[id] = append(items, rev)
		j.Mode = "report_only"
		j.RevisionNo = n
		j.InputRevision = n
		j.TranscriptRevision = rev.ID
		j.Segments = cloneValue(segments)
		j.Reading = ReadingResult{}
		j.Events = EventSet{}
		j.Report = Report{}
		j.ReportStatus = reportPending
		j.FailureStage = ""
		j.EventRequest, j.ReportRequest = ModelMeta{}, ModelMeta{}
		j.Warnings = transcriptWarnings(j.Warnings)
		j.Status = jobQueued
		j.Stage = "queued"
		j.Progress = 0
		j.Message = "修订已保存，等待重新分析"
		j.Error = ""
		j.Retryable = false
		j.CancelRequested = false
		j.FinishedAt = nil
		j.UpdatedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		return revisionFile{}, Job{}, err
	}
	return cloneValue(rev), s.snapshot(s.jobs[id]), nil
}
func (s *jobStore) revisionsFor(id string) []revisionFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneValue(s.revisions[id])
}
func revisionHash(segments []Segment) string {
	b, _ := json.Marshal(segments)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func revisionID(jobID string, n int) string { return fmt.Sprintf("%s-r%03d", jobID, n) }
func validReason(s string) bool {
	return len([]rune(strings.TrimSpace(s))) >= 2 && len([]rune(s)) <= 500
}
func newJobID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("job-%d", time.Now().UnixNano())
	}
	return "job-" + hex.EncodeToString(b)
}
