package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var errReviewConflict = errors.New("复核记录已更新，请重新打开当前报告后再保存")
var errReviewReport = errors.New("只有通过校验的已保存报告版本可以复核")

type ReviewTarget struct {
	ReportVersion int    `json:"report_version"`
	InputRevision int    `json:"input_revision_no"`
	ReportHash    string `json:"report_hash"`
}

type ObservationReview struct {
	ReportVersion    int       `json:"report_version"`
	InputRevision    int       `json:"input_revision_no"`
	ReportHash       string    `json:"report_hash"`
	ObservationIndex int       `json:"observation_index"` // One based within the immutable report.
	Revision         int       `json:"revision"`
	Fact             string    `json:"fact_judgment"`
	Meaning          string    `json:"meaning_judgment"`
	Suggestion       string    `json:"suggestion_judgment"`
	Notes            string    `json:"notes"`
	Reviewer         string    `json:"reviewer"` // Self-reported, not an authenticated identity.
	UpdatedAt        time.Time `json:"updated_at"`
}

type ReviewRequest struct {
	ReportHash       string `json:"report_hash"`
	ExpectedRevision int    `json:"expected_revision"`
	Fact             string `json:"fact_judgment"`
	Meaning          string `json:"meaning_judgment"`
	Suggestion       string `json:"suggestion_judgment"`
	Notes            string `json:"notes"`
	Reviewer         string `json:"reviewer"`
}

func reviewHash(v ReportVersion) string {
	// Ignore mutable compatibility metadata; include all substantive sources.
	b, _ := json.Marshal(struct {
		InputRevision int
		Report        Report
		Segments      []Segment
		Events        EventSet
	}{v.InputRevision, v.Report, v.Segments, v.Events})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func reviewableReport(j *Job, version int) (ReportVersion, error) {
	for _, v := range j.ReportVersions {
		if v.Version != version {
			continue
		}
		if v.ReportStatus != reportReady || validateEvents(v.Events, v.Segments) != nil || validateEventReport(v.Report, v.Events.Events, v.Segments) != nil {
			return ReportVersion{}, errReviewReport
		}
		return v, nil
	}
	return ReportVersion{}, errReviewReport
}

func validateReviewRequest(req *ReviewRequest, o Observation) error {
	req.Notes, req.Reviewer = strings.TrimSpace(req.Notes), strings.TrimSpace(req.Reviewer)
	if !contains([]string{"supported", "unsupported", "uncertain"}, req.Fact) || !contains([]string{"bounded", "overreach", "uncertain"}, req.Meaning) {
		return errors.New("请分别填写原文支持与教学意义的复核判断")
	}
	if strings.TrimSpace(o.Suggestion) == "" {
		if req.Suggestion != "not_applicable" {
			return errors.New("本条没有建议，建议复核必须为不适用")
		}
	} else if !contains([]string{"usable", "not_usable", "uncertain"}, req.Suggestion) {
		return errors.New("请判断建议是否具体、可执行且适用于本片段")
	}
	if len([]rune(req.Notes)) > 2000 || len([]rune(req.Reviewer)) > 80 || req.ExpectedRevision < 0 {
		return errors.New("理由最多2000字，复核人最多80字，版本号不能为负")
	}
	if (req.Fact != "supported" || req.Meaning != "bounded" || req.Suggestion == "not_usable" || req.Suggestion == "uncertain") && len([]rune(req.Notes)) < 2 {
		return errors.New("存在不支持、越界或暂不能判断时，请填写至少两个字的具体理由")
	}
	return nil
}

func (s *jobStore) saveReview(id string, version, index int, req ReviewRequest) (ObservationReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var saved ObservationReview
	err := s.transaction(func() error {
		j := s.jobs[id]
		if j == nil {
			return os.ErrNotExist
		}
		v, err := reviewableReport(j, version)
		if err != nil {
			return err
		}
		if index < 1 || index > len(v.Report.Observations) {
			return errors.New("结论序号超出该报告范围")
		}
		if req.ReportHash != reviewHash(v) {
			return errReviewConflict
		}
		if err := validateReviewRequest(&req, v.Report.Observations[index-1]); err != nil {
			return err
		}
		previous, slot := 0, -1
		for i, item := range j.ReportReviews {
			if item.ReportVersion == version && item.ObservationIndex == index {
				if item.ReportHash != req.ReportHash {
					return errReviewConflict
				}
				previous, slot = item.Revision, i
				break
			}
		}
		// An identical resave is idempotent and must not consume a revision.
		if slot >= 0 && previous == req.ExpectedRevision {
			current := j.ReportReviews[slot]
			if current.Fact == req.Fact && current.Meaning == req.Meaning && current.Suggestion == req.Suggestion && current.Notes == req.Notes && current.Reviewer == req.Reviewer {
				saved = current
				return nil
			}
		}
		if previous != req.ExpectedRevision {
			return errReviewConflict
		}
		saved = ObservationReview{ReportVersion: version, InputRevision: v.InputRevision, ReportHash: req.ReportHash, ObservationIndex: index, Revision: previous + 1, Fact: req.Fact, Meaning: req.Meaning, Suggestion: req.Suggestion, Notes: req.Notes, Reviewer: req.Reviewer, UpdatedAt: time.Now().UTC()}
		if slot < 0 {
			j.ReportReviews = append(j.ReportReviews, saved)
		} else {
			j.ReportReviews[slot] = saved
		}
		j.UpdatedAt = saved.UpdatedAt
		return nil
	})
	return saved, err
}

// These are manual sample counts, not teacher scores or measured model accuracy.
type ReviewCounts struct {
	Total               int `json:"total"`
	Reviewed            int `json:"reviewed"`
	Confirmed           int `json:"confirmed"`
	Uncertain           int `json:"uncertain"`
	NeedsCorrection     int `json:"needs_correction"`
	IssuesReviewed      int `json:"issues_reviewed"`
	UnsupportedIssues   int `json:"unsupported_issues"`
	SuggestionsReviewed int `json:"suggestions_reviewed"`
	SuggestionsUsable   int `json:"suggestions_usable"`
}

type ReviewBundle struct {
	JobID      string              `json:"job_id"`
	VideoName  string              `json:"video_name"`
	Target     ReviewTarget        `json:"target"`
	Source     string              `json:"review_source"`
	Limitation string              `json:"limitation"`
	Report     ReportVersion       `json:"report_snapshot"`
	Reviews    []ObservationReview `json:"reviews"`
	Counts     ReviewCounts        `json:"counts"`
}

func buildReviewBundle(j *Job, v ReportVersion) ReviewBundle {
	hash := reviewHash(v)
	b := ReviewBundle{JobID: j.ID, VideoName: j.VideoName, Target: ReviewTarget{v.Version, v.InputRevision, hash}, Source: "manual_local_unverified_identity", Limitation: "本地人工填写，身份未经认证；仅统计本报告的已复核结论，不是教师评分或整体模型准确率。零分母表示暂不可计算，未复核不能视为通过。", Report: cloneValue(v), Reviews: []ObservationReview{}, Counts: ReviewCounts{Total: len(v.Report.Observations)}}
	seen := map[int]bool{}
	for _, item := range j.ReportReviews {
		if item.ReportVersion != v.Version || item.ReportHash != hash || item.ObservationIndex < 1 || item.ObservationIndex > len(v.Report.Observations) || seen[item.ObservationIndex] {
			continue
		}
		o := v.Report.Observations[item.ObservationIndex-1]
		check := ReviewRequest{Fact: item.Fact, Meaning: item.Meaning, Suggestion: item.Suggestion, Notes: item.Notes, Reviewer: item.Reviewer}
		if validateReviewRequest(&check, o) != nil {
			continue
		}
		seen[item.ObservationIndex] = true
		b.Reviews = append(b.Reviews, item)
		b.Counts.Reviewed++
		if item.Fact == "supported" && item.Meaning == "bounded" && (item.Suggestion == "usable" || item.Suggestion == "not_applicable") {
			b.Counts.Confirmed++
		}
		if item.Fact == "uncertain" || item.Meaning == "uncertain" || item.Suggestion == "uncertain" {
			b.Counts.Uncertain++
		}
		if item.Fact == "unsupported" || item.Meaning == "overreach" || item.Suggestion == "not_usable" {
			b.Counts.NeedsCorrection++
		}
		if o.Type == "issue" {
			b.Counts.IssuesReviewed++
			if item.Fact == "unsupported" || item.Meaning == "overreach" {
				b.Counts.UnsupportedIssues++
			}
		}
		if strings.TrimSpace(o.Suggestion) != "" {
			b.Counts.SuggestionsReviewed++
			if item.Suggestion == "usable" && item.Fact == "supported" && item.Meaning == "bounded" {
				b.Counts.SuggestionsUsable++
			}
		}
	}
	return b
}

func (s *jobStore) reviewBundle(id string, version int) (ReviewBundle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[id]
	if j == nil {
		return ReviewBundle{}, os.ErrNotExist
	}
	v, err := reviewableReport(j, version)
	if err != nil {
		return ReviewBundle{}, err
	}
	return buildReviewBundle(j, v), nil
}

func reviewHTTPError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, os.ErrNotExist) {
		status = http.StatusNotFound
	} else if errors.Is(err, errReviewConflict) || errors.Is(err, errReviewReport) {
		status = http.StatusConflict
	}
	writeJobJSON(w, status, map[string]any{"error": err.Error()})
}

