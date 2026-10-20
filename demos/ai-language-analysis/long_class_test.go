package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func longSegments(minutes int) []Segment {
	var out []Segment
	for i := 0; i < minutes*2; i++ {
		out = append(out, Segment{ID: fmt.Sprintf("e-%04d", i+1), ChunkIndex: i, Start: int64(i) * 30000, End: int64(i+1) * 30000, Text: fmt.Sprintf("第%d段解释本阶段的概念和适用条件。", i+1)})
	}
	return out
}

func TestHourDurationAndWindowTimeline(t *testing.T) {
	for _, seconds := range []float64{1, 600, 2700, 3600} {
		if err := validateMediaDuration(seconds); err != nil {
			t.Fatal(seconds, err)
		}
	}
	for _, seconds := range []float64{0, -1, 3600.001, math.NaN(), math.Inf(1)} {
		if validateMediaDuration(seconds) == nil {
			t.Fatal("invalid duration accepted", seconds)
		}
	}
	segments := longSegments(60)
	windows := lessonWindows(segments)
	if len(windows) != 6 {
		t.Fatal("hour coverage", len(windows))
	}
	var ids []string
	for i, w := range windows {
		ids = append(ids, w.IDs...)
		if w.Number != i+1 || w.Start != int64(i)*analysisWindowMS || w.End != int64(i+1)*analysisWindowMS {
			t.Fatal("timeline reset", w)
		}
		if i > 0 && w.Segments[0].ID != windows[i-1].IDs[len(windows[i-1].IDs)-1] {
			t.Fatal("missing boundary context")
		}
	}
	for i, id := range ids {
		if id != segments[i].ID {
			t.Fatal("source omitted or repeated", i)
		}
	}
	chunks := planChunks(3600000, nil)
	start := int64(0)
	for _, chunk := range chunks {
		if chunk.Start != start || chunk.End <= start || chunk.End-start > 40000 {
			t.Fatal("gap, overlap or overlong audio block", chunk)
		}
		start = chunk.End
	}
	if start != 3600000 {
		t.Fatal("missing lesson end")
	}
	segments[0].Text = strings.Repeat("字", analysisWindowRunes)
	if len(lessonWindows(segments)[0].IDs) != 1 {
		t.Fatal("dense transcript was not windowed")
	}
}

// All responses are local deterministic fixtures. This tests coverage and
// evidence binding, not the accuracy or pedagogy of a real model.
type lessonProvider struct {
	mu            sync.Mutex
	failedASR     bool
	failASRAt     int
	asrCalls      int
	successfulASR int
	eventCalls    int
	failEventAt   int
	reportCalls   int
	auditCalls    int
	reportSources []Segment
	auditSources  []Segment
}

