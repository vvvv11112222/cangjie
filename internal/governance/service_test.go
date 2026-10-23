package governance

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderPDFIncludesFullReportAndPaginates(t *testing.T) {
	dimensions := make([]any, 0, 6)
	for _, code := range []string{"content", "pace", "thinking", "expression", "management", "technology"} {
		dimensions = append(dimensions, map[string]any{"dimension_code": code, "coverage_status": "observed", "summary": strings.Repeat("维度结论", 20), "limitation": "", "summary_evidence_ids": []any{"e-1"}})
	}
	observations := make([]any, 0, 20)
	for i := 0; i < 20; i++ {
		observations = append(observations, map[string]any{"dimension_code": "content", "review_status": "accepted", "observation_text": fmt.Sprintf("观察%d%s", i, strings.Repeat("完整内容", 12)), "suggestion": "改进建议", "evidence_ids": []any{"e-1"}})
	}
	snap := exportSnapshot{ExportedAt: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC), Report: map[string]any{
		"id": "report-1", "revision": 3, "summary": "总摘要", "summary_evidence_ids": []any{"e-1"}, "limitations": []any{"限制"}, "dimensions": dimensions, "observations": observations,
	}, Source: map[string]any{"title": "固定来源标题", "attribution": "固定来源署名"}, EvidenceAvailability: map[string]string{"e-1": "available", "e-2": "expired"}}
	snap.Report["provenance"] = map[string]any{"model_revision": "fixture-revision-7", "prompt_sha256": strings.Repeat("a", 64), "coverage": []any{map[string]any{"start_ms": 0, "end_ms": 1000}}}
	lines := strings.Join(pdfLines(snap), "\n")
	for _, want := range []string{"总摘要", "教学内容", "教学节奏", "学生行为汇总", "改进建议", "语音与证据依据"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("PDF lines omitted %q", want)
		}
	}
	for _, want := range []string{"固定来源标题", "固定来源署名"} {
		if !strings.Contains(strings.ReplaceAll(lines, "\n  ", ""), want) {
			t.Fatalf("PDF provenance omitted %q", want)
		}
	}
	pdf, err := renderPDF(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("expected a valid multi-page PDF, bytes=%d", len(pdf))
	}
	if !bytes.Contains(pdf, []byte("/FontFile2")) || !bytes.Contains(pdf, []byte("/ToUnicode")) {
		t.Fatal("missing embedded font or Unicode text mapping")
	}
	if output := os.Getenv("PDF_VALIDATION_OUTPUT"); output != "" {
		if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(output, pdf, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
