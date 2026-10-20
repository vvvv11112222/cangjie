package main

import (
	"context"
	"reflect"
)

// Feedback is guidance for a user-requested new generation, never a repair of
// a saved draft. Old event IDs are deliberately absent: extraction runs again.
type ReportFeedbackIssue struct {
	Code       string   `json:"code"`
	Dimension  string   `json:"requested_dimension"`
	Supported  []string `json:"previous_event_dimensions,omitempty"`
	SourceText []string `json:"original_quotes,omitempty"`
}

type ReportGenerationFeedback struct {
	Stage  string                `json:"previous_failure_stage"`
	Issues []ReportFeedbackIssue `json:"evidence_selection_errors"`
}

type reportFeedbackKey struct{}

func reportFeedback(ctx context.Context) *ReportGenerationFeedback {
	feedback, _ := ctx.Value(reportFeedbackKey{}).(*ReportGenerationFeedback)
	return feedback
}

func withReportFeedback(ctx context.Context, job Job, revision revisionFile) context.Context {
	// Only the most recent attempt on this exact immutable input is relevant.
	// A later successful report supersedes failure feedback.
	if len(job.ReportDiagnostics) == 0 {
		return ctx
	}
	diagnostic := job.ReportDiagnostics[len(job.ReportDiagnostics)-1]
	if diagnostic.InputRevision != revision.RevisionNo || !reflect.DeepEqual(diagnostic.Segments, revision.Segments) {
		return ctx
	}
	for _, version := range job.ReportVersions {
		if version.InputRevision == revision.RevisionNo && version.ReportStatus == reportReady && !version.CreatedAt.Before(diagnostic.CreatedAt) {
			return ctx
		}
	}
	feedback := buildReportFeedback(diagnostic)
	if len(feedback.Issues) == 0 {
		return ctx
	}
	return context.WithValue(ctx, reportFeedbackKey{}, &feedback)
}

func buildReportFeedback(diagnostic ReportDiagnostic) ReportGenerationFeedback {
	feedback := ReportGenerationFeedback{Stage: diagnostic.Stage, Issues: []ReportFeedbackIssue{}}
	if diagnostic.Candidate == nil {
		if diagnostic.LinkError != nil {
			feedback.Issues = append(feedback.Issues, ReportFeedbackIssue{Code: diagnostic.LinkError.Code})
		} else if diagnostic.Protocol == evidenceProtocol && diagnostic.Stage == "json" {
			feedback.Issues = append(feedback.Issues, ReportFeedbackIssue{Code: "invalid_report_format"})
		}
		return feedback
	}
	known := map[string]TeachingEvent{}
	for _, event := range diagnostic.Events.Events {
		known[event.ID] = event
	}
	for _, observation := range diagnostic.Candidate.Observations {
		if len(observation.EventIDs) == 0 {
			feedback.Issues = append(feedback.Issues, ReportFeedbackIssue{Code: "missing_event", Dimension: observation.Code})
		}
		seen := map[string]bool{}
		for _, id := range observation.EventIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			event, exists := known[id]
			issue := ReportFeedbackIssue{Dimension: observation.Code}
			if !exists {
				issue.Code = "unknown_event"
			} else if !contains(event.DimensionCodes, observation.Code) {
				issue.Code = "dimension_mismatch"
				issue.Supported = append([]string(nil), event.DimensionCodes...)
				for _, quote := range event.Quotes {
					issue.SourceText = append(issue.SourceText, quote.Text)
				}
			} else {
				continue
			}
			feedback.Issues = append(feedback.Issues, issue)
		}
	}
	return feedback
}
