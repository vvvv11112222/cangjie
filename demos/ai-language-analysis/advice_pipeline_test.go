package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMultipleAdviceFindingsPublishOrPreserveFailedCandidate(t *testing.T) {
	for _, scenario := range []string{"local_advice", "legacy_hint", "forged_third_quote", "missing_third_operation"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("BAILIAN_REPORT_AUDIT", "0")
			segments := []Segment{
				{ID: "e-0001", Start: 0, End: 10000, Text: "本课的目标是求解线性方程组。"},
				{ID: "e-0002", Start: 10000, End: 20000, Text: "例如，两个方程的图像交于同一点。"},
				{ID: "e-0003", Start: 20000, End: 30000, Text: "方程组的解是同时满足各个方程的未知数取值。", Boundary: "end_of_audio"},
			}
			store, err := newJobStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.create("sample", filepath.Join(store.root, "lesson.mp4"), "模拟课堂.mp4"); err != nil {
				t.Fatal(err)
			}
			if err = store.initialRevision("sample", segments); err != nil {
				t.Fatal(err)
			}
			if err = store.update("sample", func(j *Job) error { j.Status = jobSucceeded; return nil }); err != nil {
				t.Fatal(err)
			}
			calls := 0
			var generated string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Stream   bool                       `json:"stream"`
					Messages []struct{ Content string } `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !request.Stream || len(request.Messages) != 2 {
					t.Error("unexpected ASR or malformed model request", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				calls++
				var answer any
				switch calls {
				case 1:
					answer = map[string]any{"boundaries": []any{}}
				case 2:
					kinds := []string{"goal", "example", "definition"}
					events := EventSet{Events: []TeachingEvent{}}
					for i, s := range segments {
						events.Events = append(events.Events, TeachingEvent{ID: fmt.Sprintf("ev-%03d", i+1), Type: kinds[i], Summary: s.Text,
							Quotes: []EventQuote{{QuoteID: fmt.Sprintf("%s-q01", s.ID)}}})
					}
					answer = events
				case 3:
					var input struct {
						Bases []ReportBasis `json:"evidence_bases"`
					}
					if err := json.Unmarshal([]byte(request.Messages[1].Content), &input); err != nil {
						t.Error(err)
					}
					if strings.Contains(request.Messages[1].Content, "next_step_observation") || !strings.Contains(request.Messages[1].Content, adviceProtocol) {
						t.Error("generation request still uses the old cross-field selection")
					}
					empty := insufficientReport("本段未提供足够依据")
					draft := ReportDraft{Summary: "本段交代求解目标，用图像交点举例，并说明解的含义。", IDs: []string{"e-0001", "e-0002", "e-0003"}, Dimensions: empty.Dimensions, Observations: []ObservationDraft{}}
					draft.Dimensions[0] = Dimension{Code: "content", Status: "observed", Summary: "目标、图像例子与解的定义构成本段求解内容。", IDs: cloneValue(draft.IDs)}
					for _, s := range segments {
						for _, basis := range input.Bases {
							if basis.Code != "content" || basis.Evidence.Quotes[0].SegmentID != s.ID {
								continue
							}
							draft.Observations = append(draft.Observations, ObservationDraft{Key: basis.Key, Type: "observation", Title: basis.Evidence.Summary,
								Text: s.Text, Analysis: "这段原话提供了与求解相关的教学内容，不能据此判断学生掌握情况。",
								Coaching: &CoachingDraft{Quotes: []AdviceQuote{{SegmentID: s.ID, Text: s.Text}}, Suggestion: "讲解后，可请学生用自己的话解释这句原话，并用本段例子说明。", Verification: "观察学生解释是否与本句原话一致，若只重复术语，再请其说明例子与原话的联系。"}})
							break
						}
					}
					if len(draft.Observations) != 3 {
						t.Error("missing independent findings")
					}
					switch scenario {
					case "legacy_hint":
						draft.NextStep = ptrInt(1)
					case "forged_third_quote":
						draft.Observations[2].Coaching.Quotes[0].Text = "课堂未出现的原话"
					case "missing_third_operation":
						draft.Observations[2].Coaching.Suggestion = ""
					}
					answer = draft
				default:
					t.Error("unexpected retry or extra model request")
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				raw, _ := json.Marshal(answer)
				if calls == 3 {
					generated = string(raw)
				}
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
			job, _ := store.internalGet("sample")
			if calls != 3 || !reflect.DeepEqual(job.Segments, segments) || len(store.revisionsFor("sample")) != 1 {
				t.Fatal("request count or saved transcript changed", calls)
			}
			if strings.HasPrefix(scenario, "local") || scenario == "legacy_hint" {
				if job.ReportStatus != reportReady || len(job.ReportVersions) != 1 || len(job.Report.Observations) != 3 || len(job.ReportDiagnostics) != 0 {
					t.Fatal("valid multiple-advice report was not published", job.Error)
				}
				for i, o := range job.Report.Observations {
					if o.Suggestion == "" || o.Verification == "" || !reflect.DeepEqual(o.IDs, []string{segments[i].ID}) {
						t.Fatal("advice or evidence lost", i)
					}
				}
			} else {
				if job.ReportStatus != reportFailed || len(job.ReportVersions) != 0 || len(job.ReportDiagnostics) != 1 || !strings.Contains(job.Error, "建议校验失败") {
					t.Fatal("invalid third advice published or misclassified", job.Error)
				}
				d := job.ReportDiagnostics[0]
				if d.Raw != generated || d.Stage != "basis_binding" || !reflect.DeepEqual(d.Segments, segments) || len(d.Events.Events) != 3 {
					t.Fatal("failed candidate or evidence snapshot lost")
				}
			}
		})
	}
}
