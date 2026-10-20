package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func storedFixture(t *testing.T) (*jobStore, []Segment) {
	t.Helper()
	s, e := newJobStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.create("sample", filepath.Join(s.root, "lesson.mp4"), "课堂.mp4"); e != nil {
		t.Fatal(e)
	}
	segments := []Segment{{ID: "e-0001", Start: 0, End: 12000, Text: "了和了不分。", Boundary: "end_of_audio"}}
	if e = s.initialRevision("sample", segments); e != nil {
		t.Fatal(e)
	}
	if e = s.update("sample", func(j *Job) error { j.Status = jobSucceeded; return nil }); e != nil {
		t.Fatal(e)
	}
	return s, segments
}
func testManager(s *jobStore) *jobManager {
	return &jobManager{store: s, queue: make(chan string, 32), active: map[string]context.CancelFunc{}, analyzeText: analyzeSegments}
}

func TestStoreCopiesAndRestartRecovery(t *testing.T) {
	s, segments := storedFixture(t)
	j, _ := s.get("sample")
	if j.VideoPath != "" {
		t.Fatal("public path leaked")
	}
	j.Segments[0].Text = "changed"
	revs := s.revisionsFor("sample")
	revs[0].Segments[0].Text = "changed"
	internal, _ := s.internalGet("sample")
	if internal.VideoPath == "" || internal.Segments[0].Text != segments[0].Text {
		t.Fatal("snapshot aliases persisted state")
	}
	if e := s.update("sample", func(j *Job) error { j.Status = jobRunning; return nil }); e != nil {
		t.Fatal(e)
	}
	reloaded, e := newJobStore(s.root)
	if e != nil {
		t.Fatal(e)
	}
	j, _ = reloaded.get("sample")
	if j.Status != jobFailed || !j.Retryable {
		t.Fatal("interrupted task automatically rerun")
	}
	if reloaded.revisionsFor("sample")[0].Segments[0].Text != segments[0].Text {
		t.Fatal("immutable original changed")
	}
}
func TestRevisionConflictAndRollback(t *testing.T) {
	s, segments := storedFixture(t)
	edited := cloneValue(segments)
	edited[0].Text = "n 和 l 不分。"
	req := createRevisionRequest{ParentRevisionNo: 1, Reason: "校正拼音", Segments: edited}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, err := s.revise("sample", req); errs <- err }()
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 || len(s.revisionsFor("sample")) != 2 {
		t.Fatal("concurrent revisions both committed")
	}
	j, _ := s.get("sample")
	if j.RevisionNo != 2 || j.Segments[0].Text != edited[0].Text || j.Mode != "report_only" {
		t.Fatal("revision and job not committed together")
	}
	original := s.revisionsFor("sample")[0]
	if original.Segments[0].Text != segments[0].Text {
		t.Fatal("original modified")
	}
	if err := s.update("sample", func(j *Job) error { j.Message = "must rollback"; return fmt.Errorf("rejected") }); err == nil {
		t.Fatal("expected error")
	}
	after, _ := s.get("sample")
	if after.Message == "must rollback" {
		t.Fatal("failed callback mutated state")
	}
	// Simulate disk failure after the mutation; memory must roll back as well.
	oldRoot := s.root
	s.root = filepath.Join(oldRoot, "missing")
	if err := s.update("sample", func(j *Job) error { j.Message = "disk failure"; return nil }); err == nil {
		t.Fatal("expected disk failure")
	}
	s.root = oldRoot
	after, _ = s.get("sample")
	if after.Message == "disk failure" {
		t.Fatal("failed disk write mutated state")
	}
}
func TestRevisionRejectsChangedTimeAndUnchangedText(t *testing.T) {
	s, segments := storedFixture(t)
	for _, change := range []func([]Segment){func(v []Segment) { v[0].Start++ }, func(v []Segment) { v[0].ChunkIndex++ }, func(v []Segment) { v[0].ID = "other" }, func(v []Segment) {}} {
		v := cloneValue(segments)
		change(v)
		if _, _, e := s.revise("sample", createRevisionRequest{ParentRevisionNo: 1, Reason: "校正文字", Segments: v}); e == nil {
			t.Fatal("accepted invalid revision")
		}
	}
	if len(s.revisionsFor("sample")) != 1 {
		t.Fatal("invalid request created revision")
	}
}
func TestQueuedCancelNeverCallsModel(t *testing.T) {
	s, _ := storedFixture(t)
	m := testManager(s)
	_ = s.update("sample", func(j *Job) error { j.Status = jobQueued; j.Mode = "report_only"; return nil })
	m.analyzeText = func(context.Context, string, string, []Segment, progressFunc) (AnalysisOutput, error) {
		t.Fatal("cancelled task dispatched")
		return AnalysisOutput{}, nil
	}
	if e := m.cancel("sample"); e != nil {
		t.Fatal(e)
	}
	m.run("sample")
	j, _ := s.get("sample")
	if j.Status != jobCancelled {
		t.Fatal(j.Status)
	}
}
func TestReportOnlyUsesRevisedTextAndPreservesHistory(t *testing.T) {
	s, segments := storedFixture(t)
	m := testManager(s)
	v := cloneValue(segments)
	v[0].Text = "n 和 l 不分。"
	_, _, e := m.createRevision("sample", createRevisionRequest{ParentRevisionNo: 1, Reason: "校正拼音", Segments: v})
	if e != nil {
		t.Fatal(e)
	}
	// No FFmpeg or ASR path is available. A revision run must only use text.
	m.analyzeText = func(_ context.Context, _, _ string, input []Segment, _ progressFunc) (AnalysisOutput, error) {
		if input[0].Text != v[0].Text {
			t.Fatal("wrong revision input")
		}
		return AnalysisOutput{ReportStatus: reportReady, Segments: input, Report: insufficientReport("样本证据不足"), Events: EventSet{Events: []TeachingEvent{{ID: "ev1", Type: "term_explanation", Summary: "解释拼音", DimensionCodes: []string{"expression", "content"}, Quotes: []EventQuote{{SegmentID: input[0].ID, Text: input[0].Text}}}}}}, nil
	}
	m.run("sample")
	j, _ := s.get("sample")
	if j.Status != jobSucceeded || j.RevisionNo != 2 || len(j.ReportVersions) != 1 {
		t.Fatalf("%+v", j)
	}
	_ = s.update("sample", func(j *Job) error { j.Segments[0].Text = "later text"; return nil })
	j, _ = s.get("sample")
	if j.ReportVersions[0].Segments[0].Text != v[0].Text || j.ReportVersions[0].Events.Events[0].Quotes[0].Text != v[0].Text {
		t.Fatal("historical report sources changed")
	}
	reloaded, e := newJobStore(s.root)
	if e != nil {
		t.Fatal(e)
	}
	j, _ = reloaded.get("sample")
	if j.ReportVersions[0].InputRevision != 2 {
		t.Fatal("report version not persisted")
	}
}
func TestRoutesOriginConsentJSONAndMedia(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "fake-key")
	t.Setenv("BAILIAN_BASE_URL", "http://localhost/v1")
	s, _ := storedFixture(t)
	m := testManager(s)
	mux := http.NewServeMux()
	m.registerRoutes(mux, s.root)
	for _, path := range []string{"/api/jobs", "/api/jobs/sample/cancel", "/api/jobs/sample/retry", "/api/jobs/sample/reanalyze", "/api/jobs/sample/retranscribe", "/api/jobs/sample/revisions"} {
		r := httptest.NewRequest("POST", path, strings.NewReader("{}"))
		r.Header.Set("Origin", "http://evil.example")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	f, _ := mw.CreateFormFile("video", "lesson.mp4")
	f.Write([]byte("fake"))
	mw.Close()
	r := httptest.NewRequest("POST", "/api/jobs", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 422 || len(s.list()) != 1 {
		t.Fatal("missing consent accepted")
	}
	r = httptest.NewRequest("POST", "/api/jobs/sample/revisions", strings.NewReader(`{} {}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("trailing JSON accepted")
	}
	internal, _ := s.internalGet("sample")
	if e := os.WriteFile(internal.VideoPath, []byte("video data"), 0600); e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest("GET", "/api/jobs/sample/media", nil)
	r.Header.Set("Range", "bytes=0-4")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "video" {
		t.Fatalf("media: %d %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest("GET", "/api/jobs/sample", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), internal.VideoPath) || strings.Contains(w.Body.String(), "video_path") {
		t.Fatal("path exposed")
	}
}
func TestTextPipelineWithFakeProvider(t *testing.T) {
	segments := []Segment{{ID: "e-0001", Start: 0, End: 1000, Text: "n 和 l 不分。"}}
	events := EventSet{Events: []TeachingEvent{{ID: "ev1", Type: "term_explanation", Summary: "说明拼音区别", DimensionCodes: []string{"expression"}, Quotes: []EventQuote{{SegmentID: "e-0001", Text: "n 和 l"}}}}}
	report := Report{Summary: "说明拼音区别", IDs: []string{"e-0001"}, Observations: []Observation{{Code: "expression", Type: "observation", Title: "解释拼音区别", Text: "说明拼音区别", Analysis: "说明讨论对象，不能据此判断学生掌握程度。", EventIDs: []string{"ev1"}, IDs: []string{"e-0001"}}}}
	for _, c := range codes {
		d := Dimension{Code: c, Status: "insufficient", Limitation: "证据不足", IDs: []string{}}
		if c == "expression" {
			d.Status = "observed"
			d.Summary = "说明拼音区别"
			d.IDs = []string{"e-0001"}
		}
		report.Dimensions = append(report.Dimensions, d)
	}
	modelEvents := cloneValue(events)
	modelEvents.Events[0].Quotes = []EventQuote{{QuoteID: "e-0001-q01"}}
	// Extraction assigns both legal angles for a term explanation.
	if err := assignEventDimensions(&events); err != nil {
		t.Fatal(err)
	}
	responses := []any{map[string]any{"boundaries": []any{}}, modelEvents, draftFixture(report, events.Events)}
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls >= 3 {
			t.Error("unexpected retry")
			w.WriteHeader(500)
			return
		}
		var req struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if !req.Stream {
			t.Error("ASR called during text pipeline")
		}
		if calls == 1 && !strings.Contains(req.Messages[1].Content, "evidence_catalog") {
			t.Error("evidence catalog missing")
		}
		if calls == 2 && !strings.Contains(req.Messages[0].Content, "six-dim-text-v1") {
			t.Error("rubric missing from report prompt")
		}
		raw, _ := json.Marshal(responses[calls])
		calls++
		chunk, _ := json.Marshal(map[string]any{"id": fmt.Sprint(calls), "choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer provider.Close()
	out, err := analyzeSegments(context.Background(), provider.URL, "fake", segments, func(string, string, int) {})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || out.Events.Events[0].Quotes[0].Text != segments[0].Text {
		t.Fatal("pipeline incomplete")
	}
	if out.Report.AnalysisVersion != analysisVersion || out.Report.Observations[0].Analysis == "" {
		t.Fatal("coaching report metadata lost")
	}
}

func TestFailedTextPipelineKeepsReadingAndDoesNotRetry(t *testing.T) {
	segments := readingFixture()
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw := joinedFixture
		if calls == 2 {
			raw = `{"events":[{"id":"ev1","event_type":"definition","summary":"定义","dimension_codes":["content"],"quotes":[{"quote_id":"invented"}],"uncertainty":""}]}`
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": raw}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer provider.Close()
	out, err := analyzeSegments(context.Background(), provider.URL, "fake", segments, func(string, string, int) {})
	if err != nil || calls != 2 || out.Reading.Status != "ready" || out.Events.Events == nil || len(out.Report.Dimensions) != 6 || len(out.Warnings) != 1 {
		t.Fatalf("partial output lost or invalid fallback: %#v, %v", out, err)
	}
	if out.Reading.Paragraphs[0].Text != "A matrix is just a rectangular array of numbers." {
		t.Fatal("reading lost")
	}
	s, e := newJobStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.create("sample", "lesson.mp4", "课堂.mp4"); e != nil {
		t.Fatal(e)
	}
	if e = s.initialRevision("sample", segments); e != nil {
		t.Fatal(e)
	}
	_ = s.update("sample", func(j *Job) error { j.Status = jobQueued; j.Mode = "report_only"; j.InputRevision = 1; return nil })
	m := testManager(s)
	m.analyzeText = func(context.Context, string, string, []Segment, progressFunc) (AnalysisOutput, error) {
		return out, err
	}
	m.run("sample")
	reloaded, e := newJobStore(s.root)
	if e != nil {
		t.Fatal(e)
	}
	j, _ := reloaded.internalGet("sample")
	if j.Status != jobPartial || j.ReportStatus != reportFailed || !j.Retryable || j.FailureStage != "events" || j.Reading.Status != "ready" || len(j.ReportVersions) != 0 || len(j.Warnings) != 1 {
		t.Fatal("partial job did not preserve available results")
	}
}

func TestOldFailedJobReadingRecoveredWithoutModel(t *testing.T) {
	s, _ := storedFixture(t)
	_ = s.update("sample", func(j *Job) error {
		j.Status = jobFailed
		j.Segments = readingFixture()
		j.Reading = ReadingResult{}
		return nil
	})
	j, _ := s.get("sample")
	if len(j.Reading.Paragraphs) != 1 || j.Reading.Paragraphs[0].Text != "A matrix is just a rectangular array of numbers." {
		t.Fatal("old failed job still split")
	}
	internal, _ := s.internalGet("sample")
	if internal.Reading.Status != "" || internal.Segments[0].Text != "A matrix is just a." {
		t.Fatal("display recovery modified stored original")
	}
}

func TestPageAssetsAreServed(t *testing.T) {
	s, _ := storedFixture(t)
	mux := newDemoMux(testManager(s), s.root)
	for _, path := range []string{"/", "/jobs.js", "/recovery.js", "/analysis.js", "/report.css", "/events.js", "/transcript.js", "/api/health"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("asset %s unavailable", path)
		}
		if path == "/report.css" && w.Header().Get("Content-Type") != "text/css; charset=utf-8" {
			t.Fatal("report stylesheet must be served as CSS")
		}
	}
}

func TestHealthIdentifiesRunningReportRulesWithoutChangingTasks(t *testing.T) {
	s, _ := storedFixture(t)
	before, _ := s.get("sample")
	w := httptest.NewRecorder()
	newDemoMux(testManager(s), s.root).ServeHTTP(w, httptest.NewRequest("GET", "/api/health", nil))
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"status": "ok", "rubric_version": rubricVersion, "analysis_version": analysisVersion,
		"narrative_protocol": narrativeProtocol, "advice_protocol": adviceProtocol,
		"audit_version": reportAuditVersion, "semantic_protocol": semanticProtocol}
	after, _ := s.get("sample")
	if !reflect.DeepEqual(got, want) || w.Header().Get("Cache-Control") != "no-store" || !reflect.DeepEqual(before, after) {
		t.Fatal("runtime rules must reflect this binary, bypass cache, and leave saved tasks unchanged", got)
	}
}
