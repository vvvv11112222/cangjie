package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ReadingEdit struct {
	ID           string `json:"id"`
	Text         string `json:"text"`
	ParagraphEnd bool   `json:"paragraph_end"`
}
type ReadingParagraph struct {
	Text string   `json:"text"`
	IDs  []string `json:"source_ids"`
}
type ReadingResult struct {
	Status     string             `json:"status"`
	Message    string             `json:"message"`
	ErrorCode  string             `json:"error_code,omitempty"`
	Boundaries []BoundaryDecision `json:"boundaries"`
	Edits      []ReadingEdit      `json:"edits"`
	Paragraphs []ReadingParagraph `json:"paragraphs"`
	Request    ModelMeta          `json:"request"`
}

type BoundaryDecision struct {
	LeftID         string `json:"left_id"`
	RightID        string `json:"right_id"`
	JoinSentence   bool   `json:"join_sentence"`
	LowercaseNext  bool   `json:"lowercase_next"`
	ParagraphBreak bool   `json:"paragraph_break"`
	Source         string `json:"source,omitempty"`
}

// Protect word boundaries, numbers, mathematical symbols and internal punctuation.
// Han characters are individual units so Chinese sentence punctuation may move.
var readingTokens = regexp.MustCompile(`[0-9]+(?:[.,:][0-9]+)+|\.[0-9]+|[\p{Han}]|[\pL\pM\pN]+(?:[.'’:/+\-][\pL\pM\pN]+)*|[^\s.,!?;:。，！？；：]`)
var hanCharacter = regexp.MustCompile(`[\p{Han}]`)

func lexicalTokens(s string) []string {
	return readingTokens.FindAllString(hanCharacter.ReplaceAllString(s, " $0 "), -1)
}
func sameReadingWords(source, edited string) bool {
	a, b := lexicalTokens(source), lexicalTokens(edited)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		// Only a normal word at the segment start may change sentence-initial case.
		// Single-letter variables and all-capital abbreviations remain exact.
		ar, br := []rune(a[i]), []rune(b[i])
		if i == 0 && len(ar) > 1 && len(br) > 1 && a[i] != strings.ToUpper(a[i]) && unicode.ToLower(ar[0]) == unicode.ToLower(br[0]) && string(ar[1:]) == string(br[1:]) {
			continue
		}
		return false
	}
	return true
}

