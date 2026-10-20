package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func readyFixture(segments []Segment) AnalysisOutput {
	return AnalysisOutput{ReportStatus: reportReady, Segments: cloneValue(segments), Events: EventSet{Events: []TeachingEvent{}}, Report: insufficientReport("样本证据不足")}
}

func TestPartialRetryPinsRevisionAndDoesNotRepeatASR(t *testing.T) {
	s, original := storedFixture(t)
	m := testManager(s)
	now := time.Now()
	if err := s.update("sample", func(j *Job) error {
		j.Status, j.Mode, j.ReportStatus = jobPartial, "full", reportFailed
		j.Retryable, j.FinishedAt, j.Error = true, &now, "六维报告生成失败"
		j.Report = insufficientReport("失败兜底")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.retry("sample"); err != nil {
		t.Fatal(err)
	}
	j, _ := s.get("sample")
	if j.Mode != "report_only" || j.InputRevision != 1 || j.FinishedAt != nil || j.ReportStatus != reportPending || len(j.Report.Dimensions) != 0 {
		t.Fatalf("bad reset: %+v", j)
	}
	if err := m.retry("sample"); err == nil {
		t.Fatal("duplicate retry accepted")
	}
	if len(m.queue) != 1 {
		t.Fatal("duplicate queued")
	}
	// Current display text is not authoritative: analysis uses immutable input.
	_ = s.update("sample", func(j *Job) error { j.Segments[0].Text = "not the revision"; return nil })
	calls := 0
	m.analyzeText = func(_ context.Context, _, _ string, input []Segment, _ progressFunc) (AnalysisOutput, error) {
		calls++
		if !reflect.DeepEqual(input, original) {
			t.Fatal("input revision changed")
		}
		out := readyFixture(input)
		out.Reading = buildReading(input, conservativeBoundaries(input), "conservative", "模拟阅读回退", ModelMeta{})
		out.Warnings = []string{"阅读模型不可用，使用保守整理"}
		return out, nil
	}
	m.run("sample") // Fixture media does not exist: any ASR path would fail.
	j, _ = s.get("sample")
	if calls != 1 || j.Status != jobSucceeded || j.ReportStatus != reportReady || len(j.ReportVersions) != 1 || j.Retryable || j.FinishedAt == nil {
		t.Fatalf("retry failed: %+v", j)
	}
	if j.ReportVersions[0].ASRStatus != asrComplete || j.ReportVersions[0].ReportStatus != reportReady || !reflect.DeepEqual(s.revisionsFor("sample")[0].Segments, original) {
		t.Fatal("outcomes or immutable input lost")
	}
}

func TestLegacyRecoveryPreservesHistoryAndKnownGap(t *testing.T) {
	for _, gap := range []bool{false, true} {
		t.Run(fmt.Sprint(gap), func(t *testing.T) {
			s, original := storedFixture(t)
			warnings := []string{"六维报告证据链校验失败：观察引用未知事件"}
			if gap {
				warnings = append(warnings, "第 1 段 ASR 失败: EOF")
			}
			_ = s.update("sample", func(j *Job) error {
				j.Status, j.ASRStatus, j.ReportStatus = jobSucceeded, "", ""
				j.Warnings, j.Report, j.Events = warnings, insufficientReport("兜底"), EventSet{Events: []TeachingEvent{}}
				j.ReportVersions = []ReportVersion{{Version: 1, InputRevision: 1, Segments: cloneValue(original), Warnings: warnings, Report: j.Report, Events: j.Events}}
				return nil
			})
			reloaded, err := newJobStore(s.root)
			if err != nil {
				t.Fatal(err)
			}
			j, _ := reloaded.get("sample")
			want := asrUnknown
			if gap {
				want = asrIncomplete
			}
			if j.Status != jobPartial || !j.Retryable || j.ReportStatus != reportFailed || j.ASRStatus != want || j.ReportVersions[0].ReportStatus != reportFailed || j.ReportVersions[0].ASRStatus != want {
				t.Fatalf("legacy classified wrong: %+v", j)
			}
			if !reflect.DeepEqual(reloaded.revisionsFor("sample")[0].Segments, original) || !reflect.DeepEqual(j.ReportVersions[0].Segments, original) {
				t.Fatal("legacy sources overwritten")
			}
			m := testManager(reloaded)
			if err = m.retry("sample"); err != nil {
				t.Fatal(err)
			}
			m.analyzeText = func(_ context.Context, _, _ string, input []Segment, _ progressFunc) (AnalysisOutput, error) {
				return readyFixture(input), nil
			}
			m.run("sample")
			j, _ = reloaded.get("sample")
			if j.Status != jobPartial || j.ReportStatus != reportReady || j.ASRStatus != want || len(j.ReportVersions) != 2 || j.ReportVersions[0].ReportStatus != reportFailed || j.ReportVersions[1].ASRStatus != want {
				t.Fatalf("text retry erased gap/history: %+v", j)
			}
			edited := cloneValue(original)
			edited[0].Text = "修订后的文字。"
			_, j, err = reloaded.revise("sample", createRevisionRequest{ParentRevisionNo: 1, Reason: "纠正文字", Segments: edited})
			if err != nil || j.ASRStatus != want || j.ReportStatus != reportPending || (gap && len(j.Warnings) != 1) {
				t.Fatal("revision lost gap", err)
			}
		})
	}
}

func TestReadyReportsAreRevalidatedAndLegitimateInsufficientIsReady(t *testing.T) {
	for _, invalid := range []string{"none", "missing_outcome", "bad_report", "bad_event"} {
		t.Run(invalid, func(t *testing.T) {
			s, segments := storedFixture(t)
			_ = s.update("sample", func(j *Job) error { j.Status, j.Mode, j.InputRevision = jobQueued, "report_only", 1; return nil })
			m := testManager(s)
			m.analyzeText = func(context.Context, string, string, []Segment, progressFunc) (AnalysisOutput, error) {
				out := readyFixture(segments)
				switch invalid {
				case "missing_outcome":
					out.ReportStatus = ""
				case "bad_report":
					out.Report = Report{}
				case "bad_event":
					out.Events.Events = []TeachingEvent{{ID: "bad", Type: "example", Summary: "伪造引文", DimensionCodes: []string{"content"}, Quotes: []EventQuote{{SegmentID: segments[0].ID, Text: "invented"}}}}
				}
				return out, nil
			}
			m.run("sample")
			j, _ := s.get("sample")
			if invalid == "none" {
				if j.Status != jobSucceeded || j.ReportStatus != reportReady || len(j.ReportVersions) != 1 {
					t.Fatal("all-insufficient wrongly failed")
				}
			} else if j.Status != jobPartial || j.ReportStatus != reportFailed || len(j.ReportVersions) != 0 {
				t.Fatal("invalid report published")
			}
		})
	}
}

func TestRetranscribeCreatesIndependentMediaAndLeavesOriginal(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "fake")
	t.Setenv("BAILIAN_BASE_URL", "http://localhost/v1")
	s, _ := storedFixture(t)
	source, _ := s.internalGet("sample")
	if err := os.WriteFile(source.VideoPath, []byte("retained video"), 0600); err != nil {
		t.Fatal(err)
	}
	originalState := cloneValue(source)
	revisions := s.revisionsFor("sample")
	m := testManager(s)
	mux := http.NewServeMux()
	m.registerRoutes(mux, s.root)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/jobs/sample/retranscribe", nil))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Job Job `json:"job"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	created, _ := s.internalGet(response.Job.ID)
	if created.ID == source.ID || created.VideoPath == source.VideoPath || created.Status != jobQueued || created.Mode != "full" || created.RevisionNo != 0 || created.ASRStatus != asrIncomplete || len(m.queue) != 1 {
		t.Fatalf("not independent: %+v", created)
	}
	if response.Job.VideoPath != "" {
		t.Fatal("private path leaked")
	}
	b, err := os.ReadFile(created.VideoPath)
	if err != nil || string(b) != "retained video" {
		t.Fatal("copy failed", err)
	}
	after, _ := s.internalGet("sample")
	if !reflect.DeepEqual(after, originalState) || !reflect.DeepEqual(revisions, s.revisionsFor("sample")) {
		t.Fatal("original changed")
	}
	_ = s.update("sample", func(j *Job) error { j.Status = jobRunning; return nil })
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/jobs/sample/retranscribe", nil))
	if w.Code != 409 || len(s.list()) != 2 {
		t.Fatal("active task copied")
	}
}

func TestOrdinaryExampleDoesNotSupportThinking(t *testing.T) {
	segments := []Segment{{ID: "s1", Text: "比如这个矩阵。"}}
	set := EventSet{Events: []TeachingEvent{{ID: "ev1", Type: "example", Summary: "举例", DimensionCodes: []string{"thinking"}, Quotes: []EventQuote{{SegmentID: "s1", Text: segments[0].Text}}}}}
	if err := assignEventDimensions(&set); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(set.Events[0].DimensionCodes, []string{"content"}) {
		t.Fatal("example promotes thinking")
	}
	r := insufficientReport("证据不足")
	for i := range r.Dimensions {
		if r.Dimensions[i].Code == "thinking" {
			r.Dimensions[i].Status, r.Dimensions[i].Summary, r.Dimensions[i].IDs = "observed", "思维训练", []string{"s1"}
		}
	}
	if err := validateEventReport(r, set.Events, segments); err == nil || !strings.Contains(err.Error(), "thinking") {
		t.Fatal("unsupported thinking accepted", err)
	}
}

func TestLegacyCheckpointWithoutRevisionCannotPretendComplete(t *testing.T) {
	s, err := newJobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.create("old", "missing-video.mp4", "old.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.update("old", func(j *Job) error {
		j.Status, j.ASRStatus, j.Retryable = jobFailed, "", true
		j.CompletedASRChunks = []int{0}
		j.Segments = []Segment{{ID: "e-0001", Start: 0, End: 1000, Text: "旧文字"}}
		return nil
	})
	m := testManager(s)
	if err := m.retry("old"); err == nil || !strings.Contains(err.Error(), "独立转写任务") {
		t.Fatal("unsafe checkpoint reused", err)
	}
	if len(m.queue) != 0 {
		t.Fatal("unsafe retry queued")
	}
	_ = s.update("old", func(j *Job) error { normalizeJobRecovery(j); j.Status = jobQueued; return nil })
	m.run("old")
	j, _ := s.get("old")
	if j.Status != jobFailed || j.ASRStatus != asrUnknown || j.RevisionNo != 0 || len(j.CompletedASRChunks) != 1 || !strings.Contains(j.Error, "独立转写任务") {
		t.Fatal("worker trusted legacy checkpoint")
	}
}

