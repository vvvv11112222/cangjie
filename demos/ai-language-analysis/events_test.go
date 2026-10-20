package main

import "testing"

func TestEventDimensionsComeFromRubric(t *testing.T) {
	for eventType, expected := range eventDimensions {
		set := EventSet{Events: []TeachingEvent{{ID: "ev1", Type: eventType, DimensionCodes: []string{"invalid", "invalid"}}}}
		if err := assignEventDimensions(&set); err != nil {
			t.Fatal(err)
		}
		if len(set.Events[0].DimensionCodes) != len(expected) {
			t.Fatal("rubric membership changed")
		}
		for i, code := range expected {
			if set.Events[0].DimensionCodes[i] != code {
				t.Fatal("wrong mapping")
			}
		}
		set.Events[0].DimensionCodes[0] = "changed"
		if eventDimensions[eventType][0] == "changed" {
			t.Fatal("mapping aliased")
		}
	}
	bad := EventSet{Events: []TeachingEvent{{ID: "ev1", Type: "invented"}}}
	if assignEventDimensions(&bad) == nil {
		t.Fatal("unknown event type accepted")
	}
}

func TestDimensionMistakenAsEventTypeIsRecovered(t *testing.T) {
	for got, want := range map[string]string{"expression": "term_explanation", "pace": "transition", "management": "organization", "technology": "tool_mention", "thinking": "reasoning"} {
		set := EventSet{Events: []TeachingEvent{{ID: "ev1", Type: got, Summary: "说明", Quotes: []EventQuote{{SegmentID: "s1", Text: "原文"}}}}}
		if err := assignEventDimensions(&set); err != nil {
			t.Fatal(err)
		}
		if set.Events[0].Type != want {
			t.Fatalf("%s => %s, want %s", got, set.Events[0].Type, want)
		}
	}
}

func TestEvidenceIDsResolveExactCrossBlockSources(t *testing.T) {
	segments := readingFixture()
	catalog := evidenceCatalog(segments)
	set := EventSet{Events: []TeachingEvent{{ID: "ev1", Type: "definition", Summary: "矩阵定义", DimensionCodes: []string{"content"}, Quotes: []EventQuote{{QuoteID: catalog[0].QuoteID}, {QuoteID: catalog[1].QuoteID}}}}}
	if err := resolveEventQuotes(&set, catalog); err != nil {
		t.Fatal(err)
	}
	if err := validateEvents(set, segments); err != nil {
		t.Fatal(err)
	}
	for i, q := range set.Events[0].Quotes {
		if q.Text != segments[i].Text || q.SegmentID != segments[i].ID {
			t.Fatal("source rewritten")
		}
	}
	for _, bad := range []EventQuote{{QuoteID: "invented"}, {QuoteID: catalog[0].QuoteID, SegmentID: segments[1].ID}, {QuoteID: catalog[0].QuoteID, Text: "invented text"}} {
		v := cloneValue(set)
		v.Events[0].Quotes[0] = bad
		if err := resolveEventQuotes(&v, catalog); err == nil {
			t.Fatal("invalid reference accepted")
		}
	}
	text := "课程 18.06。n 和 l。 Next sentence."
	for _, q := range evidenceCatalog([]Segment{{ID: "s", Text: text}}) {
		if q.Text == "课程 18." {
			t.Fatal("decimal split")
		}
	}
}

func TestQuoteFormattingRecoveryPreservesEvidence(t *testing.T) {
	segments := []Segment{{ID: "s1", Text: "比较 n  和\nl。 A matrix is just a."}}
	set := EventSet{Events: []TeachingEvent{{ID: "ev1", Type: "question", Summary: "比较", DimensionCodes: []string{"thinking"}, Quotes: []EventQuote{{SegmentID: "s1", Text: "比较 n 和 l"}, {SegmentID: "s1", Text: "比较 n 和 l"}}}}}
	repairEventFormatting(&set, segments)
	if len(set.Events[0].Quotes) != 1 || set.Events[0].Quotes[0].Text != "比较 n  和\nl" {
		t.Fatalf("evidence not restored: %#v", set)
	}
	if err := validateEvents(set, segments); err != nil {
		t.Fatal(err)
	}
	for _, quote := range []string{"比较 m 和 l", "a matrix is just a.", "A matrix is just a!", "比较 n 和 l！"} {
		if restored, ok := restoreQuoteWhitespace(segments[0].Text, quote); ok {
			t.Fatalf("changed non-whitespace evidence: %q", restored)
		}
	}
	if _, ok := restoreQuoteWhitespace("n  l / n\nl", "n l"); ok {
		t.Fatal("ambiguous source accepted")
	}
	if restored, ok := restoreQuoteWhitespace("矩阵\t定义", "矩阵 定义"); !ok || restored != "矩阵\t定义" {
		t.Fatal("unicode source offsets failed")
	}
}

