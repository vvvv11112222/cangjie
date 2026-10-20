package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type jobManager struct {
	done        chan struct{}
	stopped     sync.Once
	wg          sync.WaitGroup
	store       *jobStore
	queue       chan string
	mu          sync.Mutex
	active      map[string]context.CancelFunc
	analyzeText func(context.Context, string, string, []Segment, progressFunc) (AnalysisOutput, error)
}

func newJobManager(store *jobStore) *jobManager {
	m := &jobManager{done: make(chan struct{}), store: store, queue: make(chan string, 32), active: map[string]context.CancelFunc{}, analyzeText: analyzeSegments}
	m.wg.Add(1)
	go func() { defer m.wg.Done(); m.loop() }()
	for _, j := range store.list() {
		if j.Status == jobQueued {
			m.enqueue(j.ID)
		}
	}
	return m
}
func (m *jobManager) enqueue(id string) {
	select {
	case m.queue <- id:
	default:
		_ = m.store.update(id, func(j *Job) error {
			j.Status = jobFailed
			j.Stage = "failed"
			j.Error = "任务队列已满"
			j.Message = "未能排队"
			j.Retryable = true
			return nil
		})
	}
}
func (m *jobManager) loop() {
	for {
		select {
		case <-m.done:
			return
		case id := <-m.queue:
			select {
			case <-m.done:
				return
			default:
			}
			m.run(id)
		}
	}
}
func (m *jobManager) stop() {
	m.stopped.Do(func() {
		close(m.done)
		m.mu.Lock()
		for _, cancel := range m.active {
			cancel()
		}
		m.mu.Unlock()
		m.wg.Wait()
	})
}
func (m *jobManager) run(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	m.mu.Lock()
	m.active[id] = cancel
	select {
	case <-m.done:
		cancel()
	default:
	}
	m.mu.Unlock()
	defer func() { cancel(); m.mu.Lock(); delete(m.active, id); m.mu.Unlock() }()
	claimErr := m.store.update(id, func(j *Job) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if j.Status != jobQueued || j.CancelRequested {
			return errors.New("任务不在队列")
		}
		j.Status = jobRunning
		j.Error = ""
		j.FinishedAt = nil
		j.FailureStage = ""
		j.ReportStatus = reportPending
		j.Report = Report{}
		j.Events = EventSet{}
		j.Reading = ReadingResult{}
		j.EventRequest, j.ReportRequest = ModelMeta{}, ModelMeta{}
		j.Coverage = nil
		j.Stage = "starting"
		j.Message = "任务已开始"
		j.Retryable = false
		return nil
	})
	if claimErr != nil {
		return
	}
	job, ok := m.store.internalGet(id)
	if !ok {
		return
	}
	progress := func(stage, msg string, p int) {
		if stage == "asr_complete" {
			current, found := m.store.internalGet(id)
			if found && len(current.Segments) > 0 {
				if e := m.store.initialRevision(id, current.Segments); e != nil {
					cancel()
					return
				}
			}
		}
		if e := m.store.update(id, func(j *Job) error {
			j.Stage, j.Message, j.Progress = stage, msg, p
			if stage == "asr_complete" {
				j.ASRStatus = asrComplete
			}
			return nil
		}); e != nil {
			log.Printf("任务 %s 进度保存失败", id)
			cancel()
		}
	}
	var output AnalysisOutput
	var err error
	if job.Mode == "report_only" {
		revs := m.store.revisionsFor(id)
		var selected revisionFile
		for _, r := range revs {
			if r.RevisionNo == job.InputRevision {
				selected = r
				break
			}
		}
		if selected.ID == "" {
			err = fmt.Errorf("修订版本不存在")
		} else {
			ctx = withReportFeedback(ctx, job, selected)
			output, err = m.analyzeText(ctx, env("BAILIAN_BASE_URL", ""), os.Getenv("DASHSCOPE_API_KEY"), selected.Segments, progress)
		}
	} else {
		if untrustedLegacyCheckpoint(job) {
			err = errors.New("旧转写检查点无法确认完整性，请使用原视频创建独立转写任务")
		} else {
			output, _, err = analyzeVideo(ctx, env("BAILIAN_BASE_URL", ""), os.Getenv("DASHSCOPE_API_KEY"), job.VideoPath, job.Segments, job.CompletedASRChunks, job.AudioChunks, progress, func(segments []Segment, chunks []AudioChunk, done []int) error {
				return m.store.update(id, func(j *Job) error {
					j.Segments = segments
					j.AudioChunks = chunks
					j.CompletedASRChunks = done
					return nil
				})
			})
		}
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		msg := err.Error()
		_ = m.store.update(id, func(j *Job) error {
			j.FailureStage = j.Stage
			j.EventRequest, j.ReportRequest = output.EventRequest, output.ReportRequest
			j.Coverage = output.Coverage
			if j.Stage == "reading" || j.Stage == "events" || j.Stage == "report" || j.Stage == "audit" {
				j.ReportStatus = reportFailed
			}
			if output.Reading.Status != "" {
				j.Reading = output.Reading
			}
			if output.Events.Events != nil {
				j.Events = output.Events
			}
			j.Status = jobFailed
			j.Stage = "failed"
			j.Error = msg
			j.Message = "任务未完成，请查看错误详情；已完成的转写会保留"
			j.Retryable = true
			now := time.Now().UTC()
			j.FinishedAt = &now
			if errors.Is(err, context.Canceled) {
				j.Status = jobCancelled
				j.Stage = "cancelled"
				j.Message = "任务已取消"
				j.Retryable = false
			}
			select {
			case <-m.done:
				j.Status = jobFailed
				j.Stage = "failed"
				j.Message = "服务停止，任务中断，请确认后重试"
				j.Error = "模型请求可能已计费，未自动重试"
				j.Retryable = true
			default:
			}
			return nil
		})
		return
	}
	if job.Mode == "full" {
		if e := m.store.initialRevision(id, output.Segments); e != nil {
			_ = m.store.update(id, func(j *Job) error {
				j.Status = jobFailed
				j.Stage = "failed"
				j.Error = "原始转写版本保存失败"
				j.Message = "结果持久化失败"
				j.Retryable = true
				return nil
			})
			return
		}
	}
	// Treat missing/invalid outcomes as failures, even if a pipeline forgot to
	// return an error. The worker never publishes an unvalidated report.
	if output.ReportStatus == reportReady {
		e := validateEvents(output.Events, output.Segments)
		if e == nil {
			e = validateEventReport(output.Report, output.Events.Events, output.Segments)
		}
		if e != nil {
			output.ReportDiagnostic = failedReportDiagnostic("", &output.Report, "publish_validation", e, output.Segments, output.Events, output.ReportRequest)
			output.ReportStatus, output.FailureStage, output.FailureMessage = reportFailed, "report", "六维报告证据链校验失败："+e.Error()
			output.Warnings = append(output.Warnings, output.FailureMessage)
			output.Report = insufficientReport("发布前校验未通过，已保留可用转写和诊断")
		}
	} else if output.ReportStatus != reportFailed {
		output.ReportStatus, output.FailureStage, output.FailureMessage = reportFailed, "report", "六维报告未返回明确的有效结果"
		output.Warnings = append(output.Warnings, output.FailureMessage)
	}
	if job.Mode == "report_only" {
		output.Warnings = append(transcriptWarnings(job.Warnings), output.Warnings...)
	}
	finishErr := m.store.update(id, func(j *Job) error {
		if j.CancelRequested {
			return context.Canceled
		}
		if j.Mode == "full" {
			j.Segments = output.Segments
			j.RevisionNo = 1
			j.TranscriptRevision = revisionID(id, 1)
		}
		j.Reading = output.Reading
		j.Events = output.Events
		j.Report = output.Report
		j.Coverage = output.Coverage
		j.ReportStatus = output.ReportStatus
		j.EventRequest, j.ReportRequest = output.EventRequest, output.ReportRequest
		j.FailureStage = output.FailureStage
		if output.ReportDiagnostic != nil {
			diagnostic := cloneValue(*output.ReportDiagnostic)
			diagnostic.InputRevision = j.InputRevision
			if diagnostic.InputRevision == 0 {
				diagnostic.InputRevision = j.RevisionNo
			}
			j.ReportDiagnostics = append(j.ReportDiagnostics, diagnostic)
		}
		j.Warnings = output.Warnings
		j.Progress = 100
		j.Stage = "completed"
		j.Status = jobSucceeded
		j.Message = "处理完成"
		j.Error = ""
		j.Retryable = false
		if output.ReportStatus != reportReady || j.ASRStatus != asrComplete {
			j.Status, j.Stage = jobPartial, "partial"
			j.Message = "部分完成：可用结果已保存，可重新分析"
			j.Error = output.FailureMessage
			j.Retryable = true
		}
		now := time.Now().UTC()
		j.FinishedAt = &now
		if j.InputRevision == 0 {
			j.InputRevision = 1
		}
		if output.ReportStatus == reportReady {
			j.ReportVersions = append(j.ReportVersions, ReportVersion{Coverage: output.Coverage, Version: len(j.ReportVersions) + 1, InputRevision: j.InputRevision, ASRStatus: j.ASRStatus, ReportStatus: reportReady, Report: output.Report, Segments: output.Segments, Reading: output.Reading, Events: output.Events, RubricVersion: rubricVersion, EventRequest: output.EventRequest, ReportRequest: output.ReportRequest, Warnings: output.Warnings, CreatedAt: now})
		}
		return nil
	})
	if finishErr != nil {
		log.Printf("任务 %s 完成结果保存失败", id)
		_ = m.store.update(id, func(j *Job) error {
			j.Status = jobFailed
			j.Stage = "failed"
			j.Error = "结果保存失败，未标记完成"
			j.Retryable = true
			if errors.Is(finishErr, context.Canceled) {
				j.Status = jobCancelled
				j.Stage = "cancelled"
				j.Error = ""
				j.Message = "任务已取消"
				j.Retryable = false
			}
			return nil
		})
	}
}
func (m *jobManager) cancel(id string) error {
	if err := m.store.cancel(id); err != nil {
		return err
	}
	m.mu.Lock()
	if f := m.active[id]; f != nil {
		f()
	}
	m.mu.Unlock()
	return nil
}
func (m *jobManager) retry(id string) error {
	err := m.store.update(id, func(j *Job) error {
		normalizeJobRecovery(j)
		if (j.Status != jobFailed && j.Status != jobPartial) || !j.Retryable {
			return errors.New("只有可重试的失败或部分完成任务可以重试")
		}
		if untrustedLegacyCheckpoint(*j) {
			return errors.New("旧转写检查点无法确认完整性，请使用原视频创建独立转写任务")
		}
		if j.RevisionNo > 0 {
			j.Mode = "report_only"
			j.InputRevision = j.RevisionNo
		} else {
			j.Mode = "full"
		}
		resetAnalysisForQueue(j, "等待重试")
		return nil
	})
	if err == nil {
		m.enqueue(id)
	}
	return err
}

