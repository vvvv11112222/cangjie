package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func reviewedFixture(t *testing.T) (*jobStore, ReportVersion) {
	t.Helper()
	s, _ := storedFixture(t)
	segments, events, report := coachingFixture()
	if err := validateEvents(events, segments); err != nil {
		t.Fatal(err)
	}
	if err := validateEventReport(report, events.Events, segments); err != nil {
		t.Fatal(err)
	}
	if err := s.update("sample", func(j *Job) error {
		j.ASRStatus, j.ReportStatus, j.Report, j.Events = asrComplete, reportReady, report, events
		j.Segments, j.InputRevision = cloneValue(segments), 1
		j.ReportVersions = []ReportVersion{{Version: 1, InputRevision: 1, ASRStatus: asrComplete, ReportStatus: reportReady, Segments: cloneValue(segments), Events: events, Report: report}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	j, _ := s.get("sample")
	return s, j.ReportVersions[0]
}

func reviewRequest(hash string, revision int) ReviewRequest {
	return ReviewRequest{ReportHash: hash, ExpectedRevision: revision, Fact: "supported", Meaning: "bounded", Suggestion: "usable", Reviewer: "测试员"}
}

func TestReviewRoundTripAndConflict(t *testing.T) {
	s, v := reviewedFixture(t)
	hash := reviewHash(v)
	saved, err := s.saveReview("sample", 1, 1, reviewRequest(hash, 0))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 {
		t.Fatal("expected first revision", saved.Revision)
	}
	if repeated, err := s.saveReview("sample", 1, 1, reviewRequest(hash, saved.Revision)); err != nil || repeated != saved {
		t.Fatalf("identical resave must return the existing record: %+v %v", repeated, err)
	}
	saved, err = s.saveReview("sample", 1, 1, ReviewRequest{ReportHash: hash, ExpectedRevision: saved.Revision, Fact: "unsupported", Meaning: "overreach", Suggestion: "not_usable", Notes: "原文没有出现举例", Reviewer: "测试员"})
	if err != nil || saved.Revision != 2 {
		t.Fatalf("update failed: %+v %v", saved, err)
	}
	if _, err = s.saveReview("sample", 1, 1, reviewRequest(hash, saved.Revision-1)); err != errReviewConflict {
		t.Fatal("stale revision accepted")
	}
	if _, err = s.saveReview("sample", 1, 1, ReviewRequest{ReportHash: "wrong", ExpectedRevision: 2, Fact: "supported", Meaning: "bounded", Suggestion: "usable"}); err != errReviewConflict {
		t.Fatal("wrong hash accepted")
	}
	bundle, err := s.reviewBundle("sample", 1)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Counts.Total != 1 || bundle.Counts.Reviewed != 1 || bundle.Counts.NeedsCorrection != 1 || bundle.Counts.IssuesReviewed != 0 {
		t.Fatalf("bad counts: %+v", bundle.Counts)
	}
	if bundle.Reviews[0].Fact != "unsupported" || bundle.Target.ReportHash == "" {
		t.Fatal("bundle lost review data")
	}
	if _, err = s.saveReview("sample", 1, 1, ReviewRequest{ReportHash: hash, ExpectedRevision: 2, Fact: "supported", Meaning: "bounded", Suggestion: "not_usable"}); err == nil {
		t.Fatal("not_usable without reason accepted")
	}
	bundle, _ = s.reviewBundle("sample", 1)
	if bundle.Reviews[0].Fact != "unsupported" {
		t.Fatal("failed save mutated stored review")
	}
}

func TestCurrentReviewTargetRequiresMatchingPublishedSnapshot(t *testing.T) {
	s, v := reviewedFixture(t)
	j, _ := s.get("sample")
	if j.ReportVersion != 1 || len(j.ReviewTargets) != 1 {
		t.Fatalf("current report target missing: %+v", j.ReviewTargets)
	}
	if err := s.update("sample", func(j *Job) error {
		next := cloneValue(v)
		next.Version = 2
		j.ReportVersions = append(j.ReportVersions, next)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	j, _ = s.get("sample")
	if j.ReportVersion != 2 {
		t.Fatal("must select latest exact matching snapshot")
	}
	for _, change := range []func(*Job){
		func(j *Job) { j.Report.Summary += "different" },
		func(j *Job) { j.ReportStatus = reportPending },
		func(j *Job) { j.InputRevision = 2 },
		func(j *Job) { j.Segments[0].Text += "changed source" },
	} {
		copyJob := cloneValue(j)
		change(&copyJob)
		if s.snapshot(&copyJob).ReportVersion != 0 {
			t.Fatal("stale report bound to a review target")
		}
	}
}

func TestReviewConfirmedRequiresUsableSuggestion(t *testing.T) {
	s, v := reviewedFixture(t)
	req := reviewRequest(reviewHash(v), 0)
	req.Suggestion, req.Notes = "not_usable", "不适用本片段"
	if _, err := s.saveReview("sample", 1, 1, req); err != nil {
		t.Fatal(err)
	}
	b, err := s.reviewBundle("sample", 1)
	if err != nil || b.Counts.Confirmed != 0 || b.Counts.NeedsCorrection != 1 {
		t.Fatalf("bad counts: %+v %v", b.Counts, err)
	}
}

func TestReviewRejectsInvalidTargets(t *testing.T) {
	s, v := reviewedFixture(t)
	if _, err := s.saveReview("sample", 9, 1, reviewRequest(reviewHash(v), 0)); err != errReviewReport {
		t.Fatal("unknown version accepted")
	}
	if _, err := s.saveReview("sample", 1, 5, reviewRequest(reviewHash(v), 0)); err == nil {
		t.Fatal("out-of-range observation accepted")
	}
	if _, err := s.saveReview("missing", 1, 1, reviewRequest(reviewHash(v), 0)); err == nil {
		t.Fatal("missing job accepted")
	}
}

func TestReviewRoutesServeBundleAndPut(t *testing.T) {
	s, v := reviewedFixture(t)
	m := testManager(s)
	mux := http.NewServeMux()
	m.registerRoutes(mux, s.root)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/jobs/sample/reports/1/reviews", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "manual_local_unverified_identity") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	payload := "{\"report_hash\":\"" + reviewHash(v) + "\",\"expected_revision\":0,\"fact_judgment\":\"supported\",\"meaning_judgment\":\"bounded\",\"suggestion_judgment\":\"usable\",\"reviewer\":\"测试员\"}"
	req := httptest.NewRequest("PUT", "/api/jobs/sample/reports/1/reviews/1", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1"
	req.Header.Set("Origin", "http://127.0.0.1")
	mux.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "observation_index") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/jobs/sample/reports/1/reviews", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "manual_local_unverified_identity") {
		t.Fatal("bundle not persisted")
	}
}
