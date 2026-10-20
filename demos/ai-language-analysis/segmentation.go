package main

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
)

type silence struct{ start, end int64 }

// All intervals are contiguous, non-overlapping milliseconds on the source timeline.
type AudioChunk struct {
	Start    int64  `json:"start_ms"`
	End      int64  `json:"end_ms"`
	Boundary string `json:"boundary_reason"`
}

var silenceEvent = regexp.MustCompile(`silence_(start|end):\s*([0-9]+(?:\.[0-9]+)?)`)

func parseSilences(log string, duration int64) []silence {
	var spans []silence
	start := int64(-1)
	for _, m := range silenceEvent.FindAllStringSubmatch(log, -1) {
		n, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		ms := min(duration, int64(math.Round(n*1000)))
		if m[1] == "start" {
			start = ms
			continue
		}
		if start >= 0 && ms > start {
			spans = append(spans, silence{start, ms})
		}
		start = -1
	}
	if start >= 0 && start < duration {
		spans = append(spans, silence{start, duration})
	}
	return spans
}

func detectSilences(ctx context.Context, executable, audio string, duration int64) ([]silence, error) {
	out, err := exec.CommandContext(ctx, executable, "-nostdin", "-hide_banner", "-nostats", "-v", "info", "-i", audio, "-af", "silencedetect=noise=-35dB:d=0.5", "-f", "null", "-").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("停顿检测失败: %w", err)
	}
	return parseSilences(string(out), duration), nil
}

func planChunks(duration int64, pauses []silence) []AudioChunk {
	chunks := []AudioChunk{}
	for start := int64(0); start < duration; {
		if duration-start <= 40000 {
			chunks = append(chunks, AudioChunk{start, duration, "end_of_audio"})
			break
		}
		// Leave at least eight seconds at the end, avoiding a tiny final request.
		low, high, target := start+20000, min(start+40000, duration-8000), start+30000
		end, reason := high, "forced_limit"
		best := int64(math.MaxInt64)
		for _, p := range pauses {
			if p.end-p.start < 500 {
				continue
			}
			left, right := max(low, p.start), min(high, p.end)
			if left > right {
				continue
			}
			candidate := max(left, min(right, (p.start+p.end)/2))
			distance := candidate - target
			if distance < 0 {
				distance = -distance
			}
			if distance < best {
				best, end, reason = distance, candidate, "silence"
			}
		}
		chunks = append(chunks, AudioChunk{start, end, reason})
		start = end
	}
	return chunks
}
