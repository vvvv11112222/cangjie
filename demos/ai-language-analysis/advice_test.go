package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestGroundedAdviceSelectionAndPublicCompatibility(t *testing.T) {
	segments, events, report := coachingFixture()
	d := draftFixture(report, events.Events)
	for _, selection := range []*int{ptrInt(-1), ptrInt(2)} {
		d.NextStep = selection
		if _, err := bindReportDraft(d, events.Events, segments); err == nil {
			t.Fatal("invalid action selection accepted")
		}
	}
	d.NextStep = ptrInt(1)
	raw, _ := json.Marshal(d)
	var wire map[string]any
	_ = json.Unmarshal(raw, &wire)
	for _, field := range []string{"suggestion", "verification", "advice_key"} {
		wire["observations"].([]any)[0].(map[string]any)[field] = "谷氨酸替换后序列不变"
		b, _ := json.Marshal(wire)
		if _, err := decodeReportDraft(string(b)); err == nil {
			t.Fatal("freeform subject answer accepted", field)
		}
		delete(wire["observations"].([]any)[0].(map[string]any), field)
	}
	before := cloneValue(d)
	bound, err := bindReportDraft(d, events.Events, segments)
	if err != nil || bound.Observations[0].Suggestion != d.Observations[0].Coaching.Suggestion || !reflect.DeepEqual(before, d) {
		t.Fatal("lesson-specific advice changed or input mutated", err)
	}
	*d.NextStep = 0
	if bound, err = bindReportDraft(d, events.Events, segments); err != nil || bound.Observations[0].Suggestion != d.Observations[0].Coaching.Suggestion {
		t.Fatal("legacy hint incorrectly discarded locally anchored advice", err)
	}
	d.Observations[0].Coaching = nil
	bound, err = bindReportDraft(d, events.Events, segments)
	if err != nil || bound.Observations[0].Suggestion != "" || bound.Observations[0].Verification != "" {
		t.Fatal("no-advice choice invented guidance")
	}
	d.Observations[0].Type = "issue"
	if _, err = bindReportDraft(d, events.Events, segments); err == nil {
		t.Fatal("goal announcement turned into an issue without positive problem evidence")
	}
	d = draftFixture(report, events.Events)
	d.Observations = append(d.Observations, d.Observations[0])
	d.Observations[1].Coaching = nil
	bound, err = bindReportDraft(d, events.Events, segments)
	if err != nil || len(bound.Observations) != 2 || bound.Observations[0].Suggestion == "" || bound.Observations[1].Suggestion != "" {
		t.Fatal("single selection did not preserve both findings with only one optional action", err)
	}
	if !strings.Contains(allowedAdvice("transition")[0], "none") || len(allowedAdvice("transition")) != 1 {
		t.Fatal("ordinary transition generated compulsory advice")
	}
}

func TestAdviceLivesInEachFindingWithoutCrossFieldSelection(t *testing.T) {
	segments, events, report := coachingFixture()
	draft := draftFixture(report, events.Events)
	draft.NextStep = nil
	for i := 1; i < 3; i++ {
		draft.Observations = append(draft.Observations, cloneValue(draft.Observations[0]))
		draft.Observations[i].Coaching.Suggestion += strings.Repeat("。", i)
	}
	before := cloneValue(draft)
	bound, err := bindReportDraft(draft, events.Events, segments)
	if err != nil || len(bound.Observations) != 3 || !reflect.DeepEqual(before, draft) {
		t.Fatal("multiple independently bound suggestions rejected or mutated", err)
	}
	for i, observation := range bound.Observations {
		if observation.Suggestion != draft.Observations[i].Coaching.Suggestion || observation.Verification != draft.Observations[i].Coaching.Verification {
			t.Fatal("a suggestion or its check was truncated", i)
		}
	}
	if err = validateReport(bound, segments); err != nil {
		t.Fatal(err)
	}
	if err = validateEventReport(bound, events.Events, segments); err != nil {
		t.Fatal(err)
	}
	// The screenshot arose with a legacy hint of 1 and advice in 2 and 3.
	draft.NextStep = ptrInt(1)
	if _, err = bindReportDraft(draft, events.Events, segments); err != nil {
		t.Fatal("recorded selection pattern still fails", err)
	}
	for _, change := range []func(*CoachingDraft){
		func(c *CoachingDraft) { c.Quotes[0].Text = "无来源的建议原话" },
		func(c *CoachingDraft) { c.Verification = "" },
		func(c *CoachingDraft) { c.Suggestion = "" },
	} {
		bad := cloneValue(draft)
		change(bad.Observations[2].Coaching)
		_, err = bindReportDraft(bad, events.Events, segments)
		if err == nil || !strings.HasPrefix(reportBindingMessage(err), "六维报告建议校验失败：") {
			t.Fatal("later advice bypassed checks or used a misleading failure category", err)
		}
	}
	// Advice is optional for observations but remains mandatory for issues.
	draft.NextStep = nil
	for i := range draft.Observations {
		draft.Observations[i].Coaching = nil
	}
	if _, err = bindReportDraft(draft, events.Events, segments); err != nil {
		t.Fatal("observation without advice became compulsory", err)
	}
	if !strings.HasPrefix(reportBindingMessage(&EvidenceLinkError{Message: "课堂依据不存在"}), "六维报告证据链校验失败：") {
		t.Fatal("actual evidence failure lost its category")
	}
}