func (s *jobStore) registerReviewRoutes(mux *http.ServeMux) {
	parseVersion := func(r *http.Request) (int, error) {
		v, err := strconv.Atoi(r.PathValue("version"))
		if err != nil || v < 1 {
			return 0, errors.New("报告版本号无效")
		}
		return v, nil
	}
	mux.HandleFunc("GET /api/jobs/{id}/reports/{version}/reviews", func(w http.ResponseWriter, r *http.Request) {
		v, err := parseVersion(r)
		if err != nil {
			reviewHTTPError(w, err)
			return
		}
		bundle, err := s.reviewBundle(r.PathValue("id"), v)
		if err != nil {
			reviewHTTPError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJobJSON(w, 200, bundle)
	})
	mux.HandleFunc("PUT /api/jobs/{id}/reports/{version}/reviews/{index}", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(w, r) {
			return
		}
		v, err := parseVersion(r)
		index, indexErr := strconv.Atoi(r.PathValue("index"))
		if err != nil || indexErr != nil || index < 1 {
			reviewHTTPError(w, fmt.Errorf("报告版本或结论序号无效"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var req ReviewRequest
		if err := decodeJSON(r, &req); err != nil {
			reviewHTTPError(w, errors.New("复核 JSON 无效或超过16KB"))
			return
		}
		saved, err := s.saveReview(r.PathValue("id"), v, index, req)
		if err != nil {
			reviewHTTPError(w, err)
			return
		}
		writeJobJSON(w, 200, map[string]any{"review": saved})
	})
}
