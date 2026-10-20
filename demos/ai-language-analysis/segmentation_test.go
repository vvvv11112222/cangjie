package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSilenceParser(t *testing.T) {
	got := parseSilences("noise\nsilence_start: 26.25\nsilence_end: 27.75 | silence_duration: 1.5\nsilence_start: 59.25", 60000)
	if len(got) != 2 || got[0] != (silence{26250, 27750}) || got[1] != (silence{59250, 60000}) {
		t.Fatalf("unexpected pauses: %+v", got)
	}
}

func TestPreferPauseNearTarget(t *testing.T) {
	chunks := planChunks(100000, []silence{{24000, 25000}, {29500, 30500}, {57000, 58000}})
	if chunks[0].End != 30000 || chunks[0].Boundary != "silence" || chunks[1].End != 57500 {
		t.Fatalf("unexpected chunks: %+v", chunks)
	}
}

func TestChunkCoverage(t *testing.T) {
	for _, duration := range []int64{1, 7999, 30000, 40000, 40001, 41000, 60000, 81000, 600000} {
		for _, pauses := range [][]silence{nil, {{0, duration}}, {{19000, 21500}, {39800, 40800}, {59000, 61000}}} {
			chunks := planChunks(duration, pauses)
			end := int64(0)
			for i, c := range chunks {
				if c.Start != end || c.End <= c.Start || c.End-c.Start > 40000 {
					t.Fatalf("gap, overlap or invalid duration: %+v", chunks)
				}
				if i < len(chunks)-1 && c.End-c.Start < 20000 {
					t.Fatalf("short interior chunk: %+v", chunks)
				}
				end = c.End
			}
			if end != duration {
				t.Fatalf("lost tail: %d %+v", duration, chunks)
			}
			last := chunks[len(chunks)-1]
			if len(chunks) > 1 && last.End-last.Start < 8000 {
				t.Fatalf("tiny tail: %+v", chunks)
			}
		}
	}
}

func TestNoPauseForcedBoundary(t *testing.T) {
	chunks := planChunks(85000, nil)
	if chunks[0].End != 40000 || chunks[0].Boundary != "forced_limit" || chunks[len(chunks)-1].Boundary != "end_of_audio" {
		t.Fatalf("unexpected fallback: %+v", chunks)
	}
}

// Optional local integration test; never contacts a model provider.
func TestFFmpegPauseDetection(t *testing.T) {
	ffmpeg := os.Getenv("FFMPEG_PATH")
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	if _, err := exec.LookPath(ffmpeg); err != nil {
		t.Skip("FFmpeg not available")
	}
	audio := filepath.Join(t.TempDir(), "fixture.wav")
	err := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000:duration=65", "-af", "volume=0:enable='between(t,26,28)+between(t,55,57)'", "-ac", "1", "-c:a", "pcm_s16le", audio).Run()
	if err != nil {
		t.Fatal(err)
	}
	pauses, err := detectSilences(context.Background(), ffmpeg, audio, 65000)
	if err != nil {
		t.Fatal(err)
	}
	chunks := planChunks(65000, pauses)
	if chunks[0].Boundary != "silence" || chunks[0].End < 26000 || chunks[0].End > 28000 {
		t.Fatalf("did not use generated pause: %+v, %+v", pauses, chunks)
	}
}
