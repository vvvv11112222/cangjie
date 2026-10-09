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
	return json.Marshal(payload)
}

func (s *Service) callReportModel(ctx context.Context, body []byte) (modelCallResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.ReportAPIBase, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return modelCallResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.ReportAPIKey)
	client := &http.Client{Timeout: s.cfg.ReportTimeout}
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
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     *int64 `json:"prompt_tokens"`
			CompletionTokens *int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Choices) != 1 {
		return modelCallResult{}, fmt.Errorf("report provider response is invalid")
	}
	var candidate ReportCandidate
	if err := decodeStrict([]byte(envelope.Choices[0].Message.Content), &candidate); err != nil {
		return modelCallResult{}, fmt.Errorf("report candidate is invalid: %w", err)
	}
	result := modelCallResult{Candidate: candidate, ProviderRequestID: envelope.ID}
	if envelope.Usage.PromptTokens != nil && envelope.Usage.CompletionTokens != nil && *envelope.Usage.PromptTokens >= 0 && *envelope.Usage.CompletionTokens >= 0 {
		result.InputTokens = *envelope.Usage.PromptTokens
		result.OutputTokens = *envelope.Usage.CompletionTokens
		result.UsageKnown = true
	}
	return result, nil
}

func mustJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }
