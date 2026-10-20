package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

// Only the explicitly selected live test installs this recorder. No headers or
// credentials are saved. The transport permits at most eight text requests to
// the configured endpoint; it cannot upload audio or access the running demo.
type publicCourseRecorder struct {
	next              http.RoundTripper
	host, path, model string
	sample            string
	stage             string
	calls             int
	authFailure       bool
	write             func(string, []byte)
}

type publicCourseResponseBody struct {
	io.ReadCloser
	bytes bytes.Buffer
	close func([]byte)
}

func (b *publicCourseResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytes.Write(p[:n])
	return n, err
}

func (b *publicCourseResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.close(b.bytes.Bytes())
	return err
}

func (r *publicCourseRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.calls >= 8 || req.Method != "POST" || req.URL.Host != r.host || req.URL.Path != r.path || req.URL.Scheme != "https" || req.URL.RawQuery != "" {
		return nil, fmt.Errorf("live public-course runner refused an out-of-scope request")
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	var body struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Model != r.model || !body.Stream || bytes.Contains(raw, []byte(`"input_audio"`)) {
		return nil, fmt.Errorf("live public-course runner permits configured text-model requests only")
	}
	r.calls++
	prefix := fmt.Sprintf("%02d-%s-%s", r.calls, r.sample, r.stage)
	r.write(prefix+"-request.json", raw)
	req.Body = io.NopCloser(bytes.NewReader(raw))
	started := time.Now()
	res, err := r.next.RoundTrip(req)
	if err != nil {
		r.write(prefix+"-transport.json", []byte(`{"status":"transport_failed","billing":"unknown","retry":false}`))
		return nil, err
	}
	if res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 429 {
		r.authFailure = true
	}
	res.Body = &publicCourseResponseBody{ReadCloser: res.Body, close: func(raw []byte) {
		r.write(prefix+"-response.sse", raw)
		meta, _ := json.Marshal(map[string]any{"http_status": res.StatusCode, "elapsed_ms": time.Since(started).Milliseconds(), "response_bytes_read": len(raw), "retry": false})
		r.write(prefix+"-transport.json", meta)
	}}
	return res, nil
}

type publicCourseRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f publicCourseRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPublicCourseRecorderLimitsAndArtifacts(t *testing.T) {
	artifacts := map[string]string{}
	forwarded := 0
	r := &publicCourseRecorder{host: "provider.invalid", path: "/v1/chat/completions", model: "configured-text", sample: "fixture", stage: "audit",
		write: func(name string, b []byte) { artifacts[name] = string(b) },
		next: publicCourseRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			forwarded++
			if req.Header.Get("Authorization") != "Bearer fixture-credential" {
				t.Error("outbound authorization was lost")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
		}),
	}
	request := func(endpoint, body string) *http.Request {
		req, err := http.NewRequest("POST", endpoint, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer fixture-credential")
		return req
	}
	body := `{"model":"configured-text","stream":true,"messages":[]}`
	for _, invalid := range []struct{ endpoint, body string }{
		{"http://provider.invalid/v1/chat/completions", body},
		{"https://other.invalid/v1/chat/completions", body},
		{"https://provider.invalid/api/jobs", body},
		{"https://provider.invalid/v1/chat/completions?secret=fixture", body},
		{"https://provider.invalid/v1/chat/completions", `{"model":"audio","stream":true}`},
		{"https://provider.invalid/v1/chat/completions", `{"model":"configured-text","stream":true,"input_audio":{}}`},
	} {
		if _, err := r.RoundTrip(request(invalid.endpoint, invalid.body)); err == nil {
			t.Fatal("out-of-scope request accepted")
		}
	}
	if forwarded != 0 || len(artifacts) != 0 {
		t.Fatal("rejected request reached provider or artifacts")
	}
	for i := 0; i < 8; i++ {
		res, err := r.RoundTrip(request("https://provider.invalid/v1/chat/completions", body))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}
	if _, err := r.RoundTrip(request("https://provider.invalid/v1/chat/completions", body)); err == nil || forwarded != 8 || r.calls != 8 || len(artifacts) != 24 {
		t.Fatal("request budget or raw recording failed")
	}
	for _, raw := range artifacts {
		if strings.Contains(raw, "fixture-credential") || strings.Contains(raw, "Authorization") {
			t.Fatal("authorization appeared in artifacts")
		}
	}
	if artifacts["01-fixture-audit-request.json"] != body || artifacts["01-fixture-audit-response.sse"] != "data: [DONE]\n\n" {
		t.Fatal("raw text request or response was altered")
	}
}

// Explicit opt-in: at most eight billable text calls, no retries, no ASR, no
// job-store access and no manual review writes. Semantic review follows later.
func TestLivePublicCourseComparison(t *testing.T) {
	if os.Getenv("BAILIAN_LIVE_PUBLIC_COURSE_TEST") != "1" {
		t.Skip("live public-course comparison disabled")
	}
	files, _ := publicCourseFiles(t)
	if err := godotenv.Load(); err != nil {
		t.Fatal("cannot load demo model configuration")
	}
	key, base := os.Getenv("DASHSCOPE_API_KEY"), strings.TrimRight(os.Getenv("BAILIAN_BASE_URL"), "/")
	endpoint, err := url.Parse(base)
	if err != nil || key == "" || endpoint.Host == "" || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		t.Fatal("missing or unsupported model endpoint configuration; no request sent")
	}
	t.Setenv("BAILIAN_REPORT_AUDIT", "1")
	samples := []string{"physics", "biology"}
	if selected := strings.TrimSpace(os.Getenv("BAILIAN_PUBLIC_COURSE_SAMPLE")); selected != "" {
		if selected != "physics" && selected != "biology" {
			t.Fatal("BAILIAN_PUBLIC_COURSE_SAMPLE must be physics or biology; no request sent")
		}
		samples = []string{selected}
	}
	runDir := filepath.Join("evaluation", "2026-10-17-public-regression", "runs", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(filepath.Dir(runDir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runDir, 0755); err != nil {
		t.Fatal("cannot reserve isolated run directory", err)
	}
	write := func(name string, b []byte) {
		t.Helper()
		if bytes.Contains(b, []byte(key)) {
			t.Fatal("refused to persist content containing a credential")
		}
		f, err := os.OpenFile(filepath.Join(runDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := f.Write(b)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal("cannot persist live comparison artifact")
		}
	}
	writeJSON := func(name string, value any) {
		t.Helper()
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		write(name, append(raw, '\n'))
	}
	rules := map[string]string{}
	for _, name := range []string{"prompt.txt", "events_prompt.txt", "reading_prompt.txt", "audit_prompt.txt", "rubric.md", "advice.go", "report_audit.go", "report_contract.go", "pipeline.go", "semantic_contract.go", "narrative_contract.go", "events.go", "report_basis.go", "report_evidence.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		rules[name] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	writeJSON("manifest.json", map[string]any{"created_at": time.Now().UTC(), "model": env("BAILIAN_TEXT_MODEL", "qwen-plus"), "audit_version": reportAuditVersion, "advice_protocol": adviceProtocol, "analysis_version": analysisVersion, "semantic_protocol": semanticProtocol, "narrative_protocol": narrativeProtocol, "rubric_version": rubricVersion, "rules_sha256": rules, "max_text_requests": 8, "asr_requests": 0, "automatic_retries": 0, "scope": "same archived transcripts, live text pipeline; not teacher acceptance or classroom accuracy"})
	previousTransport := http.DefaultTransport
	recorder := &publicCourseRecorder{next: previousTransport, host: endpoint.Host, path: strings.TrimRight(endpoint.Path, "/") + "/chat/completions", model: env("BAILIAN_TEXT_MODEL", "qwen-plus"), write: write}
	http.DefaultTransport = recorder
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	t.Log("isolated live results:", runDir)
	results := []map[string]any{}
	for _, sample := range samples {
		snapshot := publicCourseReport(t, files[sample+"_report"])
		before := cloneValue(snapshot.Segments)
		recorder.sample = sample
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
		out, err := analyzeSegments(ctx, base, key, snapshot.Segments, func(stage, _ string, _ int) {
			recorder.stage = stage
			t.Logf("%s: %s", sample, stage)
		})
		cancel()
		unchanged := reflect.DeepEqual(before, snapshot.Segments) && reflect.DeepEqual(before, out.Segments)
		writeJSON(sample+"-result.json", map[string]any{"analysis": out, "report_diagnostic": out.ReportDiagnostic, "same_transcript": unchanged, "input_revision_no": 1, "report_version": 1, "source_file_sha256": fmt.Sprintf("%x", sha256.Sum256(files[sample+"_report"]))})
		status := "unavailable"
		flags := 0
		if out.Report.Audit != nil {
			status, flags = out.Report.Audit.Status, len(out.Report.Audit.Findings)
		}
		results = append(results, map[string]any{"sample": sample, "same_transcript": unchanged, "report_status": out.ReportStatus, "reading_status": out.Reading.Status, "audit_status": status, "audit_findings": flags, "observations": len(out.Report.Observations), "context_cancelled": ctx.Err() != nil && err != nil})
		t.Logf("%s: report=%s audit=%s flags=%d unchanged=%v", sample, out.ReportStatus, status, flags, unchanged)
		if err != nil || !unchanged || out.ReportStatus != reportReady || out.Reading.Status != "ready" || (status != "no_flags" && status != "needs_review") {
			t.Errorf("%s did not complete all protocol stages; inspect isolated artifacts, no automatic retry", sample)
		}
		if recorder.authFailure {
			t.Error("authentication, permission or quota failure; remaining samples stopped")
			break
		}
	}
	writeJSON("results.json", map[string]any{"samples": results, "selected_samples": samples, "actual_text_requests": recorder.calls, "asr_requests": 0, "automatic_retries": 0, "semantic_review": "pending"})
}
