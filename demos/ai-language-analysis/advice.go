package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const adviceProtocol = "grounded-advice-v7"

// Advice structure errors differ from missing or mismatched evidence links.
type coachingContractError struct{ message string }

func (e *coachingContractError) Error() string { return e.message }

func reportBindingMessage(err error) string {
	var coachingError *coachingContractError
	if errors.As(err, &coachingError) {
		return "六维报告建议校验失败：" + err.Error()
	}
	return "六维报告证据链校验失败：" + err.Error()
}

// Methods guide the activity, not its wording. A candidate must supply a
// lesson-specific action and check anchored to the selected event's words.
type AdviceMethod struct {
	Key  string `json:"advice_key"`
	Name string `json:"name"`
	Goal string `json:"activity_goal"`
}

func adviceMethods() []AdviceMethod {
	return []AdviceMethod{
		{"recap_goal", "联系目标与实例", "围绕本段实际目标，请学生解释已讲概念或例子与目标的联系"},
		{"explain_term", "用本课内容解释概念", "围绕选中原话的术语，请学生用已讲例子解释其含义或条件"},
		{"explain_example", "解释本课例子", "围绕选中原话的具体例子，请学生解释它与概念的对应关系，保留个例的适用范围"},
		{"trace_reasoning", "补说本段推理", "围绕选中原话的推理，请学生连接起点、步骤、结论及已说明的条件"},
		{"answer_question", "追问本课问题的理由", "沿用选中原话的实际问题，选取一个已讲对象请学生解释理由，不重新罗列已答内容"},
		{"clarify_task", "明确本次活动要求", "围绕选中原话的活动，请学生复述要做的事或产出，教师据此澄清要求"},
	}
}

type AdviceQuote struct {
	SegmentID string `json:"segment_id"`
	Text      string `json:"text"`
}

// Generation-only anchors; public/history reports keep the existing fields.
type CoachingDraft struct {
	Quotes       []AdviceQuote `json:"focus_quotes"`
	Suggestion   string        `json:"suggestion"`
	Verification string        `json:"verification"`
}

func allowedAdvice(kind string) []string {
	key := map[string]string{"goal": "recap_goal", "definition": "explain_term", "term_explanation": "explain_term", "example": "explain_example", "reasoning": "trace_reasoning", "question": "answer_question", "organization": "clarify_task"}[kind]
	if key == "" {
		return []string{"none"}
	}
	return []string{"none", key}
}

// eventCoversExactQuote accepts an exact transcript excerpt when its full
// content is covered by one or more quotes from the same selected event.
// Whitespace between adjacent quote catalog entries is harmless; uncovered
// words, punctuation, or text from a different event remain disallowed.
func eventCoversExactQuote(event TeachingEvent, segmentID, source, excerpt string) bool {
	type span struct{ start, end int }
	var allowed []span
	for _, quote := range event.Quotes {
		if quote.SegmentID != segmentID || strings.TrimSpace(quote.Text) == "" {
			continue
		}
		for offset := 0; offset < len(source); {
			i := strings.Index(source[offset:], quote.Text)
			if i < 0 {
				break
			}
			start := offset + i
			allowed = append(allowed, span{start: start, end: start + len(quote.Text)})
			offset = start + 1
		}
	}
	if len(allowed) == 0 {
		return false
	}
	for offset := 0; offset < len(source); {
		i := strings.Index(source[offset:], excerpt)
		if i < 0 {
			return false
		}
		start, end := offset+i, offset+i+len(excerpt)
		cursor, covered := start, true
		for cursor < end {
			furthest := cursor
			for _, item := range allowed {
				if item.start <= cursor && item.end > furthest {
					furthest = item.end
				}
			}
			if furthest > cursor {
				cursor = furthest
				if cursor > end {
					cursor = end
				}
				continue
			}
			r, size := utf8.DecodeRuneInString(source[cursor:end])
			if r == utf8.RuneError && size == 1 || !unicode.IsSpace(r) {
				covered = false
				break
			}
			cursor += size
		}
		if covered {
			return true
		}
		offset = start + 1
	}
	return false
}

func bindCoaching(draft *CoachingDraft, basis ReportBasis, segments []Segment) (string, string, error) {
	invalid := func(reason string) (string, string, error) {
		return "", "", &coachingContractError{fmt.Sprintf("修改方向%s；候选已保留，无需重新转写", reason)}
	}
	if len(allowedAdvice(basis.Evidence.Type)) < 2 {
		return invalid("缺少适用的教学动作")
	}
	if draft == nil || strings.TrimSpace(draft.Suggestion) == "" || strings.TrimSpace(draft.Verification) == "" {
		return invalid("缺少具体操作或实施后的观察方法")
	}
	if len(draft.Quotes) == 0 {
		return invalid("未引用本条课堂依据")
	}
	sources := map[string]string{}
	for _, s := range segments {
		sources[s.ID] = s.Text
	}
	seen := map[AdviceQuote]bool{}
	for _, q := range draft.Quotes {
		if strings.TrimSpace(q.Text) == "" || seen[q] || !strings.Contains(sources[q.SegmentID], q.Text) {
			return invalid("的引文为空、重复或不符合原文")
		}
		if !eventCoversExactQuote(basis.Evidence, q.SegmentID, sources[q.SegmentID], q.Text) {
			return invalid("引文未被本条课堂依据完整覆盖；跨句引用请逐句列出，并确认所选依据包含每句原文")
		}
		seen[q] = true
	}
	// Anchoring proves sources, not pedagogical or subject correctness. The
	// full-context model audit and human review still check the prose.
	return draft.Suggestion, draft.Verification, nil
}