func (p *lessonProvider) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	var request struct {
		Stream   bool `json:"stream"`
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Error(err)
		http.Error(w, "invalid", 400)
		return
	}
	if !request.Stream {
		p.asrCalls++
		if p.asrCalls == p.failASRAt && !p.failedASR {
			p.failedASR = true
			http.Error(w, "simulated interruption", 500)
			return
		}
		p.successfulASR++
		text := fmt.Sprintf("第%d段解释本阶段的概念和适用条件。", p.successfulASR)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("local-asr-%d", p.asrCalls), "choices": []any{map[string]any{"message": map[string]string{"content": text}, "finish_reason": "stop"}}})
		return
	}
	var user string
	if len(request.Messages) != 2 || json.Unmarshal(request.Messages[1].Content, &user) != nil {
		t.Error("bad text input")
		http.Error(w, "bad input", 400)
		return
	}
	var answer any
	if strings.HasPrefix(user, "[") {
		var sources []Segment
		if err := json.Unmarshal([]byte(user), &sources); err != nil {
			t.Error(err)
		}
		boundaries := conservativeBoundaries(sources)
		// Join across every window seam, proving overlap decisions survive merge.
		for i := range boundaries {
			boundaries[i].JoinSentence = true
			boundaries[i].LowercaseNext = false
		}
		answer = map[string]any{"boundaries": boundaries}
	} else {
		var input struct {
			Sources   []Segment         `json:"original_segments"`
			Catalog   json.RawMessage   `json:"evidence_catalog"`
			Bases     []ReportBasis     `json:"evidence_bases"`
			Scope     *eventWindowScope `json:"window_scope"`
			Windows   []lessonWindow    `json:"lesson_windows"`
			Candidate *Report           `json:"candidate_report"`
		}
		if err := json.Unmarshal([]byte(user), &input); err != nil {
			t.Error(err)
		}
		switch {
		case input.Candidate != nil:
			p.auditCalls++
			p.auditSources = cloneValue(input.Sources)
			answer = auditFixture(*input.Candidate)
		case input.Bases != nil:
			p.reportCalls++
			p.reportSources = cloneValue(input.Sources)
			var catalog struct {
				Columns [3]string   `json:"columns"`
				Rows    [][3]string `json:"rows"`
			}
			if err := json.Unmarshal(input.Catalog, &catalog); err != nil {
				t.Error(err)
				http.Error(w, "invalid report catalog", 400)
				return
			}
			original := evidenceCatalog(input.Sources)
			if catalog.Columns != [3]string{"quote_id", "segment_id", "text"} || len(catalog.Rows) != len(original) {
				t.Error("compact catalog lost columns or quotes")
			}
			for i, q := range original {
				if i >= len(catalog.Rows) || catalog.Rows[i] != [3]string{q.QuoteID, q.SegmentID, q.Text} {
					t.Error("compact catalog changed quote mapping", i)
				}
			}
			if len(input.Windows) < 2 {
				t.Error("report missing classroom windows")
			}
			draft := ReportDraft{Summary: "按阶段解释概念与适用条件。", IDs: []string{}, Dimensions: insufficientReport("未提供足够依据").Dimensions, Observations: []ObservationDraft{}}
			if len(input.Bases) > 0 {
				basis := input.Bases[len(input.Bases)-1]
				ids := []string{basis.Evidence.Quotes[0].SegmentID}
				draft.IDs = ids
				for i := range draft.Dimensions {
					if draft.Dimensions[i].Code == basis.Code {
						draft.Dimensions[i] = Dimension{Code: basis.Code, Status: "observed", Summary: "说明后段概念的适用条件。", IDs: ids}
					}
				}
				draft.Observations = append(draft.Observations, ObservationDraft{Key: basis.Key, Type: "highlight", Title: "后段概念解释", Text: basis.Evidence.Summary, Analysis: "将概念与本阶段的适用条件相联系。"})
			}
			answer = draft
		default:
			p.eventCalls++
			if p.eventCalls == p.failEventAt {
				http.Error(w, "simulated window failure", 500)
				return
			}
			if input.Scope == nil || input.Scope.MaxEvents <= 0 {
				t.Error("missing window budget")
			}
			var catalog []EventQuote
			if err := json.Unmarshal(input.Catalog, &catalog); err != nil || len(catalog) == 0 {
				t.Error("invalid window quote catalog", err)
				http.Error(w, "invalid catalog", 400)
				return
			}
			q := catalog[len(catalog)-1]
			answer = EventSet{Events: []TeachingEvent{{ID: "ev-001", Type: "definition", Summary: "解释该阶段概念与条件。", Quotes: []EventQuote{{QuoteID: q.QuoteID}}}}}
		}
	}
	raw, _ := json.Marshal(answer)
	chunk, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("local-text-%d-%d-%d", p.eventCalls, p.reportCalls, p.auditCalls), "choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
}

