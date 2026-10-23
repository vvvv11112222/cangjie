package governance

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBehaviorSummaryKeepsUnknownAndSamplingGaps(t *testing.T) {
	yes, no := true, false
	obs := []exportObservation{{TimestampMS: 0, Behaviors: map[string]*bool{"hand_raise": &yes}}, {TimestampMS: 500, Behaviors: map[string]*bool{"hand_raise": &yes}}, {TimestampMS: 1000, Behaviors: map[string]*bool{"hand_raise": nil}}, {TimestampMS: 1500, Behaviors: map[string]*bool{"hand_raise": &no}}, {TimestampMS: 4000, Behaviors: map[string]*bool{"hand_raise": &yes}}}
	positive, known := behaviorCounts(obs, "hand_raise")
	if positive != 3 || known != 4 {
		t.Fatalf("unknown collapsed: %d/%d", positive, known)
	}
	ranges := positiveRanges(obs, "hand_raise", 500)
	if len(ranges) != 2 || !strings.Contains(ranges[0], "2个采样点") || ranges[1] != "00:04.000" {
		t.Fatalf("sampling gap bridged: %v", ranges)
	}
}
func TestPDFContainsAllAnonymousPeopleWithoutInternalDump(t *testing.T) {
	yes, no := true, false
	visual := &exportVisual{}
	visual.Sampling.IntervalMS = 500
	for i := 1; i <= 96; i++ {
		visual.Visual.Persons = append(visual.Visual.Persons, exportPerson{TrackID: fmt.Sprintf("T%04d", i), Observations: []exportObservation{{TimestampMS: 0, Behaviors: map[string]*bool{"hand_raise": &yes, "head_down": nil}}, {TimestampMS: 500, Behaviors: map[string]*bool{"hand_raise": &no, "head_down": &yes}}}})
	}
	snap := exportSnapshot{ExportedAt: time.Now(), Report: map[string]any{"revision": 1, "summary": "课堂综合结论", "content_sha256": "internal-content-hash", "provenance": map[string]any{"prompt_sha256": "private-prompt-hash"}}, Details: &reportDetails{Academic: map[string]string{"session_title": "数学课堂", "course_name": "数学", "teacher_name": "测试教师", "class_name": "测试班级"}, Visual: visual}}
	lines := strings.Join(pdfLines(snap), "\n")
	for _, person := range visual.Visual.Persons {
		if !strings.Contains(lines, person.TrackID) {
			t.Fatalf("missing person %s", person.TrackID)
		}
	}
	for _, unwanted := range []string{"internal-content-hash", "private-prompt-hash", "map[", "<nil>"} {
		if strings.Contains(lines, unwanted) {
			t.Fatalf("technical filler: %s", unwanted)
		}
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "private-prompt-hash") {
		t.Fatal("JSON provenance removed")
	}
	pdf, err := renderPDF(snap)
	if err != nil {
		t.Fatal(err)
	}
	if output := os.Getenv("PDF_PEOPLE_VALIDATION_OUTPUT"); output != "" {
		if err = os.WriteFile(output, pdf, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestLegacyReportExplainsMissingVisualAndDoesNotInventZeros(t *testing.T) {
	text := strings.Join(pdfLines(exportSnapshot{Report: map[string]any{}}), "\n")
	if !strings.Contains(text, "没有绑定可用视觉记录") || strings.Contains(text, "举手最多 0 人") {
		t.Fatal(text)
	}
}