func resetAnalysisForQueue(j *Job, message string) {
	j.Status, j.Stage, j.Message = jobQueued, "queued", message
	j.Error, j.FailureStage = "", ""
	j.Progress = 0
	j.CancelRequested, j.Retryable = false, false
	j.FinishedAt = nil
	j.ReportStatus = reportPending
	j.Report, j.Events, j.Reading = Report{}, EventSet{}, ReadingResult{}
	j.EventRequest, j.ReportRequest = ModelMeta{}, ModelMeta{}
	j.Coverage = nil
	j.Warnings = transcriptWarnings(j.Warnings)
}

// Users can upgrade a successful report without manufacturing a transcript edit.
// Reanalysis pins the latest immutable revision and retains all report versions.
func (m *jobManager) reanalyze(id string) error {
	err := m.store.update(id, func(j *Job) error {
		normalizeJobRecovery(j)
		if j.Status == jobQueued || j.Status == jobRunning || j.RevisionNo < 1 {
			return errors.New("任务运行中或尚无可分析的转写版本")
		}
		j.Mode, j.InputRevision = "report_only", j.RevisionNo
		resetAnalysisForQueue(j, "等待使用现有转写重新分析")
		return nil
	})
	if err == nil {
		m.enqueue(id)
	}
	return err
}

