package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
)

const rubricVersion = "six-dim-text-v1"

type EventQuote struct {
	QuoteID   string `json:"quote_id,omitempty"`
	SegmentID string `json:"segment_id"`
	Text      string `json:"text"`
}

// The model selects source IDs; the server owns the exact evidence text.
// Sentence spans stay within their original audio block and retain punctuation.
func evidenceCatalog(segments []Segment) []EventQuote {
	var catalog []EventQuote
	for _, segment := range segments {
		start, count := 0, 0
		appendSpan := func(end int) {
			text := strings.TrimSpace(segment.Text[start:end])
			if text != "" {
				count++
				catalog = append(catalog, EventQuote{QuoteID: fmt.Sprintf("%s-q%02d", segment.ID, count), SegmentID: segment.ID, Text: text})
			}
			start = end
		}
		for i, r := range segment.Text {
			end := i + len(string(r))
			terminal := strings.ContainsRune("。！？!?", r)
			if r == '.' && (end == len(segment.Text) || strings.HasPrefix(segment.Text[end:], " ") || strings.HasPrefix(segment.Text[end:], "\n")) {
				terminal = true
			}
			if terminal {
				appendSpan(end)
			}
		}
		appendSpan(len(segment.Text))
	}
	return catalog
}

func resolveEventQuotes(set *EventSet, catalog []EventQuote) error {
	known := map[string]EventQuote{}
	for _, q := range catalog {
		known[q.QuoteID] = q
	}
	for i := range set.Events {
		for j := range set.Events[i].Quotes {
			q := &set.Events[i].Quotes[j]
			if q.QuoteID == "" {
				continue
			} // Validate legacy direct quotes below.
			source, ok := known[q.QuoteID]
			if !ok {
				return fmt.Errorf("事件 %s 引用了不存在的证据编号 %s", set.Events[i].ID, q.QuoteID)
			}
			if (q.SegmentID != "" && q.SegmentID != source.SegmentID) || (q.Text != "" && q.Text != source.Text) {
				return fmt.Errorf("事件 %s 的证据编号与自行填写的原文不一致", set.Events[i].ID)
			}
			*q = source
		}
	}
	return nil
}

type TeachingEvent struct {
	ID             string               `json:"id"`
	Type           string               `json:"event_type"`
	Summary        string               `json:"summary"`
	DimensionCodes []string             `json:"dimension_codes"`
	Quotes         []EventQuote         `json:"quotes"`
	Uncertainty    string               `json:"uncertainty"`
	Activity       *ActivityInstruction `json:"activity_instruction,omitempty"`
}
type EventSet struct {
	Events []TeachingEvent `json:"events"`
}

var eventDimensions = map[string][]string{
	"goal": {"content"}, "definition": {"content", "expression"},
	"example": {"content"}, "question": {"thinking"},
	"reasoning": {"thinking", "content"}, "transition": {"pace"},
	"term_explanation": {"expression", "content"}, "organization": {"management"},
	"tool_mention": {"technology"}, "resource_guidance": {"content"},
}

// Dimension membership is defined by the rubric, not another model prediction.
func assignEventDimensions(set *EventSet) error {
	for i := range set.Events {
		e := &set.Events[i]
		// Some models put a rubric dimension in event_type. Recover the
		// unambiguous cases before validation; the server still owns dimensions.
		switch e.Type {
		case "expression":
			e.Type = "term_explanation"
		case "pace":
			e.Type = "transition"
		case "management":
			e.Type = "organization"
		case "technology":
			e.Type = "tool_mention"
		case "thinking":
			e.Type = "reasoning"
		}
		allowed, ok := eventDimensions[e.Type]
		if !ok {
			return fmt.Errorf("事件 %s 的类型 %s 不在标准中", e.ID, e.Type)
		}
		e.DimensionCodes = append([]string(nil), allowed...)
	}
	return nil
}

