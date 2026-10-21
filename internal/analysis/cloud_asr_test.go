package analysis

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cangjie-demo/integration/asrrequest"
)

func testWAV(ms int64) []byte {
	b := make([]byte, 44+ms*32)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(ms*32))
	return b
}
func TestASRWAVRejectsWrongDurationAndFormat(t *testing.T) {
	b := testWAV(1000)
	if err := validateASRWAV(b, 1000); err != nil {
		t.Fatal(err)
	}
	if validateASRWAV(b, 999) == nil || validateASRWAV(b, 40001) == nil {
		t.Fatal("accepted wrong duration")
	}
	binary.LittleEndian.PutUint32(b[24:], 8000)
	if validateASRWAV(b, 1000) == nil {
		t.Fatal("accepted wrong sample rate")
	}
}
func testCloudConfig(base string) CloudASRConfig {
	return CloudASRConfig{Enabled: true, APIKey: "synthetic-key", ASRSnapshot: ASRSnapshot{APIBase: base, LanguageHints: "zh,en", PriceVersion: "synthetic", RequestVersion: asrrequest.Version, InputPriceMicros: 1000000, OutputPriceMicros: 1000000, MaxInputTokens: 32768, MaxOutputTokens: 4096}}
}

func TestCloudASRBoundaries(t *testing.T) {
	for _, scenario := range []string{"replay", "unknown", "denied", "budget", "revoked-late", "deadline"} {
		t.Run(scenario, func(t *testing.T) {
			f := setupReportFixture(t)
			s, j := prepareBillingReport(t, f)
			s.cfg.ASRTimeout = 2 * time.Minute
			ctx := context.Background()
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if scenario == "revoked-late" {
					if _, err := f.pool.Exec(ctx, `UPDATE teaching.source_records SET external_processing_allowed=false WHERE id=$1`, f.source); err != nil {
						t.Error(err)
					}
				}
				usage := `,"usage":{"prompt_tokens":10,"completion_tokens":2}`
				if scenario == "unknown" {
					usage = ""
				}
				_, _ = w.Write([]byte(`{"id":"synthetic-asr","choices":[{"finish_reason":"stop","message":{"content":"合成课堂转写"}}]` + usage + `}`))
			}))
			defer provider.Close()
			s.cfg.CloudASR = testCloudConfig(provider.URL)
			snap, _ := json.Marshal(s.newSnapshot("full"))
			if _, err := f.pool.Exec(ctx, `UPDATE teaching.analysis_runs SET config_snapshot=$2 WHERE id=$1`, f.run, snap); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(ctx, `UPDATE teaching.analysis_jobs SET stage='audio_analysis' WHERE id=$1`, j.ID); err != nil {
				t.Fatal(err)
			}
			if scenario == "denied" {
				_, _ = f.pool.Exec(ctx, `UPDATE teaching.source_records SET external_processing_allowed=false WHERE id=$1`, f.source)
			}
			if scenario == "budget" {
				s.cfg.ReportMonthlyBudgetMicros = 1
			}
			if scenario == "deadline" {
				if _, err := f.pool.Exec(ctx, `UPDATE teaching.analysis_jobs SET lease_expires_at=now()-interval '2 seconds',execution_deadline_at=now()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
					t.Fatal(err)
				}
			}
			in := ASRChunk{StartMS: 0, EndMS: 1000, WAVBase64: base64.StdEncoding.EncodeToString(testWAV(1000))}
			out, err := s.TranscribeChunk(ctx, j.ID, j.Token, in)
			if scenario == "replay" {
				if err != nil || out.Text != "合成课堂转写" {
					t.Fatalf("ASR %v %v", out, err)
				}
				if _, err = s.TranscribeChunk(ctx, j.ID, j.Token, in); err != nil {
					t.Fatal(err)
				}
				changed := testWAV(1000)
				changed[len(changed)-1] = 1
				in.WAVBase64 = base64.StdEncoding.EncodeToString(changed)
				if _, err = s.TranscribeChunk(ctx, j.ID, j.Token, in); err == nil {
					t.Fatal("changed content replay accepted")
				}
				// A lease retry reuses successful chunks before submitting new ones.
				if _, err = s.Fail(ctx, j.ID, j.Token, Fail{ErrorCode: "PROCESSING_FAILED", Message: "synthetic interrupted worker", Retryable: true}); err != nil {
					t.Fatal(err)
				}
				if _, err = f.pool.Exec(ctx, `UPDATE teaching.analysis_jobs SET available_at=now() WHERE id=$1`, j.ID); err != nil {
					t.Fatal(err)
				}
				claim, err := s.Claim(ctx, "synthetic", []string{"audio_analysis"}, false)
				if err != nil || claim == nil || claim.JobID != j.ID {
					t.Fatalf("reclaim: %v %v", claim, err)
				}
				j.Token = claim.LeaseToken
				in.WAVBase64 = base64.StdEncoding.EncodeToString(testWAV(1000))
				if _, err = s.TranscribeChunk(ctx, j.ID, j.Token, in); err != nil {
					t.Fatal(err)
				}
				in.StartMS = 1000
				in.EndMS = 2000
				if _, err = s.TranscribeChunk(ctx, j.ID, j.Token, in); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unsafe ASR request accepted")
			}
			if scenario == "unknown" {
				if _, err = s.TranscribeChunk(ctx, j.ID, j.Token, in); err == nil {
					t.Fatal("unknown request repeated")
				}
			}
			want := int32(1)
			if scenario == "replay" {
				want = 2
			}
			if scenario == "denied" || scenario == "budget" || scenario == "deadline" {
				want = 0
			}
			if calls.Load() != want {
				t.Fatalf("provider calls=%d want=%d", calls.Load(), want)
			}
			if want > 0 {
				var status string
				if err = f.pool.QueryRow(ctx, `SELECT status FROM teaching.model_calls WHERE job_id=$1 ORDER BY created_at LIMIT 1`, j.ID).Scan(&status); err != nil {
					t.Fatal(err)
				}
				expected := "succeeded"
				if scenario == "unknown" {
					expected = "unknown"
				}
				if status != expected {
					t.Fatalf("bill %s", status)
				}
			}
			if scenario == "revoked-late" {
				var n int
				_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM teaching.job_artifacts WHERE job_id=$1 AND artifact_key LIKE '__asr/%'`, j.ID).Scan(&n)
				if n != 0 {
					t.Fatal("late text accepted after revoke")
				}
			}
		})
	}
}

func TestCloudASRToMainReportPipeline(t *testing.T) {
	f := setupReportFixture(t)
	ctx := context.Background()
	cfg := billingTestConfig()
	cfg.ReportModel = "synthetic-report"
	cfg.ProbeTimeout = time.Minute
	cfg.ASRTimeout = time.Minute
	cfg.VideoTimeout = time.Minute
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		content := "合成课堂：解释矩阵加法的定义。"
		if request.Model == cfg.ReportModel {
			var body string
			_ = json.Unmarshal(request.Messages[1].Content, &body)
			var input modelInput
			_ = json.Unmarshal([]byte(body), &input)
			if len(input.Evidence) == 0 {
				t.Error("report lacks ASR evidence")
				http.Error(w, "missing evidence", 500)
				return
			}
			id := input.Evidence[0].ID
			candidate := ReportCandidate{Summary: "合成说明", SummaryEvidenceIDs: []string{id}, Dimensions: []Dimension{}, Observations: []CandidateObservation{{DimensionCode: "content", ObservationType: "observation", ObservationText: "解释矩阵加法", EvidenceIDs: []string{id}}}}
			for _, code := range dimensionCodes {
				candidate.Dimensions = append(candidate.Dimensions, Dimension{DimensionCode: code, CoverageStatus: "insufficient", Limitation: "合成样本不足", Coverage: []Interval{}, SummaryEvidenceIDs: []string{}})
			}
			b, _ := json.Marshal(candidate)
			content = string(b)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-provider", "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}, "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content}}}})
	}))
	defer provider.Close()
	cfg.ReportAPIBase = provider.URL
	cfg.CloudASR = testCloudConfig(provider.URL)
	s := NewService(f.pool, f.store, cfg)
	run, _, err := s.Create(ctx, f.admin, f.session, "cloud-full", CreateRun{MediaAssetID: f.media, Mode: "full", ConfigProfile: "p0-v1"})
	if err != nil {
		t.Fatal(err)
	}
	complete := func(c *Claim, result any) {
		t.Helper()
		b, _ := json.Marshal(result)
		if _, err := s.Complete(ctx, c.JobID, c.LeaseToken, b); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(stage string) *Claim {
		t.Helper()
		c, e := s.Claim(ctx, "synthetic", []string{stage}, false)
		if e != nil || c == nil {
			t.Fatalf("claim %s: %v", stage, e)
		}
		return c
	}
	env := func(c *Claim) resultEnvelope {
		return resultEnvelope{SchemaVersion: SchemaVersion, JobID: c.JobID, RunID: c.RunID, SessionID: c.SessionID, MediaAssetID: c.MediaAssetID, Stage: c.Stage, Execution: c.Execution}
	}
	p := claim("probe")
	complete(p, ProbeResult{resultEnvelope: env(p), DurationMS: 1000, Width: 160, Height: 120, VideoCodec: "h264", HasAudio: true, PlaybackAssetID: f.media})
	a := claim("audio_analysis")
	out, err := s.TranscribeChunk(ctx, a.JobID, a.LeaseToken, ASRChunk{EndMS: 1000, WAVBase64: base64.StdEncoding.EncodeToString(testWAV(1000))})
	if err != nil {
		t.Fatal(err)
	}
	complete(a, AudioResult{resultEnvelope: env(a), Model: *a.Execution.ASRModel, Segments: []Segment{{SegmentNo: 0, StartMS: 0, EndMS: 1000, Text: out.Text, Speaker: "unknown"}}, Coverage: []Interval{{EndMS: 1000}}, Limitations: []string{"合成端到端测试，不代表真实准确率"}})
	v := claim("video_analysis")
	complete(v, VideoResult{resultEnvelope: env(v), Frames: []Frame{}, Events: []json.RawMessage{}, Coverage: []Interval{}, Limitations: []string{"合成视频结果"}, Sampling: struct {
		IntervalMS int `json:"interval_ms"`
		MaxFrames  int `json:"max_frames"`
	}{IntervalMS: cfg.KeyframeIntervalMS, MaxFrames: cfg.MaxKeyframes}})
	for i := 0; i < 3; i++ {
		did, e := s.ProcessNextGoJob(ctx)
		if e != nil || !did {
			t.Fatalf("pipeline step %d: %v %v", i, did, e)
		}
	}
	var status string
	var reports, segments int
	if err = f.pool.QueryRow(ctx, `SELECT status FROM teaching.analysis_runs WHERE id=$1`, run.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM teaching.reports WHERE run_id=$1`, run.ID).Scan(&reports)
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM teaching.transcript_segments WHERE run_id=$1`, run.ID).Scan(&segments)
	if status != "succeeded" || reports != 1 || segments != 1 {
		t.Fatalf("status=%s reports=%d transcript=%d", status, reports, segments)
	}
}

func TestCloudConfigRejectsRemoteHTTP(t *testing.T) {
	_, err := LoadCloudASR(func(k string) string {
		if k == "ASR_CLOUD_ENABLED" {
			return "true"
		}
		if k == "ASR_API_BASE" {
			return "http://untrusted.example"
		}
		return strings.Repeat("x", 1)
	})
	if err == nil {
		t.Fatal("accepted remote cleartext ASR endpoint")
	}
}

func TestCloudASRMalformedContentStillSettlesKnownUsage(t *testing.T) {
	for _, content := range []string{`17`, `null`, `"` + strings.Repeat("x", 4001) + `"`} {
		t.Run(content[:min(len(content), 12)], func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"id":"known-invalid","usage":{"prompt_tokens":7,"completion_tokens":2},"choices":[{"finish_reason":"stop","message":{"content":` + content + `}}]}`))
			}))
			defer provider.Close()
			s := &Service{cfg: Config{CloudASR: testCloudConfig(provider.URL)}}
			_, usage, err := s.callASR(context.Background(), provider.URL, []byte(`{}`))
			if err == nil || !usage.UsageKnown || usage.InputTokens != 7 || usage.OutputTokens != 2 {
				t.Fatalf("usage lost on malformed ASR content: %#v %v", usage, err)
			}
		})
	}
}

func TestASRMigrationPreservesSingleReportCall(t *testing.T) {
	f := setupReportFixture(t)
	ctx := context.Background()
	sql := `INSERT INTO teaching.model_calls(run_id,job_id,call_key,provider,model_name,prompt_version,reserved_cost) VALUES($1,$2,$3,'openai-compatible','synthetic-report','p0-v1',1)`
	if _, err := f.pool.Exec(ctx, sql, f.run, f.job, "synthetic-report-one"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, sql, f.run, f.job, "synthetic-report-two"); err == nil || !strings.Contains(err.Error(), "ux_call_job") {
		t.Fatalf("report one-call constraint lost: %v", err)
	}
}
