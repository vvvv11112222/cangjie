package analysis

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReportInputManifestMatchesBoundedRequest(t *testing.T) {
	for name, text := range map[string]string{"chinese": strings.Repeat("中", 512), "escaped": strings.Repeat("<\\\"中", 20000)} {
		t.Run(name, func(t *testing.T) {
			f := setupReportFixture(t)
			s, j := prepareBillingReport(t, f)
			ctx := context.Background()
			if _, err := f.pool.Exec(ctx, `UPDATE teaching.evidence_items SET description=$2 WHERE id=$1`, f.evidence, text); err != nil {
				t.Fatal(err)
			}
			s.cfg.ReportMaxInputTokens = 256
			snapshot, _ := json.Marshal(s.newSnapshot("full"))
			if _, err := f.pool.Exec(ctx, `UPDATE teaching.analysis_runs SET config_snapshot=$2 WHERE id=$1`, f.run, snapshot); err != nil {
				t.Fatal(err)
			}
			var sent []byte
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"id":"bounded","usage":{"prompt_tokens":17,"completion_tokens":5},"choices":[]}`))
			}))
			s.cfg.ReportAPIBase = provider.URL
			err := s.executeReport(ctx, j)
			provider.Close()
			if err == nil || len(sent) == 0 || len(sent) > 256*8 || !json.Valid(sent) {
				t.Fatalf("bounded execution: bytes=%d err=%v", len(sent), err)
			}
			var requestSHA string
			var manifest []byte
			if err = f.pool.QueryRow(ctx, `SELECT request_sha256 FROM teaching.model_calls WHERE job_id=$1`, j.ID).Scan(&requestSHA); err != nil || requestSHA != digestBytes(sent) {
				t.Fatalf("request digest mismatch: %v", err)
			}
			if err = f.pool.QueryRow(ctx, `SELECT metadata->'model_input' FROM teaching.job_artifacts WHERE job_id=$1 AND artifact_key='__model_input'`, j.ID).Scan(&manifest); err != nil {
				t.Fatal(err)
			}
			var input modelInput
			if err = json.Unmarshal(manifest, &input); err != nil {
				t.Fatal(err)
			}
			if len(input.Evidence) != 1 || input.Evidence[0].TextContent == text || input.Evidence[0].TextSHA256 != digestBytes([]byte(input.Evidence[0].TextContent)) || !strings.Contains(strings.Join(input.Limitations, ","), "MODEL_INPUT_TRUNCATED:") {
				t.Fatalf("manifest not truncated consistently: %+v", input)
			}
		})
	}
}

func TestReportExecutionSettlesMalformedCandidateBill(t *testing.T) {
	for _, choices := range []string{`[]`, `{}`, `[{"message":{}}]`, `[{"message":{"content":17}}]`, `[{"message":{"content":"not-json"}}]`} {
		t.Run(choices, func(t *testing.T) {
			f := setupReportFixture(t)
			s, j := prepareBillingReport(t, f)
			requests := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				_, _ = w.Write([]byte(`{"id":"execution-bill","usage":{"prompt_tokens":17,"completion_tokens":5},"choices":` + choices + `}`))
			}))
			defer provider.Close()
			s.cfg.ReportAPIBase = provider.URL
			if err := s.executeReport(context.Background(), j); err == nil {
				t.Fatal("invalid candidate succeeded")
			}
			var state, providerID, cost string
			var in, out int64
			err := f.pool.QueryRow(context.Background(), `SELECT status,provider_request_id,actual_cost::text,input_tokens,output_tokens FROM teaching.model_calls WHERE job_id=$1`, j.ID).Scan(&state, &providerID, &cost, &in, &out)
			if err != nil || state != "succeeded" || providerID != "execution-bill" || cost != "0.000022" || in != 17 || out != 5 || requests != 1 {
				t.Fatalf("known bill not settled: %s %s %s %d/%d requests=%d err=%v", state, providerID, cost, in, out, requests, err)
			}
			var candidates int
			if err = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM teaching.job_artifacts WHERE job_id=$1 AND artifact_key='__report_candidate'`, j.ID).Scan(&candidates); err != nil || candidates != 0 {
				t.Fatalf("bad candidate persisted: %d %v", candidates, err)
			}
		})
	}
}