// Legacy ASR gaps cannot safely be inserted into immutable revisions. Copy the
// retained media into an independent task; no provider request runs here.
func (m *jobManager) retranscribe(id, root string) (Job, error) {
	if err := modelConfigError(); err != nil {
		return Job{}, err
	}
	job, ok := m.store.internalGet(id)
	if !ok {
		return Job{}, os.ErrNotExist
	}
	normalizeJobRecovery(&job)
	if job.Status == jobRunning || job.Status == jobQueued {
		return Job{}, errors.New("任务正在运行")
	}
	if job.VideoPath == "" {
		return Job{}, errors.New("原视频不可用")
	}
	f, err := os.Open(job.VideoPath)
	if err != nil {
		return Job{}, errors.New("原视频不可用，请重新上传")
	}
	defer f.Close()
	path, err := saveUpload(f, job.VideoName, root)
	if err != nil {
		return Job{}, err
	}
	created, err := m.store.create(newJobID(), path, job.VideoName)
	if err != nil {
		_ = os.Remove(path)
		return Job{}, err
	}
	m.enqueue(created.ID)
	return created, nil
}

func writeJobJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func decodeJSON(r *http.Request, target any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("只允许一个 JSON 对象")
	}
	return nil
}
func saveUpload(f multipart.File, name, root string) (string, error) {
	return saveUploadLimit(f, name, root, maxUploadBytes)
}