func parseReadingDecisions(raw string, segments []Segment) ([]BoundaryDecision, error) {
	var payload struct {
		Boundaries []BoundaryDecision `json:"boundaries"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		return nil, err
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("extra JSON")
	}
	if len(payload.Boundaries) != max(0, len(segments)-1) {
		return nil, fmt.Errorf("boundary count changed")
	}
	for i := range payload.Boundaries {
		b := &payload.Boundaries[i]
		if b.LeftID != segments[i].ID || b.RightID != segments[i+1].ID || (b.JoinSentence && b.ParagraphBreak) || (!b.JoinSentence && b.LowercaseNext) {
			return nil, fmt.Errorf("invalid boundary at %d", i+1)
		}
		b.Source = "model"
	}
	return payload.Boundaries, nil
}

var incompleteTail = regexp.MustCompile(`\b(a|(?i:an|the|of|to|with|for|and|or))\.$`)
var genericLead = regexp.MustCompile(`^(Rectangular|Exercises)\b`)
var leadingEnglish = regexp.MustCompile(`^(\s*)([A-Z][a-z]+)\b`)
var internalAbbreviation = regexp.MustCompile(`(?i)(?:[a-z]\.){2,}$`)
var finalAbbreviation = regexp.MustCompile(`(?i:\b(mr|mrs|ms|dr|prof|st|jr|sr))\.$|\b[A-Z]\.$`)
var trailingPunctuation = regexp.MustCompile(`[.。]$`)

func obviousJoin(left, right string) bool {
	return incompleteTail.MatchString(strings.TrimSpace(left)) && strings.TrimSpace(right) != ""
}

func conservativeBoundaries(segments []Segment) []BoundaryDecision {
	out := make([]BoundaryDecision, 0, max(0, len(segments)-1))
	for i := 0; i+1 < len(segments); i++ {
		join := obviousJoin(segments[i].Text, segments[i+1].Text)
		out = append(out, BoundaryDecision{LeftID: segments[i].ID, RightID: segments[i+1].ID, JoinSentence: join, LowercaseNext: join && genericLead.MatchString(strings.TrimSpace(segments[i+1].Text)), Source: "conservative_rule"})
	}
	return out
}

func trimFalseSentenceEnd(s string) string {
	trimmed := strings.TrimRightFunc(s, unicode.IsSpace)
	if strings.HasSuffix(trimmed, "...") || internalAbbreviation.MatchString(trimmed) || finalAbbreviation.MatchString(trimmed) {
		return trimmed
	}
	return trailingPunctuation.ReplaceAllString(trimmed, "")
}

func lowerInitialWord(s string) string {
	parts := leadingEnglish.FindStringSubmatchIndex(s)
	if parts == nil {
		return s
	}
	start := parts[4]
	r, size := utf8.DecodeRuneInString(s[start:])
	return s[:start] + string(unicode.ToLower(r)) + s[start+size:]
}

func readingSeparator(left, right string) string {
	lr, _ := utf8.DecodeLastRuneInString(strings.TrimSpace(left))
	rr, _ := utf8.DecodeRuneInString(strings.TrimSpace(right))
	if unicode.Is(unicode.Han, lr) && unicode.Is(unicode.Han, rr) {
		return ""
	}
	return " "
}

func buildReading(segments []Segment, boundaries []BoundaryDecision, status, code string, meta ModelMeta) ReadingResult {
	message := "已按上下文整理跨段句子；字词保留原样。段落和标点仍需人工复核，回看按钮对应原始音频块。"
	if status != "ready" {
		message = "文本模型整理未成功（" + code + "）；已用保守规则连接明显残句，其余保持原文。仍可查看原始分段。"
	}
	result := ReadingResult{Status: status, Message: message, ErrorCode: code, Boundaries: boundaries, Edits: make([]ReadingEdit, len(segments)), Paragraphs: []ReadingParagraph{}, Request: meta}
	for i, s := range segments {
		result.Edits[i] = ReadingEdit{ID: s.ID, Text: s.Text}
	}
	for i, b := range boundaries {
		if !b.JoinSentence {
			continue
		}
		result.Edits[i].Text = trimFalseSentenceEnd(result.Edits[i].Text)
		if b.LowercaseNext {
			result.Edits[i+1].Text = lowerInitialWord(result.Edits[i+1].Text)
		}
	}
	paragraph := ReadingParagraph{IDs: []string{}}
	for i, edit := range result.Edits {
		if paragraph.Text != "" {
			paragraph.Text += readingSeparator(paragraph.Text, edit.Text)
		}
		paragraph.Text += strings.TrimSpace(edit.Text)
		paragraph.IDs = append(paragraph.IDs, edit.ID)
		last := i == len(result.Edits)-1
		end := last
		if !last && !boundaries[i].JoinSentence && (boundaries[i].ParagraphBreak || utf8.RuneCountInString(paragraph.Text) >= 900) {
			end = true
		}
		result.Edits[i].ParagraphEnd = end
		if end {
			result.Paragraphs = append(result.Paragraphs, paragraph)
			paragraph = ReadingParagraph{IDs: []string{}}
		}
	}
	return result
}

func organizeReadingWindow(ctx context.Context, base, key string, segments []Segment) ReadingResult {
	prompt, _ := assets.ReadFile("reading_prompt.txt")
	raw, meta, err := textTask(ctx, base, key, string(prompt), segments)
	decisions := []BoundaryDecision{}
	if err == nil {
		decisions, err = parseReadingDecisions(raw, segments)
	}
	status, code := "ready", ""
	if err != nil {
		status, code = "conservative", "invalid_model_output"
		if strings.Contains(err.Error(), "HTTP 403") {
			code = "model_http_403"
		}
		if strings.Contains(err.Error(), "incomplete") {
			code = "model_stream_incomplete"
		}
		decisions = conservativeBoundaries(segments)
	} else {
		for i := range decisions {
			if !decisions[i].JoinSentence && obviousJoin(segments[i].Text, segments[i+1].Text) {
				decisions[i].JoinSentence = true
				decisions[i].LowercaseNext = genericLead.MatchString(strings.TrimSpace(segments[i+1].Text))
				decisions[i].ParagraphBreak = false
				decisions[i].Source = "conservative_rule"
			}
		}
	}
	return buildReading(segments, decisions, status, code, meta)
}
