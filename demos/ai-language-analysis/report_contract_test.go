package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdviceSchemaHasOneLocalSourceOfTruth(t *testing.T) {
	segments, events, _ := coachingFixture()
	schema := reportOutputSchema(events.Events, segments)
	encoded, err := json.Marshal(schema)
	if err != nil || strings.Contains(string(encoded), "next_step_observation") {
		t.Fatal("schema still asks for a second, cross-field advice selection", err)
	}
	observation := schema["properties"].(map[string]any)["observations"].(map[string]any)["items"].(map[string]any)
	coaching := observation["properties"].(map[string]any)["coaching"].(map[string]any)
	for _, field := range []string{"focus_quotes", "suggestion", "verification"} {
		if !contains(coaching["required"].([]string), field) {
			t.Fatal("local advice lost a mandatory component", field)
		}
	}
	draft := draftFixture(insufficientReport("当前没有足够材料"), nil)
	draft.NextStep = nil
	raw, _ := json.Marshal(draft)
	decoded, err := decodeReportDraft(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bindReportDraft(decoded, nil, nil); err != nil {
		t.Fatal("empty report still requires advice selection", err)
	}
}

func TestLegacyStreamingUsesSameReportContractAndEvidenceBinding(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("FFprobe unavailable")
	}
	t.Setenv("FFMPEG_PATH", ffmpeg)
	t.Setenv("FFPROBE_PATH", ffprobe)
	video := filepath.Join(t.TempDir(), "sample.mp4")
	if output, err := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=400:sample_rate=16000", "-t", "1", "-c:a", "aac", video).CombinedOutput(); err != nil {
		t.Fatalf("media fixture: %v %s", err, output)
	}
	media, err := os.ReadFile(video)
	if err != nil {
		t.Fatal(err)
	}
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint("missing_links=", missing), func(t *testing.T) {
			t.Setenv("BAILIAN_REPORT_AUDIT", "1")
			segments, events, report := coachingFixture()
			report.Observations[0].IDs = nil // Server must calculate these.
			report.Dimensions[0].IDs = []string{"e-0001", "e-0001"}
			if missing {
				report.Observations[0].EventIDs = nil
			}
			resolvedEvents := cloneValue(events)
			events.Events[0].Quotes = []EventQuote{{QuoteID: "e-0001-q01"}}
			asrCalls, textCalls := 0, 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Stream   bool                                `json:"stream"`
					Messages []struct{ Content json.RawMessage } `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if !request.Stream {
					asrCalls++
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": segments[0].Text}, "finish_reason": "stop"}}})
					return
				}
				textCalls++
				var answer any
				switch textCalls {
				case 1:
					answer = map[string]any{"boundaries": []any{}}
				case 2:
					answer = events
				case 3:
					var system, input string
					if len(request.Messages) == 2 {
						if err := json.Unmarshal(request.Messages[0].Content, &system); err != nil {
							t.Error(err)
						}
						if err := json.Unmarshal(request.Messages[1].Content, &input); err != nil {
							t.Error(err)
						}
					}
					if len(request.Messages) != 2 || !strings.HasSuffix(system, reportContractReminder) || !strings.Contains(input, `"output_schema"`) || !strings.Contains(input, `"evidence_bases"`) || !strings.Contains(input, `"dimension_summary_choices"`) {
						t.Error("legacy request still uses old report protocol")
					}
					answer = draftFixture(report, resolvedEvents.Events)
				case 4:
					answer = auditFixture(report)
				default:
					t.Error("unexpected paid retry")
					w.WriteHeader(500)
					return
				}
				raw, _ := json.Marshal(answer)
				chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer provider.Close()
			t.Setenv("DASHSCOPE_API_KEY", "local-fake-key")
			t.Setenv("BAILIAN_BASE_URL", provider.URL)
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			part, err := form.CreateFormFile("video", "sample.mp4")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = part.Write(media); err != nil {
				t.Fatal(err)
			}
			if err = form.WriteField("consent", "yes"); err != nil {
				t.Fatal(err)
			}
			if err = form.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "/api/run", &body)
			request.Header.Set("Content-Type", form.FormDataContentType())
			response := httptest.NewRecorder()
			run(response, request)
			decoder := json.NewDecoder(response.Body)
			result, failure := false, ""
			for {
				var emission struct {
					Type string          `json:"type"`
					Data json.RawMessage `json:"data"`
				}
				if err := decoder.Decode(&emission); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				if emission.Type == "error" {
					if err := json.Unmarshal(emission.Data, &failure); err != nil {
						t.Fatal(err)
					}
				}
				if emission.Type == "result" {
					result = true
					var output struct{ Report Report }
					if err := json.Unmarshal(emission.Data, &output); err != nil || output.Report.AnalysisVersion != analysisVersion || len(output.Report.Observations[0].IDs) != 1 || len(output.Report.Dimensions[0].IDs) != 1 || output.Report.Audit == nil || output.Report.Audit.Status != "no_flags" {
						t.Fatal("legacy report bypassed binding/normalization", err)
					}
				}
			}
			wantCalls := 4
			if missing {
				wantCalls = 3
			}
			if asrCalls != 1 || textCalls != wantCalls || result == missing || (missing && !strings.Contains(failure, "模型漏填")) || (!missing && failure != "") {
				t.Fatalf("legacy result=%v error=%q ASR=%d text=%d", result, failure, asrCalls, textCalls)
			}
		})
	}
}
