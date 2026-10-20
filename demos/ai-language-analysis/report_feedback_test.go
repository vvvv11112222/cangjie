package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFeedbackCollectsAllThreeWrongDimensionSelectionsWithoutRepair(t *testing.T) {
	segments, events, report := coachingFixture()
	texts := []string{
		"To describe first what I call the row picture, that's the picture of one equation at a time.",
		"The second picture is the picture a column at a time.",
		"So the third, the algebra way to look at the problem is the matrix form.",
	}
	for i, text := range texts {
		id, eventID := fmt.Sprintf("e-%04d", i+2), fmt.Sprintf("ev-%03d", i+2)
		segments = append(segments, Segment{ID: id, Start: int64(i+1) * 10000, End: int64(i+2) * 10000, Text: text})
		events.Events = append(events.Events, TeachingEvent{ID: eventID, Type: "transition", Summary: "预告讲授顺序", DimensionCodes: []string{"pace"}, Quotes: []EventQuote{{SegmentID: id, Text: text}}})
		report.Observations[0].EventIDs = append(report.Observations[0].EventIDs, eventID)
	}
	report.Observations[0].Title = "以核心问题统整多重视角"
	before := cloneValue(report)
	err := bindObservationEvidence(&report, events.Events, segments)
	var link *EvidenceLinkError
	if err == nil {
		t.Fatal("content conclusion accepted three pace-only events")
	}
	diagnostic := failedReportDiagnostic("unpublished", &before, "event_binding", err, segments, events, ModelMeta{})
	link = diagnostic.LinkError
	if link == nil || link.Code != "dimension_mismatch" {
		t.Fatal("expected dimension mismatch")
	}
	snapshot := cloneValue(*diagnostic)
	feedback := buildReportFeedback(*diagnostic)
	if len(feedback.Issues) != 3 {
		t.Fatal("only first wrong event reported", feedback)
	}
	for i, issue := range feedback.Issues {
		if issue.Code != "dimension_mismatch" || issue.Dimension != "content" || !reflect.DeepEqual(issue.Supported, []string{"pace"}) || !reflect.DeepEqual(issue.SourceText, []string{texts[i]}) {
			t.Fatal("feedback lost actual mismatch or original quote", issue)
		}
	}
	encoded, _ := json.Marshal(feedback)
	if strings.Contains(string(encoded), "ev-") || !reflect.DeepEqual(snapshot, *diagnostic) || !reflect.DeepEqual(before, report) {
		t.Fatal("old event identifiers leaked or draft repaired in place")
	}
	feedback.Issues[0].Supported[0] = "content"
	if diagnostic.Events.Events[1].DimensionCodes[0] != "pace" {
		t.Fatal("feedback aliases diagnostic")
	}
}

func TestFeedbackOnlyAppliesToSameInputAndUnsupersededAttempt(t *testing.T) {
	segments, events, report := coachingFixture()
	report.Observations[0].EventIDs = nil
	diagnostic := failedReportDiagnostic("", &report, "event_binding", fmt.Errorf("missing"), segments, events, ModelMeta{})
	diagnostic.InputRevision = 1
	job := Job{ReportDiagnostics: []ReportDiagnostic{*diagnostic}}
	revision := revisionFile{RevisionNo: 1, Segments: segments}
	ctx := withReportFeedback(context.Background(), job, revision)
	if feedback := reportFeedback(ctx); feedback == nil || len(feedback.Issues) != 1 || feedback.Issues[0].Code != "missing_event" {
		t.Fatal("same-input failed attempt not fed back")
	}
	for _, scenario := range []string{"revision", "text", "time", "latest_attempt", "success"} {
		t.Run(scenario, func(t *testing.T) {
			changedJob, changedRevision := cloneValue(job), cloneValue(revision)
			switch scenario {
			case "revision":
				changedRevision.RevisionNo++
			case "text":
				changedRevision.Segments[0].Text = "人工修订后的文字。"
			case "time":
				changedRevision.Segments[0].End++
			case "latest_attempt":
				changedJob.ReportDiagnostics = append(changedJob.ReportDiagnostics, ReportDiagnostic{InputRevision: 2})
			case "success":
				changedJob.ReportVersions = []ReportVersion{{InputRevision: 1, ReportStatus: reportReady, CreatedAt: diagnostic.CreatedAt.Add(time.Second)}}
			}
			if reportFeedback(withReportFeedback(context.Background(), changedJob, changedRevision)) != nil {
				t.Fatal("stale feedback carried into new analysis")
			}
		})
	}
}

