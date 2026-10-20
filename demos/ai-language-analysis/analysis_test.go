package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func coachingFixture() ([]Segment, EventSet, Report) {
	segments := []Segment{{ID: "e-0001", Start: 0, End: 10000, Text: "本课的目标是求解线性方程组。"}}
	events := EventSet{Events: []TeachingEvent{{ID: "ev-001", Type: "goal", Summary: "说明求解目标", DimensionCodes: []string{"content"}, Quotes: []EventQuote{{SegmentID: "e-0001", Text: segments[0].Text}}}}}
	report := insufficientReport("样本证据不足")
	report.AnalysisVersion = analysisVersion
	report.Summary, report.IDs = "本段先交代求解目标，不能据此确认目标落实。", []string{"e-0001"}
	for i := range report.Dimensions {
		if report.Dimensions[i].Code == "content" {
			report.Dimensions[i].Status, report.Dimensions[i].Summary, report.Dimensions[i].IDs = "observed", "开篇明确求解问题。", []string{"e-0001"}
		}
	}
	report.Observations = []Observation{{Code: "content", Type: "highlight", Title: "先交代求解目标", Text: "教师说明本课目标是求解线性方程组。", Analysis: "为后续概念提供共同的问题背景，不能确认学生已经理解。", Suggestion: "如果后文还没有回扣目标，可在概念讲解后说明它在求解中的作用。", Verification: "检查概念讲解后是否出现回扣目标的语句。", EventIDs: []string{"ev-001"}, IDs: []string{"e-0001"}}}
	return segments, events, report
}

func TestCoachingReportRequiresInterpretationAndCheckableAdvice(t *testing.T) {
	segments, events, report := coachingFixture()
	if err := validateEvents(events, segments); err != nil {
		t.Fatal(err)
	}
	if err := validateEventReport(report, events.Events, segments); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Report){
		func(r *Report) { r.Observations[0].Title = "" },
		func(r *Report) { r.Observations[0].Analysis = " " },
		func(r *Report) { r.Observations[0].Verification = "" },
		func(r *Report) { r.Observations[0].Type = "issue"; r.Observations[0].Suggestion = "" },
		func(r *Report) { r.Observations[0].EventIDs = []string{"unknown"} },
		func(r *Report) { r.AnalysisVersion = "unknown" },
		func(r *Report) { r.AnalysisVersion = "six-dim-coaching-v0" },
		func(r *Report) { r.AnalysisVersion = "six-dim-coaching-v9" },
	} {
		bad := cloneValue(report)
		change(&bad)
		if err := validateEventReport(bad, events.Events, segments); err == nil {
			t.Fatal("malformed coaching report accepted")
		}
	}
	// No invented advice is required. Legacy reports retain their old fields.
	noAdvice := cloneValue(report)
	noAdvice.Observations[0].Suggestion, noAdvice.Observations[0].Verification = "", ""
	if err := validateEventReport(noAdvice, events.Events, segments); err != nil {
		t.Fatal(err)
	}
	legacy := cloneValue(report)
	legacy.AnalysisVersion = ""
	legacy.Observations[0].Title, legacy.Observations[0].Analysis, legacy.Observations[0].Verification = "", "", ""
	if err := validateEventReport(legacy, events.Events, segments); err != nil {
		t.Fatal("legacy compatibility broken", err)
	}
	encoded, _ := json.Marshal(report)
	var decoded Report
	if err := strictJSON(string(encoded), &decoded); err != nil || !reflect.DeepEqual(decoded, report) {
		t.Fatal("coaching fields not preserved", err)
	}
}

func TestShallowModelOutputDoesNotBecomeCoachingReport(t *testing.T) {
	segments, events, report := coachingFixture()
	report.AnalysisVersion = ""
	report.Observations[0].Title, report.Observations[0].Analysis, report.Observations[0].Verification = "", "", ""
	events.Events[0].Quotes = []EventQuote{{QuoteID: "e-0001-q01"}}
	responses := []any{map[string]any{"boundaries": []any{}}, events, draftFixture(report, events.Events)}
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls >= len(responses) {
			t.Error("unexpected automatic retry")
			w.WriteHeader(500)
			return
		}
		raw, _ := json.Marshal(responses[calls])
		calls++
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer provider.Close()
	out, err := analyzeSegments(context.Background(), provider.URL, "fake", segments, func(string, string, int) {})
	if err != nil || out.ReportStatus != reportFailed || out.FailureStage != "report" || len(out.Events.Events) != 1 || calls != 3 {
		t.Fatalf("shallow report wrongly accepted: %+v %v", out, err)
	}
}

func TestSuccessfulJobCanReanalyzeWithoutChangingTranscript(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "fake")
	t.Setenv("BAILIAN_BASE_URL", "http://localhost/v1")
	for _, completeness := range []string{asrComplete, asrIncomplete, asrUnknown} {
		t.Run(completeness, func(t *testing.T) {
			s, original := storedFixture(t)
			old := readyFixture(original)
			if err := s.update("sample", func(j *Job) error {
				j.Status, j.ASRStatus, j.ReportStatus = jobSucceeded, completeness, reportReady
				j.Report, j.Events = old.Report, old.Events
				j.ReportVersions = []ReportVersion{{Version: 1, InputRevision: 1, ASRStatus: completeness, ReportStatus: reportReady, Segments: cloneValue(original), Events: old.Events, Report: old.Report}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			m := testManager(s)
			mux := http.NewServeMux()
			m.registerRoutes(mux, s.root)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/jobs/sample/reanalyze", nil))
			if w.Code != 202 || len(m.queue) != 1 {
				t.Fatal(w.Code, w.Body.String())
			}
			queued, _ := s.get("sample")
			if queued.Mode != "report_only" || queued.InputRevision != 1 || len(queued.ReportVersions) != 1 || queued.ReportStatus != reportPending {
				t.Fatal("wrong reanalysis input/history")
			}
			w = httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/jobs/sample/reanalyze", nil))
			if w.Code != 409 || len(m.queue) != 1 {
				t.Fatal("duplicate reanalysis queued")
			}
			calls := 0
			m.analyzeText = func(_ context.Context, _, _ string, input []Segment, _ progressFunc) (AnalysisOutput, error) {
				calls++
				if !reflect.DeepEqual(input, original) {
					t.Fatal("reanalyze changed input")
				}
				out := readyFixture(input)
				out.Report.AnalysisVersion = analysisVersion
				return out, nil
			}
			m.run("sample") // No media exists; dispatching ASR would fail this test.
			j, _ := s.get("sample")
			wantStatus := jobSucceeded
			if completeness != asrComplete {
				wantStatus = jobPartial
			}
			if calls != 1 || j.Status != wantStatus || j.ASRStatus != completeness || j.ReportStatus != reportReady || len(j.ReportVersions) != 2 || j.ReportVersions[0].Report.AnalysisVersion != "" || j.ReportVersions[1].Report.AnalysisVersion != analysisVersion || len(s.revisionsFor("sample")) != 1 || !reflect.DeepEqual(s.revisionsFor("sample")[0].Segments, original) {
				t.Fatalf("reanalysis lost provenance: %+v", j)
			}
		})
	}
}