// isolateInvalidEvidenceEvents validates model-produced events independently.
// A single incomplete event must not erase other events whose evidence is
// valid, but rejected items are retained for internal diagnostics and are
// never supplied to report generation.
func isolateInvalidEvidenceEvents(set EventSet, catalog []EventQuote, segments []Segment) (EventSet, []RejectedEvent, bool) {
	valid := EventSet{Events: make([]TeachingEvent, 0, len(set.Events))}
	rejected := make([]RejectedEvent, 0)
	seenIDs := map[string]bool{}
	fatal := false
	for i, event := range set.Events {
		candidate := EventSet{Events: []TeachingEvent{cloneValue(event)}}
		var err error
		if id := strings.TrimSpace(event.ID); id != "" && seenIDs[id] {
			err = fmt.Errorf("事件标识重复")
		} else {
			if id := strings.TrimSpace(event.ID); id != "" {
				seenIDs[id] = true
			}
			err = resolveEventQuotes(&candidate, catalog)
			if err != nil {
				// A fabricated, cross-window, or source-mismatched quote is not
				// merely an incomplete event: it invalidates this model response.
				fatal = true
			}
		}
		if err == nil {
			repairEventFormatting(&candidate, segments)
			err = assignEventDimensions(&candidate)
		}
		if err == nil {
			err = validateEvents(candidate, segments)
		}
		if err != nil {
			rejected = append(rejected, RejectedEvent{Index: i + 1, Event: cloneValue(event), Reason: err.Error()})
			continue
		}
		valid.Events = append(valid.Events, candidate.Events[0])
	}
	return valid, rejected, fatal
}

func contains(items []string, s string) bool {
	for _, x := range items {
		if x == s {
			return true
		}
	}
	return false
}
func strictJSON(raw string, target any) error {
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("多余或不完整的 JSON")
	}
	return nil
}
func validateEvents(set EventSet, segments []Segment) error {
	if set.Events == nil || len(set.Events) > 80 {
		return fmt.Errorf("事件列表为空值或超过80项")
	}
	sources := map[string]string{}
	for _, s := range segments {
		sources[s.ID] = s.Text
	}
	seen := map[string]bool{}
	for _, e := range set.Events {
		if strings.TrimSpace(e.ID) == "" || seen[e.ID] || strings.TrimSpace(e.Summary) == "" {
			return fmt.Errorf("事件标识重复或缺少说明")
		}
		seen[e.ID] = true
		allowed, ok := eventDimensions[e.Type]
		if !ok || len(e.DimensionCodes) == 0 || len(e.Quotes) == 0 {
			return fmt.Errorf("事件 %s 缺少类型、维度或原文", e.ID)
		}
		dims := map[string]bool{}
		for _, c := range e.DimensionCodes {
			if !contains(allowed, c) || dims[c] {
				return fmt.Errorf("事件 %s 的维度不符合标准", e.ID)
			}
			dims[c] = true
		}
		quotes := map[string]bool{}
		for _, q := range e.Quotes {
			text, ok := sources[q.SegmentID]
			key := q.SegmentID + "\x00" + q.Text
			if !ok {
				return fmt.Errorf("事件 %s 引用了不存在的分段 %s", e.ID, q.SegmentID)
			}
			if strings.TrimSpace(q.Text) == "" {
				return fmt.Errorf("事件 %s 在分段 %s 的引文为空", e.ID, q.SegmentID)
			}
			if !strings.Contains(text, q.Text) {
				return fmt.Errorf("事件 %s 在分段 %s 的引文与原文不一致（可能改写了文字、大小写或标点）", e.ID, q.SegmentID)
			}
			if quotes[key] {
				return fmt.Errorf("事件 %s 在分段 %s 有重复引文", e.ID, q.SegmentID)
			}
			quotes[key] = true
		}
	}
	return nil
}

