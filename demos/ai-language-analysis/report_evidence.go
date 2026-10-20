package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const evidenceProtocol = "evidence-basis-v2"

var dimensionLabels = map[string]string{
	"content": "内容组织", "pace": "教学推进", "thinking": "思维引导",
	"expression": "解释表达", "management": "活动组织", "technology": "工具支持",
}

func dimensionLabel(code string) string {
	if label := dimensionLabels[code]; label != "" {
		return label
	}
	return "未识别维度"
}

// Internal keys stay in diagnostic data; Error is readable without those keys.
type EvidenceLinkError struct {
	Code             string `json:"code"`
	ObservationIndex int    `json:"observation_index"` // One based.
	Title            string `json:"title,omitempty"`
	Dimension        string `json:"dimension_code"`
	EventID          string `json:"event_id,omitempty"`
	SegmentID        string `json:"segment_id,omitempty"`
	Message          string `json:"message"`
}

func (e *EvidenceLinkError) Error() string { return e.Message }

func evidenceTimeLabel(ms int64) string {
	seconds := ms / 1000
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

func eventDescription(event TeachingEvent, segments []Segment) string {
	first, last := int64(-1), int64(-1)
	for _, q := range event.Quotes {
		for _, s := range segments {
			if s.ID == q.SegmentID {
				if first < 0 || s.Start < first {
					first = s.Start
				}
				if s.End > last {
					last = s.End
				}
			}
		}
	}
	label := event.Summary
	if label == "" {
		label = "课堂依据"
	}
	if first >= 0 {
		label += " · " + evidenceTimeLabel(first) + "–" + evidenceTimeLabel(last)
	}
	return label
}

func linkError(code string, index int, o Observation, event TeachingEvent, segmentID string, segments []Segment) error {
	prefix := fmt.Sprintf("第 %d 条结论", index+1)
	if o.Title != "" {
		prefix += "「" + o.Title + "」"
	}
	prefix += "（" + dimensionLabel(o.Code) + "）"
	var reason string
	switch code {
	case "missing_event":
		reason = "：模型漏填了课堂依据关联，无法核对原文来源；已有转写已保留，无需重新转写"
	case "unknown_event":
		reason = "引用了本次事件列表中不存在的课堂依据"
	case "duplicate_event":
		reason = "重复引用了「" + eventDescription(event, segments) + "」"
	case "dimension_mismatch":
		labels := make([]string, 0, len(event.DimensionCodes))
		for _, c := range event.DimensionCodes {
			labels = append(labels, dimensionLabel(c))
		}
		reason = "引用的「" + eventDescription(event, segments) + "」当前归为" + strings.Join(labels, "、") + "，与本条结论的" + dimensionLabel(o.Code) + "不一致；需核对事件分类和结论范围"
	case "source_mismatch":
		reason = "引用了不属于所选课堂依据的原文片段"
		for _, s := range segments {
			if s.ID == segmentID {
				reason += "（" + evidenceTimeLabel(s.Start) + "–" + evidenceTimeLabel(s.End) + "）"
			}
		}
	case "incomplete_sources":
		reason = "未完整关联课堂依据的全部跨段原文"
	case "unobserved_dimension":
		reason = "所属维度标为暂不能判断或不适用，不能同时包含分析结论"
	}
	return &EvidenceLinkError{Code: code, ObservationIndex: index + 1, Title: o.Title, Dimension: o.Code, EventID: event.ID, SegmentID: segmentID, Message: prefix + reason}
}

type DimensionEventChoices struct {
	Code      string          `json:"dimension_code"`
	EventIDs  []string        `json:"allowed_event_ids"`
	SourceIDs []string        `json:"allowed_summary_source_ids"`
	Events    []TeachingEvent `json:"allowed_events"`
}

func reportEventChoices(events []TeachingEvent) []DimensionEventChoices {
	choices := make([]DimensionEventChoices, 0, len(codes))
	for _, code := range codes {
		choice := DimensionEventChoices{Code: code, EventIDs: []string{}, SourceIDs: []string{}, Events: []TeachingEvent{}}
		for _, event := range events {
			if contains(event.DimensionCodes, code) {
				choice.Events = append(choice.Events, cloneValue(event))
				choice.EventIDs = append(choice.EventIDs, event.ID)
				for _, quote := range event.Quotes {
					if !contains(choice.SourceIDs, quote.SegmentID) {
						choice.SourceIDs = append(choice.SourceIDs, quote.SegmentID)
					}
				}
			}
		}
		choices = append(choices, choice)
	}
	return choices
}

// Repeating an identical reference carries no additional evidence. Preserve
// nil/empty arrays and every distinct ID, including unknown IDs, for validation.
// This never adds evidence or infers a link from a summary or dimension label.
func normalizeReportReferenceDuplicates(report *Report) {
	dedupe := func(ids []string) []string {
		if ids == nil {
			return nil
		}
		result := make([]string, 0, len(ids))
		seen := map[string]bool{}
		for _, id := range ids {
			if !seen[id] {
				result = append(result, id)
				seen[id] = true
			}
		}
		return result
	}
	report.IDs = dedupe(report.IDs)
	for i := range report.Dimensions {
		report.Dimensions[i].IDs = dedupe(report.Dimensions[i].IDs)
	}
}

// Only equivalent duplicates and missing source links are safe to normalize.
// Never remove a mismatched event or an unrelated supplied source to pass checks.
func bindObservationEvidence(report *Report, events []TeachingEvent, segments []Segment) error {
	known := map[string]TeachingEvent{}
	for _, event := range events {
		known[event.ID] = event
	}
	for i := range report.Observations {
		o := &report.Observations[i]
		if len(o.EventIDs) == 0 {
			return linkError("missing_event", i, *o, TeachingEvent{}, "", segments)
		}
		seen, sources := map[string]bool{}, map[string]bool{}
		ids := make([]string, 0, len(o.EventIDs))
		for _, id := range o.EventIDs {
			event, ok := known[id]
			if !ok {
				return linkError("unknown_event", i, *o, TeachingEvent{ID: id}, "", segments)
			}
			if !contains(event.DimensionCodes, o.Code) {
				return linkError("dimension_mismatch", i, *o, event, "", segments)
			}
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
			for _, q := range event.Quotes {
				sources[q.SegmentID] = true
			}
		}
		for _, id := range o.IDs {
			if !sources[id] {
				return linkError("source_mismatch", i, *o, TeachingEvent{}, id, segments)
			}
		}
		// Source order is owned by the immutable transcript, not model output.
		refs := make([]string, 0, len(sources))
		for _, s := range segments {
			if sources[s.ID] {
				refs = append(refs, s.ID)
				delete(sources, s.ID)
			}
		}
		if len(sources) != 0 { // Defensive check even though events were validated.
			return linkError("incomplete_sources", i, *o, TeachingEvent{}, "", segments)
		}
		o.EventIDs, o.IDs = ids, refs
	}
	return nil
}

// Failed outputs are audit records, never ReportVersions. Snapshot hides these
// model drafts from normal task/list APIs and report exports.
type ReportDiagnostic struct {
	CreatedAt       time.Time          `json:"created_at"`
	InputRevision   int                `json:"input_revision_no"`
	AnalysisVersion string             `json:"analysis_version"`
	Protocol        string             `json:"evidence_protocol"`
	Stage           string             `json:"stage"`
	Message         string             `json:"message"`
	LinkError       *EvidenceLinkError `json:"link_error,omitempty"`
	Raw             string             `json:"raw_model_output,omitempty"`
	Candidate       *Report            `json:"candidate_report,omitempty"`
	Segments        []Segment          `json:"segments"`
	Events          EventSet           `json:"events"`
	RejectedEvents  []RejectedEvent    `json:"rejected_events,omitempty"`
	Request         ModelMeta          `json:"text_request"`
}

func failedReportDiagnostic(raw string, candidate *Report, stage string, err error, segments []Segment, events EventSet, meta ModelMeta) *ReportDiagnostic {
	d := &ReportDiagnostic{CreatedAt: time.Now().UTC(), AnalysisVersion: analysisVersion, Protocol: evidenceProtocol, Stage: stage, Message: err.Error(), Raw: raw, Segments: cloneValue(segments), Events: cloneValue(events), Request: cloneValue(meta)}
	if candidate != nil {
		copy := cloneValue(*candidate)
		d.Candidate = &copy
	}
	var link *EvidenceLinkError
	if errors.As(err, &link) {
		copy := *link
		d.LinkError = &copy
	}
	return d
}