func TestHourAnalysisIncludesEndingAndAllReadingSeams(t *testing.T) {
	t.Setenv("BAILIAN_REPORT_AUDIT", "1")
	p := &lessonProvider{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	defer server.Close()
	sources := longSegments(60)
	out, err := analyzeSegments(context.Background(), server.URL, "local-fake", sources, func(string, string, int) {})
	if err != nil || out.ReportStatus != reportReady {
		t.Fatalf("hour report failed: %v %s", err, out.FailureMessage)
	}
	if !reflect.DeepEqual(sources, p.reportSources) || !reflect.DeepEqual(sources, p.auditSources) {
		t.Fatal("final report or audit omitted transcript tail")
	}
	if len(out.Reading.Boundaries) != len(sources)-1 || len(out.Reading.Edits) != len(sources) {
		t.Fatal("reading overlap lost/repeated blocks")
	}
	for i, b := range out.Reading.Boundaries {
		if b.LeftID != sources[i].ID || b.RightID != sources[i+1].ID || !b.JoinSentence {
			t.Fatal("boundary merge failed", i)
		}
	}
	if p.eventCalls != 6 || p.reportCalls != 1 || p.auditCalls != 1 || len(out.Events.Events) != 6 {
		t.Fatal("window workflow incomplete", p)
	}
	if out.Coverage.EventWindowsCompleted != 6 || out.Coverage.SourceCount != 120 || len(out.EventRequest.Requests) != 6 || len(out.Reading.Request.Requests) != 6 {
		t.Fatal("coverage or per-request usage missing")
	}
	last := out.Report.Observations[0]
	if last.IDs[0] != sources[len(sources)-1].ID {
		t.Fatal("final evidence did not use classroom end")
	}
	if err := validateEventReport(out.Report, out.Events.Events, sources); err != nil {
		t.Fatal(err)
	}
}

func TestLaterWindowFailureCannotPublishPartialClassroom(t *testing.T) {
	p := &lessonProvider{failEventAt: 3}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	defer server.Close()
	sources := longSegments(45)
	out, err := analyzeSegments(context.Background(), server.URL, "local-fake", sources, func(string, string, int) {})
	if err != nil || out.ReportStatus != reportFailed || out.FailureStage != "events" || p.reportCalls != 0 || p.eventCalls != 3 || !reflect.DeepEqual(out.Segments, sources) {
		t.Fatal("incomplete window was published or transcript lost", err, out.FailureMessage)
	}
	if !strings.Contains(out.FailureMessage, "第 3 / 5 部分") {
		t.Fatal("missing actionable failure position", out.FailureMessage)
	}
	if out.Coverage.EventWindowsCompleted != 2 || len(out.Events.Events) != 2 {
		t.Fatal("completed window evidence not retained")
	}
}

func TestHourEnglishSizedTranscriptFitsWithoutClipping(t *testing.T) {
	t.Setenv("BAILIAN_REPORT_AUDIT", "1")
	p := &lessonProvider{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	defer server.Close()
	sources := longSegments(60)
	for i := range sources {
		sources[i].Text = fmt.Sprintf("Part %d: ", i+1) + strings.Repeat("Concept conditions example reasoning. ", 12)
	}
	out, err := analyzeSegments(context.Background(), server.URL, "local-fake", sources, func(string, string, int) {})
	if err != nil || out.ReportStatus != reportReady || !reflect.DeepEqual(p.reportSources, sources) || !reflect.DeepEqual(p.auditSources, sources) {
		t.Fatal("hour-sized English text clipped or rejected", err, out.FailureMessage)
	}
}

func TestReadingWindowFallbackKeepsAllOriginalWords(t *testing.T) {
	sources := longSegments(30)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 {
			http.Error(w, "simulated reading failure", 500)
			return
		}
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		var part []Segment
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &part); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(map[string]any{"boundaries": conservativeBoundaries(part)})
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer server.Close()
	reading := organizeReading(context.Background(), server.URL, "local-fake", sources)
	if calls != 3 || reading.Status != "conservative" || len(reading.Boundaries) != len(sources)-1 || len(reading.Edits) != len(sources) {
		t.Fatal("partial fallback lost coverage", reading.Status, calls)
	}
	for i, edit := range reading.Edits {
		if edit.ID != sources[i].ID || !sameReadingWords(sources[i].Text, edit.Text) {
			t.Fatal("fallback changed source", i)
		}
	}
}

func TestTextBudgetRejectsWholeRequestBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "must not call", 500) }))
	defer server.Close()
	input := map[string]string{"text": strings.Repeat("字", maxTextRequestRunes+1)}
	_, _, err := textTask(context.Background(), server.URL, "local-fake", "prompt", input)
	if err == nil || calls != 0 || !strings.Contains(err.Error(), "未截断") || len([]rune(input["text"])) != maxTextRequestRunes+1 {
		t.Fatal("oversized request truncated or sent", err, calls)
	}
}

type memoryUpload struct{ *bytes.Reader }

func (memoryUpload) Close() error { return nil }

