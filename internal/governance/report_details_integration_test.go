package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestReportExportUsesFixedVisualRunAndChecksRecording(t *testing.T) {
	f := setupDeletionFixture(t)
	ctx := context.Background()
	var reportID, visualID, jobID string
	if err := f.pool.QueryRow(ctx, `SELECT report_id::text FROM teaching.export_jobs WHERE id=$1`, f.exportID).Scan(&reportID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO teaching.analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,config_hash,pipeline_version,config_snapshot,status,request_sha256,finished_at,mode)
 SELECT session_id,media_asset_id,requested_by,'fixed-visual',input_sha256,config_hash,pipeline_version,'{}','succeeded',request_sha256,now(),'visual_only' FROM teaching.analysis_runs WHERE id=$1 RETURNING id::text`, f.runID).Scan(&visualID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts)VALUES($1,'video_analysis','succeeded',3)RETURNING id::text`, visualID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	result := map[string]any{"visual": map[string]any{"input_sha256": strings.Repeat("b", 64), "counts": []any{map[string]any{"timestamp_ms": 0, "visible_count": 1, "hand_raise_count": 1, "head_down_count": nil}}, "persons": []any{map[string]any{"track_id": "T0001", "observations": []any{map[string]any{"timestamp_ms": 0, "behaviors": map[string]any{"hand_raise": true, "head_down": nil}}}}}}, "sampling": map[string]any{"interval_ms": 500}}
	metadata, _ := json.Marshal(map[string]any{"result": result})
	if _, err := f.pool.Exec(ctx, `INSERT INTO teaching.job_artifacts(job_id,run_id,session_id,artifact_key,metadata) VALUES($1,$2,$3,'__result/video_analysis',$4)`, jobID, visualID, f.sessionID, metadata); err != nil {
		t.Fatal(err)
	}
	var summary []byte
	if err := f.pool.QueryRow(ctx, `SELECT ((metadata->'result')-'visual')||jsonb_build_object('visual',(metadata->'result'->'visual')-'persons') FROM teaching.job_artifacts WHERE job_id=$1`, jobID).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(summary)
	fixed, _ := json.Marshal(map[string]any{"report": map[string]any{"classroom_context": map[string]any{"visual_run_id": visualID, "visual_result_sha256": hex.EncodeToString(hash[:]), "academic": map[string]string{"course_name": "固定历史课程名"}}}})
	if _, err := f.pool.Exec(ctx, `UPDATE teaching.analysis_runs SET config_snapshot=$2 WHERE id=$1`, f.runID, fixed); err != nil {
		t.Fatal(err)
	}
	d, err := f.service.reportDetails(ctx, reportID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Visual == nil || len(d.Visual.Visual.Persons) != 1 || d.Academic["course_name"] != "固定历史课程名" {
		t.Fatalf("wrong fixed projection: %+v", d)
	}
	// A later run must not be chosen in place of the one reviewed for this report.
	if _, err = f.pool.Exec(ctx, `INSERT INTO teaching.analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,config_hash,pipeline_version,config_snapshot,status,request_sha256,finished_at,mode)
 SELECT session_id,media_asset_id,requested_by,'newer-unrelated-visual',input_sha256,config_hash,pipeline_version,'{}','succeeded',request_sha256,now(),'visual_only' FROM teaching.analysis_runs WHERE id=$1`, f.runID); err != nil {
		t.Fatal(err)
	}
	if d, err = f.service.reportDetails(ctx, reportID); err != nil || d.Visual == nil {
		t.Fatalf("fixed result replaced by latest: %v", err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE teaching.analysis_runs SET input_sha256=$2 WHERE id=$1`, visualID, strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	d, err = f.service.reportDetails(ctx, reportID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Visual != nil || !strings.Contains(d.VisualNote, "不可用") {
		t.Fatal("cross-recording result was exported")
	}
}