func TestLateProviderBillSurvivesDeletedJob(t *testing.T) {
	f := setupReportFixture(t)
	s, j := prepareBillingReport(t, f)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `INSERT INTO teaching.model_calls(run_id,job_id,call_key,provider,model_name,prompt_version,status,reserved_cost,billing_period,price_version,request_sha256,dispatch_started_at) VALUES($1,$2,$3,'fixture','fixture','p0-v1','unknown',1,'2026-10-01','v1',$4,now())`, f.run, f.job, "report:"+f.job, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE teaching.model_calls SET run_id=NULL,job_id=NULL WHERE job_id=$1`, f.job); err != nil {
		t.Fatal(err)
	}
	if err := s.settleModelCall(ctx, j.ID, modelCallResult{UsageKnown: true, InputTokens: 17, OutputTokens: 5, ProviderRequestID: "late-bill"}, 22); err != nil {
		t.Fatalf("detached call lost known bill: %v", err)
	}
	var status, id, cost string
	if err := f.pool.QueryRow(ctx, `SELECT status,provider_request_id,actual_cost::text FROM teaching.model_calls WHERE call_key=$1`, "report:"+f.job).Scan(&status, &id, &cost); err != nil || status != "succeeded" || id != "late-bill" || cost != "0.000022" {
		t.Fatalf("bill %s %s %s %v", status, id, cost, err)
	}
	if err := s.settleModelCall(ctx, j.ID, modelCallResult{UsageKnown: true, ProviderRequestID: "stale"}, 0); err == nil {
		t.Fatal("already settled bill overwritten")
	}
}
func billingTestConfig() Config {
	return Config{MediaJobAttempts: 3, Lease: time.Minute, GoStageTimeout: 2 * time.Minute, ProcessorVersion: "synthetic", FFmpegSHA256: strings.Repeat("a", 64), ASRModelName: "fixture", ASRModelRevision: "v1", ASRDevice: "cpu", KeyframeIntervalMS: 1000, MaxKeyframes: 10, MaxMediaDurationMS: 60000, MaxVideoHeight: 1080, ReportEnabled: true, ReportModel: "fixture", ReportModelRevision: "v1", ReportPromptVersion: "p0-v1", ReportPromptSHA256: digestBytes([]byte(reportSystemPrompt)), ReportSelectionVersion: "selection-v1", ReportPriceVersion: "price-v1", ReportBudgetCurrency: "CNY", ReportBudgetTimezone: "Asia/Shanghai", ReportMaxInputTokens: 8000, ReportMaxOutputTokens: 1000, ReportInputPriceMicros: 1_000_000, ReportOutputPriceMicros: 1_000_000, ReportMonthlyBudgetMicros: 100_000_000}
}
func prepareBillingReport(t *testing.T, f reportFixture) (*Service, goJob) {
	ctx := context.Background()
	s := NewService(f.pool, f.store, billingTestConfig())
	snap := s.newSnapshot("full")
	raw, _ := json.Marshal(snap)
	var revision string
	err := f.pool.QueryRow(ctx, `INSERT INTO teaching.transcript_revisions(session_id,media_asset_id,source_run_id,revision_no,source_type,content_sha256,created_by,reason) VALUES($1,$2,$3,1,'asr',$4,$5,'Synthetic ASR') RETURNING id::text`, f.session, f.media, f.run, strings.Repeat("a", 64), f.admin.UserID).Scan(&revision)
	if err != nil {
		t.Fatal(err)
	}
	var ejob string
	err = f.pool.QueryRow(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts) VALUES($1,'evidence','succeeded',3) RETURNING id::text`, f.run).Scan(&ejob)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `INSERT INTO teaching.job_artifacts(job_id,run_id,session_id,lease_token,artifact_key,metadata) VALUES($1,$2,$3,gen_random_uuid(),'__result/evidence',jsonb_build_object('transcript_revision_id',$4::text))`, ejob, f.run, f.session, revision); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE teaching.analysis_runs SET status='running',finished_at=NULL,config_snapshot=$2 WHERE id=$1`, f.run, raw); err != nil {
		t.Fatal(err)
	}
	j := goJob{ID: f.job, RunID: f.run, SessionID: f.session, MediaID: f.media, Stage: "report", Token: "11111111-1111-4111-8111-111111111111"}
	if _, err = f.pool.Exec(ctx, `UPDATE teaching.analysis_jobs SET status='running',progress=0,worker_id='go:reporting',lease_token=$2,lease_expires_at=now()+interval '1 minute',execution_deadline_at=now()+interval '2 minute',expected_execution_sha256=$3 WHERE id=$1`, f.job, j.Token, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	return s, j
}