func TestGroundedAdviceQuotesCannotBorrowOtherEvidence(t *testing.T) {
	segments, events, report := coachingFixture()
	draft := draftFixture(report, events.Events)
	// Real words in the same block but outside the selected event are also
	// insufficient: a block ID alone cannot anchor the proposed activity.
	segments[0].Text += "另一件事。"
	segments = append(segments, Segment{ID: "other", Start: 10000, End: 20000, Text: "另一处真实原话。"})
	for name, change := range map[string]func(*CoachingDraft){
		"missing":       func(c *CoachingDraft) { c.Quotes = nil },
		"invented":      func(c *CoachingDraft) { c.Quotes[0].Text = "不存在的原话" },
		"outside_event": func(c *CoachingDraft) { c.Quotes[0].Text = "另一件事。" },
		"other_source":  func(c *CoachingDraft) { c.Quotes[0] = AdviceQuote{SegmentID: "other", Text: segments[1].Text} },
		"duplicate":     func(c *CoachingDraft) { c.Quotes = append(c.Quotes, c.Quotes[0]) },
		"no_action":     func(c *CoachingDraft) { c.Suggestion = " " },
		"no_check":      func(c *CoachingDraft) { c.Verification = "" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := cloneValue(draft)
			change(bad.Observations[0].Coaching)
			before := cloneValue(bad)
			if _, err := bindReportDraft(bad, events.Events, segments); err == nil || !reflect.DeepEqual(before, bad) {
				t.Fatal("unanchored/incomplete advice accepted or candidate changed", err)
			}
		})
	}
	bound, err := bindReportDraft(draft, events.Events, segments)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(bound)
	if strings.Contains(string(raw), "focus_quotes") || strings.Contains(string(raw), "coaching\"") {
		t.Fatal("generation-only coaching anchors leaked into public report")
	}
	if err = validateReport(bound, segments); err != nil {
		t.Fatal(err)
	}
}

func TestAdviceQuoteCanSpanAdjacentQuotesFromTheSameEvent(t *testing.T) {
	segment := Segment{ID: "e-0008", Text: "First sentence. Second sentence. Third sentence."}
	quotes := evidenceCatalog([]Segment{segment})
	if len(quotes) != 3 {
		t.Fatalf("fixture should produce three adjacent source quotes, got %d", len(quotes))
	}
	event := TeachingEvent{ID: "ev-egg", Type: "example", Quotes: cloneValue(quotes)}
	basis := ReportBasis{Evidence: event}
	draft := &CoachingDraft{
		Quotes:       []AdviceQuote{{SegmentID: segment.ID, Text: segment.Text}},
		Suggestion:   "讲完这个例子后，请学生解释这三句所表达的变化。",
		Verification: "观察回答是否能对应到例子里的具体原话，而非只复述术语。",
	}
	if _, _, err := bindCoaching(draft, basis, []Segment{segment}); err != nil {
		t.Fatalf("exact excerpt spanning the selected event's complete adjacent quotes was rejected: %v", err)
	}

	// A real source excerpt is still rejected when the selected event omitted
	// an intervening sentence; exact transcript matching alone is insufficient.
	basis.Evidence.Quotes = []EventQuote{quotes[0], quotes[2]}
	if _, _, err := bindCoaching(draft, basis, []Segment{segment}); err == nil {
		t.Fatal("excerpt borrowing an unselected middle sentence was accepted")
	}
	draft.Quotes[0].Text = "Second sentence."
	if _, _, err := bindCoaching(draft, basis, []Segment{segment}); err == nil {
		t.Fatal("exact transcript text from a different same-segment event was accepted")
	}
	basis.Evidence.Quotes = []EventQuote{quotes[0], quotes[2]}
	draft.Quotes[0].Text = "First sentence. Third sentence."
	if _, _, err := bindCoaching(draft, basis, []Segment{segment}); err == nil {
		t.Fatal("excerpt containing non-contiguous evidence was accepted")
	}
}
func ptrInt(n int) *int { return &n }

func TestAuditRejectsTheRecordedMisreadingAndInventedSourceQuote(t *testing.T) {
	segments, _, report := coachingFixture()
	report.Dimensions[0].Limitation = "转写未区分师生发言，仅能确认提问行为存在。"
	f := anchoredFinding(report, segments, "dimension:"+report.Dimensions[0].Code, []string{"limitation"})
	if err := validateAuditFinding(f, report, segments); err != nil {
		t.Fatal(err)
	}
	f.ReportQuotes[0].Text = "无法确认提问行为存在"
	if validateAuditFinding(f, report, segments) == nil {
		t.Fatal("recorded model misreading accepted as report quotation")
	}
	f = anchoredFinding(report, segments, "summary", []string{"summary"})
	f.SourceQuotes[0].Text = "教师已经讲解了这里没有的内容"
	if validateAuditFinding(f, report, segments) == nil {
		t.Fatal("invented source quote accepted")
	}
	f = anchoredFinding(report, segments, "observation:1", []string{"analysis", "suggestion"})
	f.ReportQuotes = f.ReportQuotes[:1]
	if validateAuditFinding(f, report, segments) == nil {
		t.Fatal("unquoted second field accepted")
	}
}