func saveUploadLimit(f multipart.File, name, root string, limit int64) (string, error) {
	if root == "" {
		root = "data"
	}
	media := filepath.Join(root, "media", newJobID())
	media, err := filepath.Abs(media)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(media, 0700); err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	if len(ext) > 10 {
		ext = ".media"
	}
	path := filepath.Join(media, newJobID()+ext)
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	n, copyErr := io.Copy(dst, io.LimitReader(f, limit+1))
	closeErr := dst.Close()
	if n > limit {
		_ = os.Remove(path)
		return "", errors.New("文件超过2GB，请压缩视频后上传")
	}
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if copyErr != nil {
			return "", copyErr
		}
		return "", closeErr
	}
	return path, nil
}
func (m *jobManager) createUpload(r *http.Request, root string) (Job, error) {
	if err := modelConfigError(); err != nil {
		return Job{}, err
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		return Job{}, fmt.Errorf("上传失败或超过2GB")
	}
	defer r.MultipartForm.RemoveAll()
	if r.FormValue("consent") != "yes" {
		return Job{}, fmt.Errorf("请确认云端处理授权和可能费用")
	}
	f, h, err := r.FormFile("video")
	if err != nil {
		return Job{}, fmt.Errorf("请选择视频")
	}
	defer f.Close()
	if h.Size > maxUploadBytes {
		return Job{}, fmt.Errorf("文件超过2GB，请压缩视频后上传")
	}
	path, err := saveUpload(f, h.Filename, root)
	if err != nil {
		return Job{}, err
	}
	id := newJobID()
	job, err := m.store.create(id, path, h.Filename)
	if err != nil {
		_ = os.Remove(path)
		return Job{}, err
	}
	m.enqueue(id)
	return job, nil
}

type createRevisionRequest struct {
	ParentRevisionNo int       `json:"parent_revision_no"`
	Reason           string    `json:"reason"`
	Segments         []Segment `json:"segments"`
}

func validateRevisionSegments(segments []Segment) error {
	if len(segments) == 0 {
		return errors.New("segments不能为空")
	}
	last := int64(-1)
	for i, s := range segments {
		if s.ID == "" || strings.TrimSpace(s.Text) == "" || s.Start < 0 || s.End <= s.Start || s.Start < last {
			return fmt.Errorf("第%d段时间或文本无效", i+1)
		}
		last = s.End
	}
	return nil
}
func (m *jobManager) createRevision(id string, req createRevisionRequest) (revisionFile, Job, error) {
	rev, j, err := m.store.revise(id, req)
	if err == nil {
		m.enqueue(id)
	}
	return rev, j, err
}
func modelConfigError() error {
	base, key := os.Getenv("BAILIAN_BASE_URL"), os.Getenv("DASHSCOPE_API_KEY")
	u, err := url.Parse(base)
	if key == "" || err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || strings.Contains(base, "YOUR_") {
		return errors.New("请配置 DASHSCOPE_API_KEY 与 BAILIAN_BASE_URL")
	}
	return nil
}
func sameOrigin(w http.ResponseWriter, r *http.Request) bool {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	o := r.Header.Get("Origin")
	if (o != "" && o != scheme+"://"+r.Host) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeJobJSON(w, 403, map[string]any{"error": "不允许跨站请求"})
		return false
	}
	return true
}

