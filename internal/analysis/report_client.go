package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type modelEvidence struct {
	ContextOnly bool `json:"context_only,omitempty"`
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	StartMS     int64  `json:"start_ms"`
	EndMS       int64  `json:"end_ms"`
	TextContent string `json:"text_content"`
	TextSHA256  string `json:"text_sha256"`
}

type modelInput struct {
	SchemaVersion        string            `json:"schema_version"`
	RunID                string            `json:"run_id"`
	SessionID            string            `json:"session_id"`
	MediaAssetID         string            `json:"media_asset_id"`
	InputSHA256          string            `json:"input_sha256"`
	TranscriptRevisionID *string           `json:"transcript_revision_id"`
	Model                Model             `json:"model"`
	PromptVersion        string            `json:"prompt_version"`
	PromptSHA256         string            `json:"prompt_sha256"`
	SelectionVersion     string            `json:"selection_version"`
	CourseContext        map[string]string `json:"course_context"`
	Coverage             []Interval        `json:"coverage"`
	Limitations          []string          `json:"limitations"`
	Evidence             []modelEvidence   `json:"evidence"`
}

const reportSystemPrompt = "Return one strict JSON ReportCandidate. Use only supplied evidence IDs. Include exactly the six dimension codes content, pace, thinking, expression, management, technology. Unsupported dimensions must be insufficient with a non-empty limitation and no factual summary."

type modelCallResult struct {
	Candidate         ReportCandidate
	ProviderRequestID string
	InputTokens       int64
	OutputTokens      int64
	BilledSeconds     int64 // ASR duration billing only; never substitutes for tokens.
	TokensKnown       bool  // Duration-billed ASR can omit token usage.
	UsageKnown        bool
}

func buildReportRequest(input modelInput, config reportSnapshot) ([]byte, error) {
	payload := map[string]any{
		"model":           config.Model.Name,
		"temperature":     0,
		"max_tokens":      config.MaxOutputTokens,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": config.SystemPrompt},
			{"role": "user", "content": mustJSON(input)},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	maxBytes := int64(config.MaxInputTokens) * 8
	if config.MaxInputTokens <= 0 || int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("complete report prompt exceeds configured input limit")
	}
	return body, nil
}

func (s *Service) callReportModel(ctx context.Context, body []byte) (modelCallResult, error) {
	cfg := s.ModelConfig()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.ReportAPIBase, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return modelCallResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.ReportAPIKey)
	// A redirected paid POST must not be replayed to a different endpoint.
	client := &http.Client{Timeout: s.cfg.ReportTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return modelCallResult{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return modelCallResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return modelCallResult{}, fmt.Errorf("report provider returned status %d", resp.StatusCode)
	}
	var envelope struct {
		ID      string          `json:"id"`
		Choices json.RawMessage `json:"choices"`
		Usage   json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return modelCallResult{}, fmt.Errorf("report provider response is invalid")
	}
	result := modelCallResult{ProviderRequestID: envelope.ID}
	var usage struct {
		PromptTokens     *int64 `json:"prompt_tokens"`
		CompletionTokens *int64 `json:"completion_tokens"`
	}
	if json.Unmarshal(envelope.Usage, &usage) == nil && usage.PromptTokens != nil && usage.CompletionTokens != nil && *usage.PromptTokens >= 0 && *usage.CompletionTokens >= 0 {
		result.InputTokens = *usage.PromptTokens
		result.OutputTokens = *usage.CompletionTokens
		result.UsageKnown = true
	}
	var choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(envelope.Choices, &choices); err != nil || len(choices) != 1 {
		return result, fmt.Errorf("report provider choices are invalid")
	}
	if err := decodeStrict([]byte(choices[0].Message.Content), &result.Candidate); err != nil {
		return result, fmt.Errorf("report candidate is invalid: %w", err)
	}
	return result, nil
}

func mustJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }
