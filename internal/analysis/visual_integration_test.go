package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"strings"
	"testing"
	"time"
)

// Dedicated TEST_DATABASE_URL only; shares the existing integration fixture lock.
func TestVisualLifecycleIntegration(t *testing.T) {
	f := setupReportFixture(t)
	ctx := context.Background()
	fixture, _ := visualFixture(t)
	cfg := Config{VisionEnabled: true, VisionModelRevision: fixture.Model.Revision, Lease: time.Minute, ProbeTimeout: time.Minute, VideoTimeout: time.Minute, MediaJobAttempts: 2, ProcessorVersion: "test-visual", FFmpegSHA256: strings.Repeat("f", 64), ASRDevice: "cpu", MaxKeyframes: 240, KeyframeIntervalMS: 30000, MaxMediaDurationMS: 7200000, MaxVideoHeight: 1080, MaxArtifactBytes: 1 << 20}
	svc := NewService(f.pool, f.store, cfg)
	create := func(key string) Run {
		r, _, err := svc.Create(ctx, f.admin, f.session, key, CreateRun{MediaAssetID: f.media, Mode: "visual_only", ConfigProfile: "vision-v1"})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	claim := func(stage string) *Claim {
		c, err := svc.Claim(ctx, "test-worker", []string{stage}, true)
		if err != nil || c == nil {
			t.Fatalf("claim: %v", err)
		}
		return c
	}
	envelope := func(c *Claim) resultEnvelope {
		return resultEnvelope{SchemaVersion: SchemaVersion, JobID: c.JobID, RunID: c.RunID, SessionID: c.SessionID, MediaAssetID: c.MediaAssetID, Stage: c.Stage, Execution: c.Execution}
	}
	complete := func(c *Claim, value any) CompleteResult {
		raw, _ := json.Marshal(value)
		v, err := svc.Complete(ctx, c.JobID, c.LeaseToken, raw)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	probe := func(c *Claim) {
		a, _, err := svc.UploadArtifact(ctx, c.JobID, c.LeaseToken, ArtifactInput{ArtifactKey: c.LeaseToken + "/proxy", Kind: "proxy"}, bytes.NewReader([]byte("synthetic mp4")))
		if err != nil {
			t.Fatal(err)
		}
		complete(c, ProbeResult{resultEnvelope: envelope(c), DurationMS: 2000, Width: 160, Height: 120, VideoCodec: "h264", PlaybackAssetID: a.AssetID, Limitations: []string{}})
	}
	r := create("visual-success")
	same, replay, err := svc.Create(ctx, f.admin, f.session, "visual-success", CreateRun{MediaAssetID: f.media, Mode: "visual_only", ConfigProfile: "vision-v1"})
	if err != nil || !replay || same.ID != r.ID {
		t.Fatal("create replay failed", err)
	}
	if _, err = svc.Visual(ctx, f.admin, r.ID, "summary", "", 0, 100); err == nil {
		t.Fatal("unfinished read accepted")
	}
	old, err := svc.Claim(ctx, "old-worker", []string{"probe", "video_analysis"})
	if err != nil || old != nil {
		t.Fatal("legacy worker claimed visual task", err)
	}
	probe(claim("probe"))
	c := claim("video_analysis")
	timestamp := int64(0)
	a, _, err := svc.UploadArtifact(ctx, c.JobID, c.LeaseToken, ArtifactInput{ArtifactKey: c.LeaseToken + "/frame", Kind: "keyframe", TimestampMS: &timestamp}, bytes.NewReader([]byte("synthetic jpeg")))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := visualFixture(t)
	v.resultEnvelope = envelope(c)
	v.Visual.InputSHA256 = c.InputSHA256
	v.Frames[0].AssetID = a.AssetID
	v.Visual.Persons[0].Observations[0].FrameAssetID = &a.AssetID
	v.Visual.Counts = append(v.Visual.Counts, VisualCount{TimestampMS: 500})
	second := v.Visual.Persons[0].Observations[0]
	second.TimestampMS = 500
	second.FrameAssetID = nil
	v.Visual.Persons[0].Observations = append(v.Visual.Persons[0].Observations, second)
	v.Visual.Persons = append(v.Visual.Persons, VisualPerson{TrackID: "T0002", Observations: []VisualObservation{second}})
	var event VisualEvent
	json.Unmarshal(v.Events[0], &event)
	event.FrameAssetIDs = []string{a.AssetID}
	v.Events[0], _ = json.Marshal(event)
	// Wrong uploaded timestamp and foreign job image references cannot be committed.
	v.Frames[0].TimestampMS = 1
	raw, _ := json.Marshal(v)
	if _, err = svc.Complete(ctx, c.JobID, c.LeaseToken, raw); err == nil {
		t.Fatal("bad image timestamp accepted")
	}
	v.Frames[0].TimestampMS = 0
	complete(c, v)
	if replay := complete(c, v); !replay.Duplicate {
		t.Fatal("completion replay not idempotent")
	}
	finished, err := svc.Get(ctx, f.admin, r.ID)
	if err != nil || finished.Status != "succeeded" || finished.ReportID != nil || len(finished.Jobs) != 2 {
		t.Fatalf("bad visual terminal: %+v %v", finished, err)
	}
	result, err := svc.Results(ctx, f.admin, r.ID)
	if err != nil || len(result.Events) != 1 || len(result.Evidence) != 2 {
		t.Fatalf("missing behavior evidence: %+v %v", result, err)
	}
	if _, err = svc.Visual(ctx, identity.Principal{}, r.ID, "summary", "", 0, 100); err == nil {
		t.Fatal("scope bypass")
	}
	summary, err := svc.Visual(ctx, f.admin, r.ID, "summary", "", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(summary)
	if bytes.Contains(body, []byte(`"persons"`)) {
		t.Fatal("summary leaked full persons array")
	}
	for _, section := range []string{"persons", "observations"} {
		value, err := svc.Visual(ctx, f.admin, r.ID, section, "T0001", 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		page := value.(map[string]any)
		if len(page["items"].([]json.RawMessage)) != 1 || page["next_cursor"] == nil {
			t.Fatal("missing person observation")
		}
	}
	if _, err = svc.Visual(ctx, f.admin, r.ID, "observations", "T9999", 0, 100); err == nil {
		t.Fatal("missing person accepted")
	}
	empty, err := svc.Visual(ctx, f.admin, r.ID, "observations", "T0001", 2, 1)
	if err != nil || len(empty.(map[string]any)["items"].([]json.RawMessage)) != 0 {
		t.Fatal("end of observation pagination", err)
	}
	restarted := NewService(f.pool, f.store, Config{})
	if _, err = restarted.Visual(ctx, f.admin, r.ID, "summary", "", 0, 100); err != nil {
		t.Fatal("saved result depends on enabled flag", err)
	}
	// Cancellation and media failure stay inside the two-stage plan.
	cancelled := create("visual-cancel")
	if value, err := svc.Cancel(ctx, f.admin, cancelled.ID); err != nil || value.Status != "cancelled" || len(value.Jobs) != 2 {
		t.Fatal("cancel failed", err)
	}
	failed := create("visual-fail")
	c = claim("probe")
	if _, err = svc.Fail(ctx, c.JobID, c.LeaseToken, Fail{ErrorCode: "UNSUPPORTED_MEDIA", Message: "synthetic failure", Retryable: false}); err != nil {
		t.Fatal(err)
	}
	value, err := svc.Get(ctx, f.admin, failed.ID)
	if err != nil || value.Status != "failed" || len(value.Jobs) != 2 {
		t.Fatal("probe failure plan", err)
	}
	failed = create("visual-model-fail")
	probe(claim("probe"))
	c = claim("video_analysis")
	if _, err = svc.Fail(ctx, c.JobID, c.LeaseToken, Fail{ErrorCode: "PROCESSING_FAILED", Message: "synthetic failure", Retryable: false}); err != nil {
		t.Fatal(err)
	}
	value, err = svc.Get(ctx, f.admin, failed.ID)
	if err != nil || value.Status != "failed" || len(value.Jobs) != 2 {
		t.Fatal("visual failure plan", err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE teaching.source_records SET allowed_uses='["playback"]' WHERE id=$1`, f.source); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Visual(ctx, f.admin, r.ID, "summary", "", 0, 100); err == nil {
		t.Fatal("revoked source readable")
	}
}