func TestUploadLimitsAndIncompleteFileCleanup(t *testing.T) {
	if maxUploadBytes != int64(2)*1024*1024*1024 || maxUploadRequestBytes <= maxUploadBytes {
		t.Fatal("2GB limit or multipart allowance incorrect")
	}
	for _, size := range []int{16, 17} {
		root := t.TempDir()
		path, err := saveUploadLimit(memoryUpload{bytes.NewReader(bytes.Repeat([]byte("a"), size))}, "lesson.mp4", root, 16)
		if size == 16 {
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Size() != 16 {
				t.Fatal("accepted file incomplete", err)
			}
		} else {
			if err == nil || path != "" {
				t.Fatal("oversized upload accepted")
			}
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					t.Error(err)
				} else if !d.IsDir() {
					t.Error("partial file left behind", path)
				}
				return nil
			})
		}
	}
	store, err := newJobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mux := newDemoMux(testManager(store), store.root)
	request := httptest.NewRequest("POST", "/api/jobs", strings.NewReader("not a real multipart body"))
	request.ContentLength = maxUploadRequestBytes + 1
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || len(store.list()) != 0 {
		t.Fatal("oversized body queued or parsed", response.Code)
	}
}

func TestWindowsRejectEvidenceFromAnotherPart(t *testing.T) {
	sources := longSegments(30)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The source exists globally but was not sent in the final window.
		answer := EventSet{Events: []TeachingEvent{{ID: "ev-001", Type: "definition", Summary: "错误引用另一部分。", Quotes: []EventQuote{{QuoteID: "e-0001-q01"}}}}}
		raw, _ := json.Marshal(answer)
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer server.Close()
	set, _, err := extractEvents(context.Background(), server.URL, "local-fake", sources)
	if err == nil || len(set.Events) != 1 || !strings.Contains(err.Error(), "第 2 / 3 部分") {
		t.Fatal("cross-window evidence accepted", err, len(set.Events))
	}
}

func TestFortyFiveMinuteMediaResumesWithoutRepeatingSuccessfulASR(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("FFprobe unavailable")
	}
	t.Setenv("FFMPEG_PATH", ffmpeg)
	t.Setenv("FFPROBE_PATH", ffprobe)
	t.Setenv("BAILIAN_REPORT_AUDIT", "1")
	video := filepath.Join(t.TempDir(), "synthetic-45-minutes.mp4")
	command := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=green:s=64x48:r=1/10", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000", "-t", "2700", "-c:v", "mpeg4", "-c:a", "aac", video)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("long synthetic media: %v %s", err, output)
	}
	p := &lessonProvider{failASRAt: 35}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	defer server.Close()
	t.Setenv("BAILIAN_BASE_URL", server.URL)
	t.Setenv("DASHSCOPE_API_KEY", "local-fake")
	store, err := newJobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.create("long-class", video, "synthetic-45-minutes.mp4"); err != nil {
		t.Fatal(err)
	}
	manager := testManager(store)
	manager.run("long-class")
	first, _ := store.get("long-class")
	if first.Status != jobFailed || len(first.CompletedASRChunks) != 34 || p.eventCalls != 0 || first.RevisionNo != 0 {
		t.Fatal("failure checkpoint incorrect", first.Status, len(first.CompletedASRChunks))
	}
	original := cloneValue(first.Segments)
	if err = manager.retry("long-class"); err != nil {
		t.Fatal(err)
	}
	manager.run("long-class")
	job, _ := store.get("long-class")
	if job.Status != jobSucceeded || job.ASRStatus != asrComplete || job.ReportStatus != reportReady {
		t.Fatalf("long resume failed: %s %s", job.Status, job.Error)
	}
	if len(job.CompletedASRChunks) != len(job.AudioChunks) || p.asrCalls != len(job.AudioChunks)+1 || !reflect.DeepEqual(job.Segments[:len(original)], original) {
		t.Fatal("successful ASR repeated or modified")
	}
	end := job.AudioChunks[len(job.AudioChunks)-1].End
	if end < 2700000 || end > 2700100 || job.Segments[len(job.Segments)-1].End != end {
		t.Fatal("source timeline drift", end)
	}
	if !reflect.DeepEqual(job.Segments, p.reportSources) || !reflect.DeepEqual(job.Segments, p.auditSources) {
		t.Fatal("media tail missing from model input")
	}
	if job.Coverage == nil || job.Coverage.EventWindowsCompleted != len(job.Coverage.Windows) || len(job.Coverage.Windows) < 5 || len(job.ReportVersions) != 1 || job.ReportVersions[0].Coverage == nil {
		t.Fatal("coverage/history missing")
	}
	if err = validateEventReport(job.Report, job.Events.Events, job.Segments); err != nil {
		t.Fatal(err)
	}
	t.Logf("45-minute media: %d audio blocks, %d successful ASR + 1 failed request, %d analysis windows; original timeline end %dms", len(job.AudioChunks), p.successfulASR, len(job.Coverage.Windows), end)
}