func TestEventsRejectInventedQuotesAndDimensions(t *testing.T) {
	segments := []Segment{{ID: "s1", Text: "请比较 n 和 l。"}, {ID: "s2", Text: "再举一个例子。"}}
	valid := EventSet{Events: []TeachingEvent{{ID: "ev1", Type: "question", Summary: "要求比较", DimensionCodes: []string{"thinking"}, Quotes: []EventQuote{{SegmentID: "s1", Text: "比较 n 和 l"}}}}}
	if e := validateEvents(valid, segments); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*EventSet){
		func(v *EventSet) { v.Events[0].Quotes[0].Text = "比较 m 和 l" },
		func(v *EventSet) { v.Events[0].Quotes[0].SegmentID = "s2" },
		func(v *EventSet) { v.Events[0].DimensionCodes = []string{"technology"} },
		func(v *EventSet) { v.Events = append(v.Events, v.Events[0]) },
	} {
		v := cloneValue(valid)
		change(&v)
		if e := validateEvents(v, segments); e == nil {
			t.Fatal("invented or inconsistent event accepted")
		}
	}
}
func TestReportRequiresAllCrossSegmentEventSources(t *testing.T) {
	segments := []Segment{{ID: "s1", Text: "矩阵是"}, {ID: "s2", Text: "矩形数表。"}}
	events := []TeachingEvent{{ID: "ev1", Type: "definition", DimensionCodes: []string{"content"}, Quotes: []EventQuote{{SegmentID: "s1", Text: "矩阵是"}, {SegmentID: "s2", Text: "矩形数表"}}}}
	report := Report{IDs: []string{}, Observations: []Observation{{Code: "content", Type: "observation", Text: "定义矩阵", EventIDs: []string{"ev1"}, IDs: []string{"s1", "s2"}}}}
	for _, c := range codes {
		d := Dimension{Code: c, Status: "insufficient", Limitation: "证据不足", IDs: []string{}}
		if c == "content" {
			d.Status = "observed"
			d.Summary = "定义矩阵"
			d.IDs = []string{"s1", "s2"}
		}
		report.Dimensions = append(report.Dimensions, d)
	}
	if e := validateEventReport(report, events, segments); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Report){
		func(r *Report) { r.Observations[0].IDs = []string{"s1"} },
		func(r *Report) { r.Observations[0].EventIDs = []string{"missing"} },
		func(r *Report) { r.Observations[0].Code = "technology" },
		func(r *Report) {
			r.Dimensions[0].Status = "insufficient"
			r.Dimensions[0].Summary = ""
			r.Dimensions[0].IDs = []string{}
		},
	} {
		v := cloneValue(report)
		change(&v)
		if e := validateEventReport(v, events, segments); e == nil {
			t.Fatal("invalid report evidence accepted")
		}
	}
}

func TestNormalizeReportCoverageDowngradesUncoveredDimension(t *testing.T) {
	segments := []Segment{{ID: "s1", Text: "矩阵是矩形数表。"}}
	events := []TeachingEvent{{ID: "ev1", Type: "definition", DimensionCodes: []string{"content"}, Quotes: []EventQuote{{SegmentID: "s1", Text: segments[0].Text}}}}
	report := Report{Summary: "课堂介绍了矩阵。", IDs: []string{"s1"}, Observations: []Observation{}, Dimensions: []Dimension{
		{Code: "content", Status: "observed", Summary: "给出矩阵定义。", IDs: []string{"s1"}},
		{Code: "pace", Status: "insufficient", Limitation: "缺少证据", IDs: []string{}},
		{Code: "thinking", Status: "observed", Summary: "教师进行了推理。", IDs: []string{"s1"}},
		{Code: "expression", Status: "insufficient", Limitation: "缺少证据", IDs: []string{}},
		{Code: "management", Status: "insufficient", Limitation: "缺少证据", IDs: []string{}},
		{Code: "technology", Status: "insufficient", Limitation: "缺少证据", IDs: []string{}},
	}}
	normalizeReportCoverage(&report, events)
	thinking := report.Dimensions[2]
	if thinking.Status != "insufficient" || thinking.Summary != "" || len(thinking.IDs) != 0 || thinking.Limitation == "" {
		t.Fatalf("thinking dimension was not downgraded: %#v", thinking)
	}
	if err := validateEventReport(report, events, segments); err != nil {
		t.Fatalf("normalized report should validate: %v", err)
	}
}

func TestNormalizeReportCoverageKeepsUnknownEventForStrictValidation(t *testing.T) {
	report := Report{Observations: []Observation{{Code: "thinking", Type: "observation", Text: "推理", EventIDs: []string{"missing"}, IDs: []string{"s1"}}}}
	normalizeReportCoverage(&report, nil)
	if err := validateEventReport(report, nil, []Segment{{ID: "s1", Text: "文本"}}); err == nil {
		t.Fatal("unknown event must remain a validation error")
	}
}
