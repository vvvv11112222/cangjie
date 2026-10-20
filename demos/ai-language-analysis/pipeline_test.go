package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestVideoPipelineAndEmptyChunkCheckpoint(t *testing.T) {
	ffmpeg, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("FFmpeg unavailable")
	}
	ffprobe, e := exec.LookPath("ffprobe")
	if e != nil {
		t.Skip("FFprobe unavailable")
	}
	t.Setenv("FFMPEG_PATH", ffmpeg)
	t.Setenv("FFPROBE_PATH", ffprobe)
	video := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=green:s=160x90:r=10", "-f", "lavfi", "-i", "sine=frequency=400:sample_rate=16000", "-t", "1.5", "-c:v", "mpeg4", "-c:a", "aac", video)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("media fixture: %v %s", e, out)
	}
	asrCalls, textCalls := 0, 0
	empty := false
	report := Report{IDs: []string{}, Observations: []Observation{}}
	for _, code := range codes {
		report.Dimensions = append(report.Dimensions, Dimension{Code: code, Status: "insufficient", Limitation: "样本证据不足", IDs: []string{}})
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream bool `json:"stream"`
		}
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			t.Error(e)
		}
		if !req.Stream {
			asrCalls++
			text := "课堂样本。"
			if empty {
				text = ""
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "asr-fake", "choices": []any{map[string]any{"message": map[string]string{"content": text}, "finish_reason": "stop"}}})
			return
		}
		index := textCalls % 3
		textCalls++
		var answer any
		switch index {
		case 0:
			answer = map[string]any{"boundaries": []any{}}
		case 1:
			answer = EventSet{Events: []TeachingEvent{}}
		case 2:
			answer = draftFixture(report, nil)
		}
		raw, _ := json.Marshal(answer)
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer provider.Close()
	var saved []Segment
	var chunks []AudioChunk
	var done []int
	checkpoint := func(s []Segment, c []AudioChunk, d []int) error {
		saved = cloneValue(s)
		chunks = cloneValue(c)
		done = cloneValue(d)
		return nil
	}
	progress := func(string, string, int) {}
	out, _, err := analyzeVideo(context.Background(), provider.URL, "fake", video, nil, nil, nil, progress, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Segments) != 1 || asrCalls != 1 || textCalls != 3 || len(done) != 1 {
		t.Fatal("video workflow incomplete")
	}
	_, _, err = analyzeVideo(context.Background(), provider.URL, "fake", video, saved, done, chunks, progress, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if asrCalls != 1 {
		t.Fatal("completed ASR called twice")
	}
	changed := cloneValue(chunks)
	changed[0].End++
	if _, _, err = analyzeVideo(context.Background(), provider.URL, "fake", video, saved, done, changed, progress, checkpoint); err == nil {
		t.Fatal("changed checkpoint plan accepted")
	}
	// Empty recognition still checkpoints the completed request, so retrying
	// does not repeatedly bill a silent chunk.
	empty = true
	saved = nil
	chunks = nil
	done = nil
	_, _, err = analyzeVideo(context.Background(), provider.URL, "fake", video, nil, nil, nil, progress, checkpoint)
	if err == nil || len(done) != 1 || len(saved) != 0 {
		t.Fatal("empty chunk checkpoint lost")
	}
	previous := asrCalls
	_, _, _ = analyzeVideo(context.Background(), provider.URL, "fake", video, saved, done, chunks, progress, checkpoint)
	if asrCalls != previous {
		t.Fatal("empty ASR repeated")
	}

	// Exercise the actual background worker's private media lookup and commit.
	empty = false
	t.Setenv("DASHSCOPE_API_KEY", "fake")
	t.Setenv("BAILIAN_BASE_URL", provider.URL)
	store, e := newJobStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.create("worker-video", video, "sample.mp4"); e != nil {
		t.Fatal(e)
	}
	manager := testManager(store)
	manager.run("worker-video")
	job, _ := store.get("worker-video")
	if job.Status != jobSucceeded || job.RevisionNo != 1 || len(job.ReportVersions) != 1 || len(store.revisionsFor(job.ID)) != 1 {
		t.Fatalf("background video job not committed: %+v", job)
	}
}

func TestFailedASRStopsBeforeTextAndResumesOnlyUnfinishedChunk(t *testing.T) {
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
	video := filepath.Join(t.TempDir(), "two-chunks.mp4")
	cmd := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=green:s=64x48:r=1", "-f", "lavfi", "-i", "sine=frequency=400:sample_rate=16000", "-t", "45", "-c:v", "mpeg4", "-c:a", "aac", video)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	var asrCalls, textCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if !req.Stream {
			n := asrCalls.Add(1)
			if n == 2 {
				http.Error(w, "simulated ASR failure", 500)
				return
			}
			text := "第一块已识别。"
			if n > 2 {
				text = ""
			} // Successful silence is complete, not failure.
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": text}, "finish_reason": "stop"}}})
			return
		}
		n := textCalls.Add(1)
		var answer any
		switch n {
		case 1:
			answer = map[string]any{"boundaries": []any{}}
		case 2:
			answer = EventSet{Events: []TeachingEvent{}}
		case 3:
			answer = draftFixture(insufficientReport("样本证据不足"), nil)
		default:
			t.Error("unexpected text retry")
			http.Error(w, "unexpected", 500)
			return
		}
		raw, _ := json.Marshal(answer)
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer provider.Close()
	t.Setenv("DASHSCOPE_API_KEY", "fake")
	t.Setenv("BAILIAN_BASE_URL", provider.URL)
	s, err := newJobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.create("resume", video, "two-chunks.mp4"); err != nil {
		t.Fatal(err)
	}
	m := testManager(s)
	m.run("resume")
	j, _ := s.get("resume")
	if j.Status != jobFailed || j.FailureStage != "asr" || j.ASRStatus != asrIncomplete || len(j.CompletedASRChunks) != 1 || j.CompletedASRChunks[0] != 0 || len(j.AudioChunks) != 2 || len(j.Segments) != 1 || j.RevisionNo != 0 || len(s.revisionsFor("resume")) != 0 || textCalls.Load() != 0 || asrCalls.Load() != 2 {
		t.Fatalf("failed chunk checkpointed or text called: %+v", j)
	}
	if !strings.Contains(j.Error, "第 2 段 ASR 失败") {
		t.Fatal("missing failure details")
	}
	first := cloneValue(j.Segments[0])
	if err = m.retry("resume"); err != nil {
		t.Fatal(err)
	}
	m.run("resume")
	j, _ = s.get("resume")
	if j.Status != jobSucceeded || j.ASRStatus != asrComplete || j.ReportStatus != reportReady || len(j.CompletedASRChunks) != 2 || len(j.Segments) != 1 || j.Segments[0] != first || j.RevisionNo != 1 || len(j.ReportVersions) != 1 || asrCalls.Load() != 3 || textCalls.Load() != 3 {
		t.Fatalf("resume repeated success or lost empty checkpoint: %+v", j)
	}
}