func (m *jobManager) registerRoutes(mux *http.ServeMux, root string) {
	m.registerMedia(mux)
	m.store.registerReviewRoutes(mux)
	mux.HandleFunc("POST /api/jobs/{id}/reanalyze", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(w, r) {
			return
		}
		if err := modelConfigError(); err != nil {
			writeJobJSON(w, 422, map[string]any{"error": err.Error()})
			return
		}
		if err := m.reanalyze(r.PathValue("id")); err != nil {
			writeJobJSON(w, 409, map[string]any{"error": err.Error()})
			return
		}
		j, _ := m.store.get(r.PathValue("id"))
		writeJobJSON(w, 202, map[string]any{"job": j})
	})
	mux.HandleFunc("POST /api/jobs/{id}/retranscribe", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(w, r) {
			return
		}
		job, err := m.retranscribe(r.PathValue("id"), root)
		if err != nil {
			writeJobJSON(w, 409, map[string]any{"error": err.Error()})
			return
		}
		writeJobJSON(w, 202, map[string]any{"job": job})
	})
	mux.HandleFunc("POST /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(w, r) {
			return
		}
		if r.ContentLength > maxUploadRequestBytes {
			writeJobJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "文件超过2GB，请压缩视频后上传"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
		job, err := m.createUpload(r, root)
		if err != nil {
			writeJobJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
			return
		}
		writeJobJSON(w, http.StatusAccepted, map[string]any{"job": job})
	})
	mux.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		writeJobJSON(w, http.StatusOK, map[string]any{"jobs": m.store.list()})
	})
	mux.HandleFunc("GET /api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		j, ok := m.store.get(r.PathValue("id"))
		if !ok {
			writeJobJSON(w, 404, map[string]any{"error": "任务不存在"})
			return
		}
		writeJobJSON(w, 200, map[string]any{"job": j})
	})
	mux.HandleFunc("POST /api/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(w, r) {
			return
		}
		if err := m.cancel(r.PathValue("id")); err != nil {
			writeJobJSON(w, 409, map[string]any{"error": err.Error()})
			return
		}
		j, _ := m.store.get(r.PathValue("id"))
		writeJobJSON(w, 200, map[string]any{"job": j})
	})
	mux.HandleFunc("POST /api/jobs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(w, r) {
			return
		}
		if err := m.retry(r.PathValue("id")); err != nil {
			writeJobJSON(w, 409, map[string]any{"error": err.Error()})
			return
		}
		j, _ := m.store.get(r.PathValue("id"))
		writeJobJSON(w, 202, map[string]any{"job": j})
	})
	mux.HandleFunc("GET /api/jobs/{id}/revisions", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := m.store.get(r.PathValue("id")); !ok {
			writeJobJSON(w, 404, map[string]any{"error": "任务不存在"})
			return
		}
		writeJobJSON(w, 200, map[string]any{"revisions": m.store.revisionsFor(r.PathValue("id"))})
	})
	mux.HandleFunc("POST /api/jobs/{id}/revisions", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(w, r) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		var req createRevisionRequest
		if err := decodeJSON(r, &req); err != nil {
			writeJobJSON(w, 400, map[string]any{"error": "修订 JSON 无效"})
			return
		}
		rev, j, err := m.createRevision(r.PathValue("id"), req)
		if err != nil {
			writeJobJSON(w, 409, map[string]any{"error": err.Error()})
			return
		}
		writeJobJSON(w, 202, map[string]any{"revision": rev, "job": j})
	})
}

// Media is retained so evidence playback also works after a page refresh.
func (m *jobManager) registerMedia(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/jobs/{id}/media", func(w http.ResponseWriter, r *http.Request) {
		j, ok := m.store.internalGet(r.PathValue("id"))
		if !ok || j.VideoPath == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		http.ServeFile(w, r, j.VideoPath)
	})
}