func TestTextFailureStagesDoNotCreateReports(t *testing.T) {
	for _, failure := range []string{"event_http", "report_http", "report_json", "report_validation", "report_evidence"} {
		t.Run(failure, func(t *testing.T) {
			segments := readingFixture()
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if (failure == "event_http" && calls == 2) || (failure == "report_http" && calls == 3) {
					http.Error(w, "simulated failure", 500)
					return
				}
				raw := joinedFixture
				if calls == 2 {
					raw = `{"events":[]}`
				}
				if calls == 3 {
					answer := insufficientReport("不足")
					if failure == "report_validation" {
						answer.Dimensions = nil
					}
					if failure == "report_evidence" {
						answer.Summary, answer.IDs = "没有对应事件的结论", []string{segments[0].ID}
					}
					b, _ := json.Marshal(draftFixture(answer, nil))
					raw = string(b)
					if failure == "report_json" {
						raw = `{`
					}
				}
				chunk, _ := json.Marshal(map[string]any{"id": fmt.Sprint(calls), "choices": []any{map[string]any{"delta": map[string]string{"content": raw}, "finish_reason": "stop"}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer provider.Close()
			out, err := analyzeSegments(context.Background(), provider.URL, "fake", segments, func(string, string, int) {})
			wantStage, wantCalls := "report", 3
			if failure == "event_http" {
				wantStage, wantCalls = "events", 2
			}
			if err != nil || out.ReportStatus != reportFailed || out.FailureStage != wantStage || out.FailureMessage == "" || out.Reading.Status != "ready" || calls != wantCalls {
				t.Fatalf("failed stage hidden: %+v %v calls=%d", out, err, calls)
			}
			s, err := newJobStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			_, _ = s.create("sample", "missing.mp4", "sample.mp4")
			if err := s.initialRevision("sample", segments); err != nil {
				t.Fatal(err)
			}
			_ = s.update("sample", func(j *Job) error { j.Mode, j.Status, j.InputRevision = "report_only", jobQueued, 1; return nil })
			m := testManager(s)
			m.analyzeText = func(context.Context, string, string, []Segment, progressFunc) (AnalysisOutput, error) {
				return out, nil
			}
			m.run("sample")
			j, _ := s.get("sample")
			if j.Status != jobPartial || !j.Retryable || j.ReportStatus != reportFailed || len(j.ReportVersions) != 0 || j.Reading.Status != "ready" || j.FailureStage != wantStage {
				t.Fatal("failed output published or intermediate lost")
			}
		})
	}
}
