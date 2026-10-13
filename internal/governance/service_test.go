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
	}}
	lines := strings.Join(pdfLines(snap), "\n")
	for _, want := range []string{"总摘要证据", "维度/状态", "不足原因", "观察/复核", "观察证据"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("PDF lines omitted %q", want)
		}
	}
	pdf := renderPDF(snap)
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4")) || bytes.Contains(pdf, []byte("/Count 1 >>")) {
		t.Fatalf("expected a valid multi-page PDF, bytes=%d", len(pdf))
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
