package analysis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallReportModelPreservesUsageWhenCandidateIsInvalid(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "provider-request-1",
			"choices": []map[string]any{{"message": map[string]any{"content": `not-json`}}},
			"usage":   map[string]any{"prompt_tokens": 17, "completion_tokens": 5},
		})
	}))
	defer provider.Close()

	service := &Service{cfg: Config{ReportAPIBase: provider.URL, ReportAPIKey: "test"}}
	result, err := service.callReportModel(context.Background(), []byte(`{}`))
	if err == nil || !result.UsageKnown || result.InputTokens != 17 || result.OutputTokens != 5 || result.ProviderRequestID != "provider-request-1" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestFitModelInputAccountsForCompleteRequestOverhead(t *testing.T) {
	config := reportSnapshot{Model: Model{Name: "fixture"}, SystemPrompt: reportSystemPrompt, MaxInputTokens: 256, MaxOutputTokens: 64}
	input := modelInput{
		SchemaVersion: SchemaVersion,
		RunID:         strings.Repeat("r", 36), SessionID: strings.Repeat("s", 36), MediaAssetID: strings.Repeat("m", 36),
		InputSHA256: strings.Repeat("a", 64), CourseContext: map[string]string{"course_name": "课程", "session_title": "课堂"},
		Limitations: []string{},
	}
	for i := 0; i < 20; i++ {
		input.Evidence = append(input.Evidence, modelEvidence{ID: strings.Repeat(string(rune('a'+i)), 36), Kind: "transcript", StartMS: int64(i * 1000), EndMS: int64(i*1000 + 500), TextContent: "短证据", TextSHA256: strings.Repeat("b", 64)})
	}

	fitted, err := fitModelInputToRequest(input, config, 0)
	if err != nil {
		t.Fatal(err)
	}
	body, err := buildReportRequest(fitted, config)
	if err != nil || len(body) > config.MaxInputTokens*8 {
		t.Fatalf("bounded request bytes=%d err=%v", len(body), err)
	}
	if len(fitted.Evidence) >= len(input.Evidence) || !strings.HasPrefix(fitted.Limitations[len(fitted.Limitations)-1], "MODEL_INPUT_SKIPPED_COUNT:") {
		t.Fatalf("selection was not disclosed: evidence=%d limitations=%v", len(fitted.Evidence), fitted.Limitations)
	}
}
