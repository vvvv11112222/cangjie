package analysis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestASRSecondsReservationRoundsEachChunkUp(t *testing.T) {
	c := ASRSnapshot{BillingMode: "seconds", PriceSecondMicros: 220}
	for _, row := range []struct{ ms, want int64 }{{1, 220}, {1000, 220}, {1001, 440}, {40000, 8800}} {
		got, err := c.ReserveMicros(row.ms)
		if err != nil || got != row.want {
			t.Fatalf("%dms: %d, %v", row.ms, got, err)
		}
	}
	for _, ms := range []int64{0, -1, 40001} {
		if _, err := c.ReserveMicros(ms); err == nil {
			t.Fatal("invalid duration accepted")
		}
	}
}

func TestASRSecondsConfigRequiresOwnPrice(t *testing.T) {
	settings := map[string]string{"ASR_CLOUD_ENABLED": "true", "ASR_API_BASE": "https://models.example/v1", "ASR_API_KEY": "synthetic", "ASR_MODEL_NAME": "synthetic", "ASR_MODEL_REVISION": "v1", "ASR_PRICE_VERSION": "synthetic", "ASR_BILLING_MODE": "seconds", "ASR_PRICE_PER_SECOND_MICROS": "220", "ASR_MAX_INPUT_TOKENS": "32768", "ASR_MAX_OUTPUT_TOKENS": "4096"}
	load := func() (CloudASRConfig, error) { return LoadCloudASR(func(key string) string { return settings[key] }) }
	c, err := load()
	if err != nil || c.PriceSecondMicros != 220 || c.InputPriceMicros != 0 {
		t.Fatalf("seconds config %+v %v", c, err)
	}
	for _, bad := range []string{"", "0", "-1", "0.00022", "1000000001"} {
		settings["ASR_PRICE_PER_SECOND_MICROS"] = bad
		if _, err := load(); err == nil {
			t.Fatal("invalid second price accepted")
		}
	}
	settings["ASR_PRICE_PER_SECOND_MICROS"] = "220"
	settings["ASR_BILLING_MODE"] = "minutes"
	if _, err := load(); err == nil {
		t.Fatal("unknown billing mode accepted")
	}
}

func TestASRSecondsNeverUsesTokensAsDuration(t *testing.T) {
	for _, usage := range []string{`{"prompt_tokens":25,"completion_tokens":2}`, `{"seconds":0}`, `{"seconds":-1}`, `{"seconds":1.2}`, `{"seconds":86401}`, `{"seconds":2}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"id":"synthetic","usage":` + usage + `,"choices":[{"finish_reason":"stop","message":{"content":"合成文本"}}]}`))
		}))
		s := &Service{cfg: Config{CloudASR: testCloudConfig(server.URL)}}
		_, result, _ := s.callASRBilled(context.Background(), server.URL, []byte(`{}`), true)
		server.Close()
		if result.UsageKnown != (usage == `{"seconds":2}`) {
			t.Fatalf("usage inferred incorrectly: %s %+v", usage, result)
		}
	}
}

func TestASRSecondsBillingAndUnknownReplay(t *testing.T) {
	for _, scenario := range []string{"success", "missing-seconds", "budget"} {
		t.Run(scenario, func(t *testing.T) {
			f := setupReportFixture(t)
			s, j := prepareBillingReport(t, f)
			s.cfg.ASRTimeout = 2 * time.Minute
			ctx := context.Background()
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				usage := `{"seconds":2}`
				if scenario == "missing-seconds" {
					usage = `{"prompt_tokens":50,"completion_tokens":2}`
				}
				w.Write([]byte(`{"id":"seconds-test","usage":` + usage + `,"choices":[{"finish_reason":"stop","message":{"content":"合成转写"}}]}`))
			}))
			defer provider.Close()
			s.cfg.CloudASR = testCloudConfig(provider.URL)
			s.cfg.CloudASR.BillingMode = "seconds"
			s.cfg.CloudASR.PriceSecondMicros = 220
			snap, _ := json.Marshal(s.newSnapshot("full"))
			if _, err := f.pool.Exec(ctx, `UPDATE teaching.analysis_runs SET config_snapshot=$2 WHERE id=$1`, f.run, snap); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(ctx, `UPDATE teaching.analysis_jobs SET stage='audio_analysis' WHERE id=$1`, j.ID); err != nil {
				t.Fatal(err)
			}
			if scenario == "budget" {
				s.cfg.ReportMonthlyBudgetMicros = 439
			}
			in := ASRChunk{StartMS: 0, EndMS: 1001, WAVBase64: base64.StdEncoding.EncodeToString(testWAV(1001))}
			_, err := s.TranscribeChunk(ctx, j.ID, j.Token, in)
			if scenario == "success" {
				if err != nil {
					t.Fatal(err)
				}
				var cost string
				var tokensNull bool
				if err = f.pool.QueryRow(ctx, `SELECT actual_cost::text,input_tokens IS NULL AND output_tokens IS NULL FROM teaching.model_calls WHERE job_id=$1`, j.ID).Scan(&cost, &tokensNull); err != nil || cost != "0.000440" || !tokensNull {
					t.Fatalf("cost=%s tokensNull=%v %v", cost, tokensNull, err)
				}
				var seconds int
				if err = f.pool.QueryRow(ctx, `SELECT (metadata->>'billed_seconds')::int FROM teaching.audit_logs WHERE action='model.asr.settle' AND resource_id=$1`, f.run).Scan(&seconds); err != nil || seconds != 2 {
					t.Fatalf("duration receipt %d %v", seconds, err)
				}
				if _, err = s.TranscribeChunk(ctx, j.ID, j.Token, in); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unsafe ASR accepted")
			}
			if scenario == "missing-seconds" {
				if _, err = s.TranscribeChunk(ctx, j.ID, j.Token, in); err == nil {
					t.Fatal("unknown paid request retried")
				}
			}
			want := int32(1)
			if scenario == "budget" {
				want = 0
			}
			if calls.Load() != want {
				t.Fatalf("provider calls=%d want=%d", calls.Load(), want)
			}
		})
	}
}