func TestManualRetrySendsFeedbackAndUsesNewEventsWithoutExtraRequests(t *testing.T) {
	for _, repeatsMistake := range []bool{false, true} {
		t.Run(fmt.Sprint("repeats_mistake=", repeatsMistake), func(t *testing.T) {
			store, segments := storedFixture(t)
			_, oldEvents, candidate := coachingFixture()
			oldEvents.Events[0].ID = "ev-old"
			oldEvents.Events[0].Quotes = []EventQuote{{SegmentID: segments[0].ID, Text: segments[0].Text}}
			candidate.Observations[0].Code, candidate.Observations[0].EventIDs = "thinking", []string{"ev-old"}
			diagnostic := failedReportDiagnostic("old draft", &candidate, "event_binding", fmt.Errorf("mismatch"), segments, oldEvents, ModelMeta{})
			diagnostic.InputRevision = 1
			if err := store.update("sample", func(job *Job) error {
				job.Status, job.ReportStatus, job.Retryable = jobPartial, reportFailed, true
				job.ReportDiagnostics = []ReportDiagnostic{*diagnostic}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			_, newEvents, answer := coachingFixture()
			newEvents.Events[0].ID = "ev-new-goal"
			newEvents.Events[0].Quotes = []EventQuote{{QuoteID: "e-0001-q01"}}
			newEvents.Events = append(newEvents.Events, TeachingEvent{ID: "ev-new-transition", Type: "transition", Summary: "模拟转场", Quotes: []EventQuote{{QuoteID: "e-0001-q01"}}})
			answer.Observations[0].EventIDs = []string{"ev-new-goal"}
			if repeatsMistake {
				answer.Observations[0].EventIDs = append(answer.Observations[0].EventIDs, "ev-new-transition")
			}
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Stream   bool                       `json:"stream"`
					Messages []struct{ Content string } `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !request.Stream || len(request.Messages) != 2 {
					t.Error("unexpected ASR or invalid request", err)
					w.WriteHeader(400)
					return
				}
				calls++
				var input struct {
					Feedback *ReportGenerationFeedback `json:"previous_validation_feedback"`
					Choices  []DimensionSummaryChoice  `json:"dimension_summary_choices"`
					Bases    []ReportBasis             `json:"evidence_bases"`
				}
				if calls > 1 {
					if err := json.Unmarshal([]byte(request.Messages[1].Content), &input); err != nil {
						t.Error(err)
					}
				}
				var response any
				switch calls {
				case 1:
					response = map[string]any{"boundaries": []any{}}
					if strings.Contains(request.Messages[1].Content, "previous_validation_feedback") {
						t.Error("reading stage receives unnecessary failed report context")
					}
				case 2, 3:
					if input.Feedback == nil || len(input.Feedback.Issues) != 1 || input.Feedback.Issues[0].Code != "dimension_mismatch" || strings.Contains(request.Messages[1].Content, "ev-old") {
						t.Error("failure feedback missing or old IDs reused")
					}
					response = newEvents
					if calls == 3 {
						currentEvents := cloneValue(newEvents)
						if err := assignEventDimensions(&currentEvents); err != nil {
							t.Error(err)
						}
						for i := range currentEvents.Events {
							for _, basis := range input.Bases {
								if basis.Evidence.ID == currentEvents.Events[i].ID {
									currentEvents.Events[i].Quotes = cloneValue(basis.Evidence.Quotes)
									break
								}
							}
						}
						response = draftFixture(answer, currentEvents.Events)
						if repeatsMistake {
							response = answer
						} // Old mixed links must still be rejected.
						if len(input.Choices) != 6 || len(input.Bases) != 2 || input.Bases[0].Evidence.ID != "ev-new-goal" || !reflect.DeepEqual(input.Choices[0].SourceIDs, []string{"e-0001"}) {
							t.Error("current constrained event/source choices missing")
						}
					}
				default:
					t.Error("automatic repair request added")
					w.WriteHeader(500)
					return
				}
				raw, _ := json.Marshal(response)
				chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer provider.Close()
			t.Setenv("BAILIAN_BASE_URL", provider.URL)
			t.Setenv("DASHSCOPE_API_KEY", "local-fake")
			manager := testManager(store)
			if err := manager.retry("sample"); err != nil {
				t.Fatal(err)
			}
			manager.run("sample")
			after, _ := store.internalGet("sample")
			if calls != 3 || !reflect.DeepEqual(after.Segments, segments) || !reflect.DeepEqual(after.ReportDiagnostics[0], *diagnostic) {
				t.Fatal("request count, transcript or original diagnostic changed")
			}
			if repeatsMistake {
				if after.ReportStatus != reportFailed || len(after.ReportVersions) != 0 || len(after.ReportDiagnostics) != 2 {
					t.Fatal("feedback weakened validation or published failed candidate")
				}
			} else if after.ReportStatus != reportReady || len(after.ReportVersions) != 1 || after.Report.Observations[0].EventIDs[0] != "ev-new-goal" {
				t.Fatal("valid new selection failed", after.Error)
			}
		})
	}
}

func TestDimensionSummarySchemaOnlyListsSourcesFromThatDimension(t *testing.T) {
	segments, events, _ := coachingFixture()
	segments = append(segments, Segment{ID: "e-0002", Text: "下一步看例子。"})
	events.Events = append(events.Events, TeachingEvent{ID: "ev-002", Type: "transition", DimensionCodes: []string{"pace"}, Quotes: []EventQuote{{SegmentID: "e-0002", Text: segments[1].Text}}})
	schema := reportOutputSchema(events.Events, segments)
	branches := schema["properties"].(map[string]any)["dimensions"].(map[string]any)["items"].(map[string]any)["anyOf"].([]any)
	if len(branches) != 6 {
		t.Fatal("six dimension branches required")
	}
	for i, branch := range branches {
		fields := branch.(map[string]any)["properties"].(map[string]any)
		sources := fields["summary_evidence_ids"].(map[string]any)
		if i < 2 {
			ids := sources["items"].(map[string]any)["enum"].([]string)
			if !reflect.DeepEqual(ids, []string{segments[i].ID}) {
				t.Fatal("summary schema allows other dimension's sources")
			}
		} else if sources["maxItems"] != 0 || !reflect.DeepEqual(fields["coverage_status"].(map[string]any)["enum"], []string{"insufficient"}) {
			t.Fatal("empty dimension schema allows unsupported summary")
		}
	}
}