// Restore whitespace formatting only, and only when the source match is unique.
// Never normalize punctuation, casing, or words: evidence must remain verbatim.
func restoreQuoteWhitespace(source, quote string) (string, bool) {
	normalize := func(s string) (string, []int, []int) {
		var b strings.Builder
		var starts, ends []int
		space := false
		for i, r := range s {
			width := len(string(r))
			if unicode.IsSpace(r) {
				if b.Len() == 0 {
					continue
				}
				if space {
					ends[len(ends)-1] = i + len(string(r))
					continue
				}
				r = ' '
				space = true
			} else {
				space = false
			}
			encoded := string(r)
			b.WriteString(encoded)
			for range []byte(encoded) {
				starts = append(starts, i)
				ends = append(ends, i+width)
			}
		}
		return strings.TrimRight(b.String(), " "), starts, ends
	}
	s, starts, ends := normalize(source)
	q, _, _ := normalize(quote)
	if q == "" {
		return "", false
	}
	i := strings.Index(s, q)
	if i < 0 || strings.Index(s[i+1:], q) >= 0 {
		return "", false
	}
	return source[starts[i]:ends[i+len(q)-1]], true
}

func repairEventFormatting(set *EventSet, segments []Segment) {
	sources := map[string]string{}
	for _, s := range segments {
		sources[s.ID] = s.Text
	}
	for i := range set.Events {
		e := &set.Events[i]
		var quotes []EventQuote
		seen := map[string]bool{}
		for _, q := range e.Quotes {
			if source, ok := sources[q.SegmentID]; ok && !strings.Contains(source, q.Text) {
				if restored, ok := restoreQuoteWhitespace(source, q.Text); ok {
					q.Text = restored
				}
			}
			key := q.SegmentID + "\x00" + q.Text
			if !seen[key] {
				quotes = append(quotes, q)
				seen[key] = true
			}
		}
		e.Quotes = quotes
	}
}
func validateEventReport(report Report, events []TeachingEvent, segments []Segment) error {
	if err := validateReport(report, segments); err != nil {
		return err
	}
	known := map[string]TeachingEvent{}
	supported := map[string]map[string]bool{}
	for _, e := range events {
		known[e.ID] = e
		for _, c := range e.DimensionCodes {
			if supported[c] == nil {
				supported[c] = map[string]bool{}
			}
			for _, q := range e.Quotes {
				supported[c][q.SegmentID] = true
			}
		}
	}
	for _, d := range report.Dimensions {
		for _, id := range d.IDs {
			if !supported[d.Code][id] {
				return fmt.Errorf("维度 %s 引用无对应教学事件", d.Code)
			}
		}
	}
	for _, id := range report.IDs {
		found := false
		for _, refs := range supported {
			if refs[id] {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("总体结论引用无对应事件")
		}
	}
	for index, o := range report.Observations {
		if len(o.EventIDs) == 0 {
			return linkError("missing_event", index, o, TeachingEvent{}, "", segments)
		}
		refs := map[string]bool{}
		seen := map[string]bool{}
		for _, id := range o.EventIDs {
			e, ok := known[id]
			if !ok {
				return linkError("unknown_event", index, o, TeachingEvent{ID: id}, "", segments)
			}
			if seen[id] {
				return linkError("duplicate_event", index, o, e, "", segments)
			}
			if !contains(e.DimensionCodes, o.Code) {
				return linkError("dimension_mismatch", index, o, e, "", segments)
			}
			seen[id] = true
			for _, q := range e.Quotes {
				refs[q.SegmentID] = true
			}
		}
		if len(refs) != len(o.IDs) {
			return linkError("incomplete_sources", index, o, TeachingEvent{}, "", segments)
		}
		for _, id := range o.IDs {
			if !refs[id] {
				return linkError("source_mismatch", index, o, TeachingEvent{}, id, segments)
			}
		}
		for _, d := range report.Dimensions {
			if d.Code == o.Code && d.Status != "observed" {
				return linkError("unobserved_dimension", index, o, TeachingEvent{}, "", segments)
			}
		}
	}
	return nil
}

// Downgrade only dimensions with no supporting events at all. Never filter
// invalid citations from a supported summary or silently drop mismatched events.
// Observation links are handled by bindObservationEvidence before validation.
func normalizeReportCoverage(report *Report, events []TeachingEvent) {
	if report == nil {
		return
	}
	supported := map[string]map[string]bool{}
	for _, e := range events {
		for _, code := range e.DimensionCodes {
			if supported[code] == nil {
				supported[code] = map[string]bool{}
			}
			for _, q := range e.Quotes {
				supported[code][q.SegmentID] = true
			}
		}
	}
	for i := range report.Dimensions {
		d := &report.Dimensions[i]
		if d.Status != "observed" {
			continue
		}
		if len(supported[d.Code]) == 0 {
			d.Status = "insufficient"
			d.Summary = ""
			d.IDs = []string{}
			if strings.TrimSpace(d.Limitation) == "" {
				d.Limitation = "未提取到可验证的教学事件，不能据此评价该维度"
			}
		}
	}
}
func textTask(ctx context.Context, base, key, prompt string, input any) (string, ModelMeta, error) {
	if err := textRequestBudget(prompt, input); err != nil {
		return "", ModelMeta{}, err
	}
	raw, _ := json.Marshal(input)
	return callModel(ctx, base, key, map[string]any{"model": env("BAILIAN_TEXT_MODEL", "qwen-plus"), "stream": true, "stream_options": map[string]bool{"include_usage": true}, "enable_thinking": false, "response_format": map[string]string{"type": "json_object"}, "messages": []map[string]string{{"role": "system", "content": prompt}, {"role": "user", "content": string(raw)}}}, true)
}
func extractEventsWindow(ctx context.Context, base, key string, segments []Segment) (EventSet, ModelMeta, error) {
	p, _ := assets.ReadFile("events_prompt.txt")
	r, _ := assets.ReadFile("rubric.md")
	catalog := evidenceCatalog(segments)
	input := map[string]any{"original_segments": segments, "evidence_catalog": catalog, "semantic_protocol": semanticProtocol}
	if scope, ok := ctx.Value(eventWindowKey{}).(eventWindowScope); ok {
		input["window_scope"] = scope
		p = append(p, []byte("\n【分段分析】本次只是长课堂的一个窗口。遵守window_scope.max_events上限；提取代表性的完整教学动作，不用局部未提到判断整堂缺失，保留原始来源编号和时间。")...)
	}
	if feedback := reportFeedback(ctx); feedback != nil {
		input["previous_validation_feedback"] = feedback
	}
	raw, meta, err := textTask(ctx, base, key, string(p)+"\n"+string(r), input)
	var set EventSet
	if err == nil {
		err = strictJSON(raw, &set)
	}
	var rejected []RejectedEvent
	fatalEvidence := false
	if err == nil && (set.Events == nil || len(set.Events) > 80) {
		err = fmt.Errorf("事件列表为空值或超过80项")
	}
	if err == nil {
		set, rejected, fatalEvidence = isolateInvalidEvidenceEvents(set, catalog, segments)
	}
	if err == nil {
		// Activity semantics are checked per event. A malformed organization
		// event must not discard valid content, reasoning, pace, expression or
		// technology evidence from the same model response.
		baseRejectedCount := len(rejected)
		valid, activityRejected := isolateInvalidActivityEvents(set)
		if len(activityRejected) > 0 {
			set = valid
			rejected = append(rejected, activityRejected...)
		}
		if len(rejected) > 0 {
			parts := make([]string, 0, len(rejected))
			for _, item := range rejected {
				parts = append(parts, item.Reason)
			}
			message := fmt.Sprintf("有%d条课堂依据未通过结构或证据校验：%s", len(rejected), strings.Join(parts, "；"))
			if baseRejectedCount == 0 && len(activityRejected) > 0 {
				message = fmt.Sprintf("有%d条活动依据未通过校验：%s", len(activityRejected), strings.Join(parts, "；"))
			}
			err = &eventContractError{
				raw: raw, err: fmt.Errorf("%s", message),
				partial: set, rejected: rejected, fatal: fatalEvidence,
			}
		}
	}
	if err != nil && raw != "" {
		if existing, ok := err.(*eventContractError); ok {
			return set, meta, existing
		}
		err = &eventContractError{raw: raw, err: err}
	}
	return set, meta, err
}
