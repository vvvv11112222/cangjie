package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Segment struct {
	ID         string `json:"id"`
	ChunkIndex int    `json:"chunk_index,omitempty"`
	Start      int64  `json:"start_ms"`
	End        int64  `json:"end_ms"`
	Text       string `json:"text"`
	Boundary   string `json:"boundary_reason"`
}
type Dimension struct {
	Code       string   `json:"dimension_code"`
	Status     string   `json:"coverage_status"`
	Summary    string   `json:"summary"`
	Limitation string   `json:"limitation"`
	IDs        []string `json:"summary_evidence_ids"`
}
type Observation struct {
	// Kept for history compatibility; v5 records the activity method here,
	// while suggestion/verification are generated for the selected lesson.
	AdviceTemplate string   `json:"advice_template,omitempty"`
	Title          string   `json:"title,omitempty"`
	Analysis       string   `json:"analysis,omitempty"`
	Verification   string   `json:"verification,omitempty"`
	EventIDs       []string `json:"event_ids"`
	Code           string   `json:"dimension_code"`
	Type           string   `json:"observation_type"`
	Text           string   `json:"observation_text"`
	Suggestion     string   `json:"suggestion"`
	IDs            []string `json:"evidence_ids"`
}
type Report struct {
	Audit           *ReportAudit  `json:"quality_audit,omitempty"`
	AnalysisVersion string        `json:"analysis_version,omitempty"`
	Summary         string        `json:"summary"`
	IDs             []string      `json:"summary_evidence_ids"`
	Dimensions      []Dimension   `json:"dimensions"`
	Observations    []Observation `json:"observations"`
}

var codes = []string{"content", "pace", "thinking", "expression", "management", "technology"}

func validateReport(r Report, segments []Segment) error {
	if r.AnalysisVersion != "" && r.AnalysisVersion != analysisVersion {
		return errors.New("分析报告版本不支持")
	}
	known := map[string]bool{}
	for _, s := range segments {
		known[s.ID] = true
	}
	refs := func(ids []string, required bool) error {
		if ids == nil || (required && len(ids) == 0) {
			return errors.New("missing evidence references")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !known[id] || seen[id] {
				return fmt.Errorf("invalid evidence reference %q", id)
			}
			seen[id] = true
		}
		return nil
	}
	summary := func(text string, ids []string) error {
		if strings.TrimSpace(text) == "" && len(ids) > 0 {
			return errors.New("empty summary with citations")
		}
		return refs(ids, strings.TrimSpace(text) != "")
	}
	if err := summary(r.Summary, r.IDs); err != nil {
		return err
	}
	if len(r.Dimensions) != 6 || r.Observations == nil {
		return errors.New("expected six dimensions and observations array")
	}
	seen := map[string]bool{}
	valid := map[string]bool{}
	for _, c := range codes {
		valid[c] = true
	}
	for _, d := range r.Dimensions {
		if !valid[d.Code] || seen[d.Code] {
			return errors.New("invalid or duplicate dimension")
		}
		seen[d.Code] = true
		if d.Status != "observed" && d.Status != "insufficient" && d.Status != "not_applicable" {
			return errors.New("invalid coverage status")
		}
		if d.Status != "observed" && strings.TrimSpace(d.Limitation) == "" {
			return errors.New("missing limitation")
		}
		if d.Status != "observed" && (strings.TrimSpace(d.Summary) != "" || len(d.IDs) > 0) {
			return errors.New("insufficient dimension cannot claim evidence")
		}
		if d.Status == "observed" && strings.TrimSpace(d.Summary) == "" {
			return errors.New("observed dimension requires summary")
		}
		if err := summary(d.Summary, d.IDs); err != nil {
			return err
		}
	}
	for _, o := range r.Observations {
		if !valid[o.Code] || strings.TrimSpace(o.Text) == "" || (o.Type != "highlight" && o.Type != "issue" && o.Type != "observation") {
			return errors.New("invalid observation")
		}
		// Legacy reports remain readable. New coaching reports must explain
		// their interpretation, and actionable advice must be checkable.
		if r.AnalysisVersion == analysisVersion {
			if strings.TrimSpace(o.Title) == "" || strings.TrimSpace(o.Analysis) == "" {
				return errors.New("新版分析缺少发现标题或教学意义说明")
			}
			if o.Type == "issue" && strings.TrimSpace(o.Suggestion) == "" {
				return errors.New("新版分析的改进点缺少可执行建议")
			}
			if strings.TrimSpace(o.Suggestion) != "" && strings.TrimSpace(o.Verification) == "" {
				return errors.New("新版分析的建议缺少检查方法")
			}
		}
		if err := refs(o.IDs, true); err != nil {
			return err
		}
	}
	return nil
}

type Completion struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Finish string `json:"finish_reason"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
	ID    string          `json:"id"`
	Error json.RawMessage `json:"error"`
}
type ModelMeta struct {
	ID    string          `json:"request_id"`
	Usage json.RawMessage `json:"usage"`
}

// Each request is dispatched once. A timeout does not imply the provider did not bill it.
func callModel(ctx context.Context, base, key string, body any, stream bool) (string, ModelMeta, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "", ModelMeta{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", ModelMeta{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return "", ModelMeta{}, fmt.Errorf("model request failed; billing may be unknown: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", ModelMeta{}, fmt.Errorf("model HTTP %d; request-id=%s (check model, region and quota)", res.StatusCode, res.Header.Get("x-request-id"))
	}
	if stream {
		return readStream(io.LimitReader(res.Body, 4<<20))
	}
	var c Completion
	if err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&c); err != nil {
		return "", ModelMeta{}, err
	}
	if len(c.Choices) != 1 || c.Choices[0].Finish != "stop" {
		return "", ModelMeta{}, errors.New("ASR completion missing or truncated")
	}
	return c.Choices[0].Message.Content, ModelMeta{c.ID, c.Usage}, nil
}
func readStream(r io.Reader) (string, ModelMeta, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var out strings.Builder
	var meta ModelMeta
	stopped := false
	done := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			break
		}
		var c Completion
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			return "", meta, err
		}
		if len(c.Error) > 0 && string(c.Error) != "null" {
			return "", meta, errors.New("provider returned streaming error")
		}
		if c.ID != "" {
			meta.ID = c.ID
		}
		if len(c.Usage) > 0 && string(c.Usage) != "null" {
			meta.Usage = c.Usage
		}
		for _, choice := range c.Choices {
			out.WriteString(choice.Delta.Content)
			if choice.Finish == "stop" {
				stopped = true
			} else if choice.Finish != "" {
				return "", meta, fmt.Errorf("incomplete generation: %s", choice.Finish)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", meta, err
	}
	if !stopped || !done {
		return "", meta, errors.New("incomplete SSE stream")
	}
	return out.String(), meta, nil
}
