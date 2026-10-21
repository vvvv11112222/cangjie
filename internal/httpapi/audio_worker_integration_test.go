package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cangjie-demo/integration/asrrequest"
	"github.com/vvvv11112222/cangjie/internal/academic"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/classroom"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/media"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

// Real Go + Python Runner + FFmpeg + original demo segmentation. Only the paid
// provider is substituted. It proves integration, not recognition accuracy.
func TestAudioWorkerMainPipeline(t *testing.T) {
	python, planner := os.Getenv("WORKER_AUDIO_PYTHON"), os.Getenv("WORKER_AUDIO_PLANNER")
	if python == "" || planner == "" {
		t.Skip("WORKER_AUDIO_PYTHON/PLANNER not set")
	}
	pool, lesson, source := captureDatabaseFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE teaching.source_records SET allowed_uses='["playback","analysis","export"]',external_processing_allowed=true WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../..")
	tmp := t.TempDir()
	bridge := func(args ...string) map[string]any {
		t.Helper()
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(cctx, python, args...)
		command.Dir = root
		command.Env = append(os.Environ(), "PYTHONUTF8=1", "WORKER_TOKEN=synthetic-audio-worker", "ASR_PLANNER="+planner, "ASR_MODEL_NAME=synthetic-asr", "ASR_MODEL_REVISION=v1")
		var log bytes.Buffer
		command.Stderr = &log
		raw, err := command.Output()
		if err != nil {
			t.Fatalf("Python bridge %v: %v %s", args, err, log.String())
		}
		var result map[string]any
		if err = json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("bridge output: %s %v", raw, err)
		}
		return result
	}
	runtime := bridge("-m", "workers", "runtime-info")
	var asrCalls, reportCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		text := "合成课堂：请解释矩阵加法的定义。"
		if body.Model == "synthetic-asr" {
			asrCalls.Add(1)
			var content []struct {
				Audio struct {
					Data string `json:"data"`
				} `json:"input_audio"`
			}
			_ = json.Unmarshal(body.Messages[0].Content, &content)
			if len(content) != 1 || !strings.HasPrefix(content[0].Audio.Data, "data:audio/wav;base64,") {
				t.Error("not the delivered ASR request format")
				return
			}
			wav, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(content[0].Audio.Data, "data:audio/wav;base64,"))
			if len(wav) < 44+16000 {
				t.Error("missing audio")
				return
			}
			// This fixture's audio starts one second after video T0. Leading
			// silence must remain, rather than shifting speech to the origin.
			for _, b := range wav[44 : 44+16000] {
				if b != 0 {
					t.Error("delayed audio shifted to video T0")
					break
				}
			}
		} else {
			reportCalls.Add(1)
			var payload string
			_ = json.Unmarshal(body.Messages[1].Content, &payload)
			var input struct {
				Evidence []struct {
					ID   string `json:"id"`
					Kind string `json:"kind"`
					Text string `json:"text_content"`
				} `json:"evidence"`
			}
			_ = json.Unmarshal([]byte(payload), &input)
			id := ""
			for _, e := range input.Evidence {
				if e.Kind == "transcript" {
					id = e.ID
					if e.Text != text {
						t.Error("ASR text changed")
					}
					break
				}
			}
			if id == "" {
				t.Error("ASR evidence missing from report")
				return
			}
			candidate := analysis.ReportCandidate{Summary: "合成报告", SummaryEvidenceIDs: []string{id}, Dimensions: []analysis.Dimension{}, Observations: []analysis.CandidateObservation{{DimensionCode: "content", ObservationType: "observation", ObservationText: "教师要求解释定义", EvidenceIDs: []string{id}}}}
			for _, code := range []string{"content", "pace", "thinking", "expression", "management", "technology"} {
				candidate.Dimensions = append(candidate.Dimensions, analysis.Dimension{DimensionCode: code, CoverageStatus: "insufficient", Limitation: "合成素材不评价教学质量", Coverage: []analysis.Interval{}, SummaryEvidenceIDs: []string{}})
			}
			raw, _ := json.Marshal(candidate)
			text = string(raw)
		}
		writeJSON(w, 200, map[string]any{"id": "synthetic-local-provider", "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 10}, "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": text}}}})
	}))
	defer provider.Close()
	store, err := storage.NewLocal(filepath.Join(tmp, "media"))
	if err != nil {
		t.Fatal(err)
	}
	prompt, sha, _ := analysis.ReportPrompt("p0-v1")
	_ = prompt
	cfg := analysis.Config{Lease: 30 * time.Second, ProbeTimeout: time.Minute, ASRTimeout: time.Minute, VideoTimeout: time.Minute, GoStageTimeout: time.Minute, MediaJobAttempts: 2,
		ProcessorVersion: runtime["processor_version"].(string), FFmpegSHA256: runtime["ffmpeg_build_sha256"].(string), ASRModelName: "synthetic-asr", ASRModelRevision: "v1", ASRDevice: "cpu", KeyframeIntervalMS: 1000, MaxKeyframes: 10, MaxMediaDurationMS: 60000, MaxVideoHeight: 1080, MaxArtifactBytes: 10 << 20,
		ReportEnabled: true, ReportAPIBase: provider.URL, ReportAPIKey: "synthetic", ReportModel: "synthetic-report", ReportModelRevision: "v1", ReportPromptVersion: "p0-v1", ReportPromptSHA256: sha, ReportSelectionVersion: "time-window-v1", ReportPriceVersion: "synthetic", ReportBudgetCurrency: "CNY", ReportBudgetTimezone: "Asia/Shanghai", ReportMonthlyBudgetMicros: 100000000, ReportInputPriceMicros: 1000000, ReportOutputPriceMicros: 1000000, ReportTimeout: time.Minute, ReportMaxInputTokens: 8000, ReportMaxOutputTokens: 1000,
		CloudASR: analysis.CloudASRConfig{Enabled: true, APIKey: "synthetic", ASRSnapshot: analysis.ASRSnapshot{APIBase: provider.URL, LanguageHints: "zh,en", RequestVersion: asrrequest.Version, PriceVersion: "synthetic", InputPriceMicros: 1000000, OutputPriceMicros: 1000000, MaxInputTokens: 32768, MaxOutputTokens: 4096}}}
	svc := analysis.NewService(pool, store, cfg)
	server := httptest.NewServer(New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Identity: identity.NewService(pool, 8*time.Hour), Academic: academic.NewService(pool), Classroom: classroom.NewService(pool), Media: media.NewService(pool, store, 10<<20, 14*24*time.Hour), Analysis: svc, PublicOrigin: "http://frontend.test", WorkerID: "worker-01", WorkerToken: "synthetic-audio-worker", WorkerCapabilities: []string{"probe", "audio_analysis", "video_analysis"}}))
	defer server.Close()
	input := filepath.Join(tmp, "delayed.mp4")
	gen := exec.Command(runtime["ffmpeg_binary"].(string), "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration=3", "-itsoffset", "1", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", input)
	if raw, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v %s", err, raw)
	}
	video, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	client := newTestClient(t)
	csrf := loginTestUser(t, client, server.URL, "teacher")
	uploaded := requestMultipart(t, client, server.URL+"/api/v1/sessions/"+lesson+"/media", source, video, csrf, http.StatusCreated)
	run := requestWithHeaders(t, client, "POST", server.URL+"/api/v1/sessions/"+lesson+"/analysis-runs", map[string]any{"media_asset_id": uploaded["id"], "mode": "full", "config_profile": "p0-v1", "input_transcript_revision_id": nil}, csrf, map[string]string{"Idempotency-Key": "synthetic-real-audio"}, 202)
	id := run["id"].(string)
	for _, stage := range []string{"probe", "audio_analysis", "video_analysis"} {
		result := bridge("-m", "workers.tools.check_real_go", "work", "--api-base", server.URL+"/internal/v1", "--stage", stage, "--run-id", id, "--scratch", filepath.Join(tmp, "scratch"))
		t.Logf("real %s completed: %v seconds", stage, result["wall_seconds"])
	}
	for i := 0; i < 3; i++ {
		if did, err := svc.ProcessNextGoJob(ctx); err != nil || !did {
			t.Fatalf("Go stage: %v %v", did, err)
		}
	}
	finished := requestJSON(t, client, "GET", server.URL+"/api/v1/analysis-runs/"+id, nil, "", 200)
	if finished["status"] != "succeeded" || finished["report_id"] == nil {
		t.Fatalf("run: %#v", finished)
	}
	results := requestJSON(t, client, "GET", server.URL+"/api/v1/analysis-runs/"+id+"/results", nil, "", 200)
	if len(results["segments"].([]any)) != 1 || results["transcript_revision_id"] == nil {
		t.Fatal("ASR not stored in main transcript")
	}
	report := requestJSON(t, client, "GET", server.URL+"/api/v1/reports/"+finished["report_id"].(string), nil, "", 200)
	if report["status"] != "draft" || asrCalls.Load() != 1 || reportCalls.Load() != 1 {
		t.Fatalf("report=%s calls=%d/%d", report["status"], asrCalls.Load(), reportCalls.Load())
	}
	if status := rawStatus(http.DefaultClient, "POST", server.URL+"/internal/v1/jobs/00000000-0000-4000-8000-000000000001/asr-chunks", nil, nil); status != 401 {
		t.Fatalf("anonymous ASR status %d", status)
	}
	if entries, err := os.ReadDir(filepath.Join(tmp, "scratch")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				sub, _ := os.ReadDir(filepath.Join(tmp, "scratch", entry.Name()))
				if len(sub) > 0 {
					t.Fatal("Worker private media left after completion")
				}
			}
		}
	}
	t.Log("synthetic real-worker upload -> delayed audio extraction -> demo segmentation -> Go cloud ASR -> transcript -> evidence -> draft report; recognition quality not measured")
}
