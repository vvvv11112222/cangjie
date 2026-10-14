package analysis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestMalformedChoicesPreserveBilling(t *testing.T) {
	for _, choices := range []string{`[]`, `{}`, `17`, `[{"message":{}}]`, `[{"message":{"content":17}}]`, `[{"message":{"content":"not-json"}}]`} {
		t.Run(choices, func(t *testing.T) {
			requests := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				_, _ = w.Write([]byte(`{"id":"known-bill","usage":{"prompt_tokens":17,"completion_tokens":5},"choices":` + choices + `}`))
			}))
			defer provider.Close()
			s := &Service{cfg: Config{ReportAPIBase: provider.URL}}
			result, err := s.callReportModel(context.Background(), []byte(`{}`))
			if err == nil || !result.UsageKnown || result.InputTokens != 17 || result.OutputTokens != 5 || result.ProviderRequestID != "known-bill" || requests != 1 {
				t.Fatalf("billing was lost or request retried: %#v, %v, requests=%d", result, err, requests)
			}
		})
	}
}

func TestMalformedUsageRemainsUnknown(t *testing.T) {
	for _, usage := range []string{`null`, `{}`, `{"prompt_tokens":-1,"completion_tokens":5}`, `{"prompt_tokens":"17","completion_tokens":5}`} {
		t.Run(usage, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"id":"unknown-bill","choices":[],"usage":` + usage + `}`))
			}))
			defer provider.Close()
			s := &Service{cfg: Config{ReportAPIBase: provider.URL}}
			result, err := s.callReportModel(context.Background(), []byte(`{}`))
			if err == nil || result.UsageKnown || result.ProviderRequestID != "unknown-bill" {
				t.Fatalf("invalid usage claimed known: %#v, %v", result, err)
			}
		})
	}
}

func TestFitSingleEvidenceToSerializedLimit(t *testing.T) {
	for _, text := range []string{strings.Repeat("中", 512), strings.Repeat("\"\\\n<>&中", 10000)} {
		config := reportSnapshot{Model: Model{Name: "fixture"}, SystemPrompt: reportSystemPrompt, MaxInputTokens: 256, MaxOutputTokens: 64}
		input := modelInput{SchemaVersion: SchemaVersion, Limitations: []string{}, Evidence: []modelEvidence{{ID: "e-1", Kind: "transcript", StartMS: 0, EndMS: 1000, TextContent: text, TextSHA256: digestBytes([]byte(text))}}}
		fitted, err := fitModelInputToRequest(input, config, 0)
		if err != nil {
			t.Fatal(err)
		}
		body, err := buildReportRequest(fitted, config)
		if err != nil || !json.Valid(body) || len(body) > config.MaxInputTokens*8 {
			t.Fatalf("invalid bounded body: %d, %v", len(body), err)
		}
		e := fitted.Evidence[0]
		if e.TextContent == "" || !utf8.ValidString(e.TextContent) || len(e.TextContent) >= len(text) || e.TextSHA256 != digestBytes([]byte(e.TextContent)) || input.Evidence[0].TextContent != text {
			t.Fatalf("invalid excerpt or source mutation: %#v", e)
		}
		if !strings.Contains(strings.Join(fitted.Limitations, ","), "MODEL_INPUT_TRUNCATED:e-1") {
			t.Fatal("missing disclosure")
		}
		config.MaxInputTokens = 1
		if _, err = fitModelInputToRequest(input, config, 0); err == nil {
			t.Fatal("impossible packaging must fail")
		}
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
