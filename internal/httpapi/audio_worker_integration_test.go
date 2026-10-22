package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
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
	"github.com/vvvv11112222/cangjie/internal/governance"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/media"
	"github.com/vvvv11112222/cangjie/internal/modelsettings"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

// Real Go + Python Runner + FFmpeg + original demo segmentation. Only the paid
// provider is substituted. It proves integration, not recognition accuracy.
func TestAudioWorkerMainPipeline(t *testing.T) {
	for _, mode := range []string{"tokens", "seconds"} {
		t.Run(mode, func(t *testing.T) { runAudioWorkerMainPipeline(t, mode == "seconds") })
	}
}

func runAudioWorkerMainPipeline(t *testing.T, secondsBilling bool) {
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
		command.Env = append(os.Environ(), "PYTHONUTF8=1", "WORKER_TOKEN=synthetic-audio-worker", "ASR_PLANNER="+planner, "ASR_MODEL_NAME=not-installed", "ASR_MODEL_REVISION=not-installed", "ASR_MODEL_FROM_CLAIM=1")
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
	systemPrompt, promptSHA, err := analysis.ReportPrompt(analysis.ComprehensivePromptVersion)
	if err != nil {
		t.Fatal(err)
	}
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
		usage := map[string]int{"prompt_tokens": 20, "completion_tokens": 10}
		if body.Model == "synthetic-asr" {
			callNo := asrCalls.Add(1)
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
			if secondsBilling {
				for offset := 12; offset+8 <= len(wav); {
					size := int(binary.LittleEndian.Uint32(wav[offset+4 : offset+8]))
					if size < 0 || offset+8+size > len(wav) {
						t.Error("invalid synthetic WAV")
						return
					}
					if string(wav[offset:offset+4]) == "data" {
						usage["seconds"] = (size + 31999) / 32000
						break
					}
					offset += 8 + size + size%2
				}
			}
			if len(wav) < 44+16000 {
				t.Error("missing audio")
				return
			}
			// This fixture's audio starts one second after video T0. Leading
			// silence must remain, rather than shifting speech to the origin.
			for _, b := range wav[44 : 44+16000] {
				if callNo != 1 {
					break
				}
				if b != 0 {
					t.Error("delayed audio shifted to video T0")
					break
				}
			}
		} else {
			reportCalls.Add(1)
			var actualPrompt string
			if json.Unmarshal(body.Messages[0].Content, &actualPrompt) != nil || actualPrompt != systemPrompt {
				t.Error("main audio flow did not use the bundled teacher report prompt")
			}
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
			candidate := analysis.ReportCandidate{Summary: "合成报告", SummaryEvidenceIDs: []string{id}, Dimensions: []analysis.Dimension{}, Observations: []analysis.CandidateObservation{{DimensionCode: "thinking", ObservationType: "observation", ObservationText: "教师要求解释定义；合成片段不能判断学生实际理解情况。", Suggestion: "下次可以请一名学生先解释，再让同伴补充一个矩阵加法例子，观察其是否能说明对应位置相加。", EvidenceIDs: []string{id}}}}
			for _, code := range []string{"content", "pace", "thinking", "expression", "management", "technology"} {
				candidate.Dimensions = append(candidate.Dimensions, analysis.Dimension{DimensionCode: code, CoverageStatus: "insufficient", Limitation: "合成素材不评价教学质量", Coverage: []analysis.Interval{}, SummaryEvidenceIDs: []string{}})
			}
			raw, _ := json.Marshal(candidate)
			text = string(raw)
		}
		writeJSON(w, 200, map[string]any{"id": "synthetic-local-provider", "usage": usage, "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": text}}}})
	}))
	defer provider.Close()
	store, err := storage.NewLocal(filepath.Join(tmp, "media"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := analysis.Config{Lease: 30 * time.Second, ProbeTimeout: time.Minute, ASRTimeout: time.Minute, VideoTimeout: time.Minute, GoStageTimeout: time.Minute, MediaJobAttempts: 2,
		ProcessorVersion: runtime["processor_version"].(string), FFmpegSHA256: runtime["ffmpeg_build_sha256"].(string), ASRModelName: "synthetic-asr", ASRModelRevision: "v1", ASRDevice: "cpu", KeyframeIntervalMS: 1000, MaxKeyframes: 100, MaxMediaDurationMS: 60000, MaxVideoHeight: 1080, MaxArtifactBytes: 10 << 20,
		ReportEnabled: true, ReportAPIBase: provider.URL, ReportAPIKey: "synthetic", ReportModel: "synthetic-report", ReportModelRevision: "v1", ReportPromptVersion: analysis.ComprehensivePromptVersion, ReportPromptSHA256: promptSHA, ReportSelectionVersion: "time-window-v1", ReportPriceVersion: "synthetic", ReportBudgetCurrency: "CNY", ReportBudgetTimezone: "Asia/Shanghai", ReportMonthlyBudgetMicros: 30000000, ReportInputPriceMicros: 1000000, ReportOutputPriceMicros: 1000000, ReportTimeout: time.Minute, ReportMaxInputTokens: 8000, ReportMaxOutputTokens: 1000,
		CloudASR: analysis.CloudASRConfig{Enabled: true, APIKey: "synthetic", ASRSnapshot: analysis.ASRSnapshot{APIBase: provider.URL, LanguageHints: "zh,en", RequestVersion: asrrequest.Version, PriceVersion: "synthetic", InputPriceMicros: 1000000, OutputPriceMicros: 1000000, MaxInputTokens: 32768, MaxOutputTokens: 4096}}}
	if secondsBilling {
		cfg.CloudASR.BillingMode = "seconds"
		cfg.CloudASR.PriceSecondMicros = 220
	}
	webValues := modelsettings.FromConfig(cfg)
	webValues.PriceSource = "Synthetic integration prices, no billed provider"
	cfg.CloudASR = analysis.CloudASRConfig{}
	cfg.ReportEnabled = false
	cfg.ReportAPIKey = ""
	cfg.ReportMonthlyBudgetMicros = 0
	cfg.ASRModelName = "not-installed"
	cfg.ASRModelRevision = "not-installed"
	svc := analysis.NewService(pool, store, cfg)
	if _, err = pool.Exec(ctx, `UPDATE teaching.runtime_model_settings SET revision=gen_random_uuid(),settings='{}',secret_revision=NULL,updated_by=NULL`); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `UPDATE teaching.runtime_model_settings SET revision=gen_random_uuid(),settings='{}',secret_revision=NULL,updated_by=NULL`)
	modelSettings, err := modelsettings.New(ctx, pool, svc, filepath.Join(tmp, "model-settings"))
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := governance.InitializeLedger(filepath.Join(tmp, "deletions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	gov := governance.NewService(pool, store, ledger, governance.Config{Lease: time.Minute, MaxAttempts: 3, ExportRetention: time.Hour, ExecutorID: "synthetic-audio-export"})
	server := httptest.NewServer(New(Options{ModelSettings: modelSettings, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Identity: identity.NewService(pool, 8*time.Hour), Academic: academic.NewService(pool), Classroom: classroom.NewService(pool), Media: media.NewService(pool, store, 10<<20, 14*24*time.Hour), Analysis: svc, Governance: gov, PublicOrigin: "http://frontend.test", WorkerID: "worker-01", WorkerToken: "synthetic-audio-worker", WorkerCapabilities: []string{"probe", "audio_analysis", "video_analysis"}}))
	defer server.Close()
	adminConfig := newTestClient(t)
	adminConfigCSRF := loginTestUser(t, adminConfig, server.URL, "admin")
	requestJSON(t, newTestClient(t), "GET", server.URL+"/api/v1/model-settings", nil, "", 401)
	requestJSON(t, adminConfig, "GET", server.URL+"/api/v1/model-settings?unexpected=1", nil, "", 400)
	initialSettings := requestJSON(t, adminConfig, "GET", server.URL+"/api/v1/model-settings", nil, "", 200)
	syntheticKey := "synthetic"
	webInput := modelsettings.Update{ExpectedRevision: initialSettings["revision"].(string), Values: webValues, ASRAPIKey: &syntheticKey, ReportAPIKey: &syntheticKey, PricingConfirmed: true, Reason: "Enable synthetic pipeline via web settings"}
	requestJSON(t, adminConfig, "PUT", server.URL+"/api/v1/model-settings", webInput, "invalid-csrf", 403)
	appliedSettings := requestJSON(t, adminConfig, "PUT", server.URL+"/api/v1/model-settings", webInput, adminConfigCSRF, 200)
	if appliedSettings["revision"] != appliedSettings["active_revision"] || asrCalls.Load() != 0 || reportCalls.Load() != 0 {
		t.Fatal("web configuration was not applied offline")
	}
	input := filepath.Join(tmp, "delayed.mp4")
	gen := exec.Command(runtime["ffmpeg_binary"].(string), "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration=45", "-itsoffset", "1", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=44", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", input)
	if raw, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v %s", err, raw)
	}
	video, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	client := newTestClient(t)
	csrf := loginTestUser(t, client, server.URL, "teacher")
	requestJSON(t, client, "GET", server.URL+"/api/v1/model-settings", nil, "", 403)
	requestJSON(t, client, "PUT", server.URL+"/api/v1/model-settings", webInput, csrf, 403)
	uploaded := requestMultipart(t, client, server.URL+"/api/v1/sessions/"+lesson+"/media", source, video, csrf, http.StatusCreated)
	run := requestWithHeaders(t, client, "POST", server.URL+"/api/v1/sessions/"+lesson+"/analysis-runs", map[string]any{"media_asset_id": uploaded["id"], "mode": "full", "config_profile": "p0-v1", "input_transcript_revision_id": nil}, csrf, map[string]string{"Idempotency-Key": "synthetic-real-audio"}, 202)
	id := run["id"].(string)
	webInput.ExpectedRevision = appliedSettings["revision"].(string)
	requestJSON(t, adminConfig, "PUT", server.URL+"/api/v1/model-settings", webInput, adminConfigCSRF, 409)
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
	if len(results["segments"].([]any)) != 2 || results["transcript_revision_id"] == nil {
		t.Fatal("ASR not stored in main transcript")
	}
	report := requestJSON(t, client, "GET", server.URL+"/api/v1/reports/"+finished["report_id"].(string), nil, "", 200)
	if report["status"] != "draft" || asrCalls.Load() != 2 || reportCalls.Load() != 1 {
		t.Fatalf("report=%s calls=%d/%d", report["status"], asrCalls.Load(), reportCalls.Load())
	}
	observations := report["observations"].([]any)
	if len(observations) != 1 || !strings.Contains(observations[0].(map[string]any)["suggestion"].(string), "对应位置相加") {
		t.Fatal("teacher-facing advice lost in the main-project report")
	}
	// Follow the actual cloud-generated draft through the existing public review
	// and export APIs; do not seed a pre-published report directly in the database.
	observation := observations[0].(map[string]any)
	if observation["review_status"] != "unreviewed" || len(report["dimensions"].([]any)) != 6 {
		t.Fatal("generated report must preserve six dimensions and require human review")
	}
	segments := map[any]map[string]any{}
	for _, raw := range results["segments"].([]any) {
		segment := raw.(map[string]any)
		segments[segment["start_ms"]] = segment
	}
	evidenceByID := map[any]map[string]any{}
	for _, raw := range results["evidence"].([]any) {
		evidence := raw.(map[string]any)
		evidenceByID[evidence["id"]] = evidence
	}
	if len(observation["evidence_ids"].([]any)) == 0 {
		t.Fatal("generated teaching advice requires evidence")
	}
	for _, evidenceID := range observation["evidence_ids"].([]any) {
		evidence := evidenceByID[evidenceID]
		segment := segments[evidence["start_ms"]]
		if evidence == nil || segment == nil || evidence["kind"] != "transcript" || evidence["transcript_segment_id"] == nil || evidence["description"] != segment["text_content"] || evidence["run_id"] != id || evidence["session_id"] != lesson || evidence["media_asset_id"] != uploaded["id"] || evidence["availability"] != "available" {
			t.Fatal("report must cite available transcript evidence from this upload and run")
		}
		start, end := evidence["start_ms"].(float64), evidence["end_ms"].(float64)
		if start < 0 || start >= end || end > 45000 || evidence["start_ms"] != segment["start_ms"] || evidence["end_ms"] != segment["end_ms"] {
			t.Fatal("report evidence must preserve transcript times on the source video timeline")
		}
	}
	reportURL := server.URL + "/api/v1/reports/" + report["id"].(string)
	requestWithHeaders(t, client, http.MethodPost, reportURL+"/exports", map[string]any{"format": "json"}, csrf, map[string]string{"Idempotency-Key": "unreviewed-audio-report"}, http.StatusConflict)
	report = requestJSON(t, client, http.MethodPost, reportURL+"/review", map[string]any{"lock_version": report["lock_version"], "action": "submit", "observation_id": nil, "content_sha256": nil, "reason": "合成报告提交复核"}, csrf, http.StatusOK)
	supervisor := newTestClient(t)
	supervisorCSRF := loginTestUser(t, supervisor, server.URL, "supervisor")
	requestJSON(t, supervisor, http.MethodPost, reportURL+"/publish", map[string]any{"lock_version": report["lock_version"], "expected_current_report_id": nil}, supervisorCSRF, http.StatusUnprocessableEntity)
	report = requestJSON(t, supervisor, http.MethodPost, reportURL+"/review", map[string]any{"lock_version": report["lock_version"], "action": "accept", "observation_id": observation["id"], "content_sha256": nil, "reason": "合成用例核对引用"}, supervisorCSRF, http.StatusOK)
	report = requestJSON(t, supervisor, http.MethodPost, reportURL+"/review", map[string]any{"lock_version": report["lock_version"], "action": "confirm_report", "observation_id": nil, "content_sha256": report["content_sha256"], "reason": "合成用例确认整篇"}, supervisorCSRF, http.StatusOK)
	requestJSON(t, client, http.MethodPost, reportURL+"/publish", map[string]any{"lock_version": report["lock_version"], "expected_current_report_id": nil}, csrf, http.StatusForbidden)
	published := requestJSON(t, supervisor, http.MethodPost, reportURL+"/publish", map[string]any{"lock_version": report["lock_version"], "expected_current_report_id": nil}, supervisorCSRF, http.StatusOK)
	if published["status"] != "published" {
		t.Fatal("reviewed audio report was not published")
	}
	exported := requestWithHeaders(t, client, http.MethodPost, reportURL+"/exports", map[string]any{"format": "json"}, csrf, map[string]string{"Idempotency-Key": "published-audio-report"}, http.StatusAccepted)
	replayed := requestWithHeaders(t, client, http.MethodPost, reportURL+"/exports", map[string]any{"format": "json"}, csrf, map[string]string{"Idempotency-Key": "published-audio-report"}, http.StatusOK)
	if replayed["id"] != exported["id"] {
		t.Fatal("export replay created a duplicate task")
	}
	if worked, err := gov.ProcessExport(ctx); err != nil || !worked {
		t.Fatalf("export generated audio report: worked=%v err=%v", worked, err)
	}
	exportURL := server.URL + "/api/v1/exports/" + exported["id"].(string)
	ready := requestJSON(t, client, http.MethodGet, exportURL, nil, "", http.StatusOK)
	if ready["status"] != "succeeded" || ready["download_url"] == nil {
		t.Fatal("published audio report export not ready")
	}
	response, err := client.Get(server.URL + ready["download_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Report               analysis.Report   `json:"report"`
		EvidenceAvailability map[string]string `json:"evidence_availability"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&snapshot)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || decodeErr != nil || snapshot.Report.ID != published["id"] || snapshot.Report.ContentSHA256 == nil || *snapshot.Report.ContentSHA256 != published["content_sha256"] || len(snapshot.Report.Dimensions) != 6 || len(snapshot.Report.Observations) != 1 {
		t.Fatalf("download must contain the fixed published report: status=%d err=%v", response.StatusCode, decodeErr)
	}
	if snapshot.Report.Observations[0].Suggestion != observation["suggestion"] || snapshot.Report.Observations[0].ReviewStatus != "accepted" || len(snapshot.Report.Observations[0].EvidenceIDs) == 0 {
		t.Fatal("download lost the reviewed teaching advice or its evidence references")
	}
	for _, evidenceID := range snapshot.Report.Observations[0].EvidenceIDs {
		if snapshot.EvidenceAvailability[evidenceID] != "available" || evidenceByID[evidenceID] == nil {
			t.Fatal("download refers to evidence outside the generated audio report")
		}
	}
	requestJSON(t, supervisor, http.MethodGet, exportURL+"/content", nil, "", http.StatusNotFound)
	if _, err := pool.Exec(ctx, `UPDATE teaching.source_records SET allowed_uses='["playback","analysis"]',rights_version=rights_version+1 WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, client, http.MethodGet, exportURL+"/content", nil, "", http.StatusGone)
	if asrCalls.Load() != 2 || reportCalls.Load() != 1 {
		t.Fatal("review, publication or export unexpectedly requested another model call")
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
	t.Log("synthetic real-worker upload -> delayed audio extraction -> demo segmentation -> Go cloud ASR -> transcript -> evidence -> draft -> review -> publish -> authorized JSON export; recognition quality not measured")
}

func TestASRChunkHTTPGuards(t *testing.T) {
	svc := analysis.NewService(nil, nil, analysis.Config{})
	for _, audioAllowed := range []bool{false, true} {
		caps := []string{"video_analysis"}
		if audioAllowed {
			caps = append(caps, "audio_analysis")
		}
		server := httptest.NewServer(New(Options{Analysis: svc, WorkerToken: "synthetic-worker", WorkerID: "worker-01", WorkerCapabilities: caps, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
		endpoint := server.URL + "/internal/v1/jobs/00000000-0000-4000-8000-000000000001/asr-chunks"
		if code := rawStatus(http.DefaultClient, "POST", endpoint, nil, nil); code != 401 {
			t.Fatalf("anonymous status %d", code)
		}
		headers := map[string]string{"Authorization": "Bearer synthetic-worker", "X-Lease-Token": "00000000-0000-4000-8000-000000000002"}
		want := 403
		if audioAllowed {
			want = 400
		}
		for _, payload := range []any{map[string]any{"end_ms": 1, "wav_base64": "AA=="}, map[string]any{"start_ms": 0, "end_ms": 1, "wav_base64": "AA==", "url": "https://unused.invalid"}} {
			if code := rawStatus(http.DefaultClient, "POST", endpoint, payload, headers); code != want {
				t.Fatalf("shape/capability status=%d want=%d", code, want)
			}
		}
		server.Close()
	}
}
