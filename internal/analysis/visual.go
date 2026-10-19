package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"math"
	"regexp"
	"strconv"
)

// Visual-v1 reuses the prototype's observations. Null means unobservable, never false.
type VisualParameters struct {
	Model           Model `json:"model"`
	MaxSamples      int   `json:"max_samples"`
	MaxPosePeople   int   `json:"max_pose_people"`
	HeadDownEnabled bool  `json:"head_down_enabled"`
}
type VisualCount struct {
	TimestampMS    int64 `json:"timestamp_ms"`
	VisibleCount   *int  `json:"visible_count"`
	HandRaiseCount *int  `json:"hand_raise_count"`
	HeadDownCount  *int  `json:"head_down_count"`
}
type VisualObservation struct {
	TimestampMS  int64            `json:"timestamp_ms"`
	BBox         [4]float64       `json:"bbox"`
	Behaviors    map[string]*bool `json:"behaviors"`
	FrameAssetID *string          `json:"frame_asset_id"`
}
type VisualPerson struct {
	TrackID      string              `json:"track_id"`
	Observations []VisualObservation `json:"observations"`
}
type VisualEvent struct {
	EventType     string   `json:"event_type"`
	TrackID       string   `json:"track_id"`
	StartMS       int64    `json:"start_ms"`
	EndMS         int64    `json:"end_ms"`
	Confidence    *float64 `json:"confidence"`
	FrameAssetIDs []string `json:"frame_asset_ids"`
}
type VisualData struct {
	InputSHA256 string         `json:"input_sha256"`
	Width       int            `json:"width"`
	Height      int            `json:"height"`
	Counts      []VisualCount  `json:"counts"`
	Persons     []VisualPerson `json:"persons"`
}

var trackPattern = regexp.MustCompile(`^T[0-9]{3,8}$`)
var behaviorLabels = map[string]string{"hand_raise": "举手", "head_down": "低头", "leave_seat": "离座", "possible_phone": "疑似持手机"}

var behaviorKinds = []string{"hand_raise", "head_down", "leave_seat", "possible_phone"}

// encoding/json accepts missing keys and null scalars. Check required visual
// fields explicitly so the Go boundary enforces the strict public DTO too.
func validateVisualShape(raw []byte) error {
	object := func(raw json.RawMessage, keys []string, nullable map[string]bool) (map[string]json.RawMessage, error) {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil || m == nil {
			return nil, invalidResult("visual object required")
		}
		for _, k := range keys {
			v, ok := m[k]
			if !ok || (!nullable[k] && string(v) == "null") {
				return nil, invalidResult("missing required visual field: " + k)
			}
		}
		return m, nil
	}
	root, err := object(raw, []string{"visual", "model", "frames", "events", "sampling", "coverage", "limitations"}, nil)
	if err != nil {
		return err
	}
	if _, err = object(root["model"], []string{"name", "revision"}, nil); err != nil {
		return err
	}
	d, err := object(root["visual"], []string{"input_sha256", "width", "height", "counts", "persons"}, nil)
	if err != nil {
		return err
	}
	var counts, people, events []json.RawMessage
	if err = json.Unmarshal(d["counts"], &counts); err != nil {
		return invalidResult("counts array required")
	}
	for _, raw := range counts {
		if _, err = object(raw, []string{"timestamp_ms", "visible_count", "hand_raise_count", "head_down_count"}, map[string]bool{"visible_count": true, "hand_raise_count": true, "head_down_count": true}); err != nil {
			return err
		}
	}
	if err = json.Unmarshal(d["persons"], &people); err != nil {
		return invalidResult("persons array required")
	}
	for _, raw := range people {
		person, err := object(raw, []string{"track_id", "observations"}, nil)
		if err != nil {
			return err
		}
		var observations []json.RawMessage
		if err = json.Unmarshal(person["observations"], &observations); err != nil {
			return invalidResult("observations array required")
		}
		for _, raw := range observations {
			obs, err := object(raw, []string{"timestamp_ms", "bbox", "behaviors", "frame_asset_id"}, map[string]bool{"frame_asset_id": true})
			if err != nil {
				return err
			}
			var box []json.RawMessage
			if err = json.Unmarshal(obs["bbox"], &box); err != nil || len(box) != 4 {
				return invalidResult("bbox requires four coordinates")
			}
			for _, v := range box {
				if string(v) == "null" {
					return invalidResult("bbox coordinate cannot be null")
				}
			}
		}
	}
	if err = json.Unmarshal(root["events"], &events); err != nil {
		return invalidResult("events array required")
	}
	for _, raw := range events {
		if _, err = object(raw, []string{"event_type", "track_id", "start_ms", "end_ms", "confidence", "frame_asset_ids"}, map[string]bool{"confidence": true}); err != nil {
			return err
		}
	}
	return nil
}

func validateVisual(v VideoResult, p Parameters, sha string, duration int64, width, height int) error {
	bad := func() error { return invalidResult("visual observations, identity or timeline are invalid") }
	d := v.Visual
	if p.Visual == nil || d == nil || v.Model == nil || *v.Model != p.Visual.Model || d.InputSHA256 != sha || d.Width != width || d.Height != height || d.Width < 1 || d.Height < 1 || d.Counts == nil || d.Persons == nil || len(d.Counts) == 0 || len(d.Counts) > p.Visual.MaxSamples || len(d.Persons) > 10000 || len(v.Events) > 10000 {
		return bad()
	}
	times := map[int64]bool{}
	prev := int64(-1)
	for _, c := range d.Counts {
		if c.TimestampMS <= prev || c.TimestampMS >= duration || !pointCovered(c.TimestampMS, v.Coverage) {
			return bad()
		}
		prev = c.TimestampMS
		times[c.TimestampMS] = true
		for _, n := range []*int{c.VisibleCount, c.HandRaiseCount, c.HeadDownCount} {
			if n != nil && (*n < 0 || *n > 10000) {
				return bad()
			}
		}
	}
	images := map[string]int64{}
	for _, f := range v.Frames {
		if _, ok := images[f.AssetID]; ok || !times[f.TimestampMS] {
			return bad()
		}
		images[f.AssetID] = f.TimestampMS
	}
	people := map[string]map[int64]VisualObservation{}
	total := 0
	for _, person := range d.Persons {
		if !trackPattern.MatchString(person.TrackID) || len(person.Observations) == 0 {
			return bad()
		}
		if _, ok := people[person.TrackID]; ok {
			return bad()
		}
		observed := map[int64]VisualObservation{}
		prev = -1
		for _, o := range person.Observations {
			total++
			if total > 100000 || o.TimestampMS <= prev || !times[o.TimestampMS] || len(o.Behaviors) != 4 {
				return bad()
			}
			prev = o.TimestampMS
			for _, k := range behaviorKinds {
				if _, ok := o.Behaviors[k]; !ok {
					return bad()
				}
			}
			b := o.BBox
			for _, x := range b {
				if math.IsNaN(x) || math.IsInf(x, 0) {
					return bad()
				}
			}
			if b[0] < 0 || b[1] < 0 || b[2] <= b[0] || b[3] <= b[1] || b[2] > float64(width) || b[3] > float64(height) {
				return bad()
			}
			if o.FrameAssetID != nil {
				t, ok := images[*o.FrameAssetID]
				if !ok || t != o.TimestampMS {
					return bad()
				}
			}
			observed[o.TimestampMS] = o
		}
		people[person.TrackID] = observed
	}
	for _, raw := range v.Events {
		var event VisualEvent
		if err := decodeStrict(raw, &event); err != nil {
			return bad()
		}
		found := false
		for _, k := range behaviorKinds {
			if event.EventType == k {
				found = true
			}
		}
		if !found || event.StartMS < 0 || event.EndMS <= event.StartMS || event.EndMS > duration || !covered(event.StartMS, event.EndMS, v.Coverage) || event.FrameAssetIDs == nil {
			return bad()
		}
		observations, ok := people[event.TrackID]
		if !ok {
			return bad()
		}
		positive := false
		for t, o := range observations {
			b := o.Behaviors[event.EventType]
			if t >= event.StartMS && t < event.EndMS && b != nil && *b {
				positive = true
			}
		}
		if !positive {
			return bad()
		}
		if event.Confidence != nil && (math.IsNaN(*event.Confidence) || math.IsInf(*event.Confidence, 0) || *event.Confidence < 0 || *event.Confidence > 1) {
			return bad()
		}
		for _, id := range event.FrameAssetIDs {
			t, ok := images[id]
			if !ok || t < event.StartMS || t >= event.EndMS {
				return bad()
			}
			o, ok := observations[t]
			if !ok || o.FrameAssetID == nil || *o.FrameAssetID != id || o.Behaviors[event.EventType] == nil || !*o.Behaviors[event.EventType] {
				return bad()
			}
		}
	}
	return nil
}

func (s *Service) finishVisual(ctx context.Context, tx pgx.Tx, j lockedJob) error {
	var status string
	var code *string
	if err := tx.QueryRow(ctx, `SELECT status,error_code FROM teaching.analysis_jobs WHERE run_id=$1 AND stage='video_analysis'`, j.RunID).Scan(&status, &code); err != nil {
		return err
	}
	if status == "succeeded" {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT metadata->'result' FROM teaching.job_artifacts WHERE run_id=$1 AND artifact_key='__result/video_analysis'`, j.RunID).Scan(&raw); err != nil {
			return err
		}
		var result VideoResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
		// Images and behavior evidence are created by Go, in the completion transaction.
		for _, frame := range result.Frames {
			prov := map[string]any{"model": result.Model, "processor_version": result.Execution.ProcessorVersion}
			if _, err := tx.Exec(ctx, `INSERT INTO teaching.evidence_items(run_id,session_id,media_asset_id,frame_asset_id,kind,start_ms,end_ms,description,provenance) VALUES($1,$2,$3,$4,'frame',$5,$6,'课堂观察画面',$7)`, j.RunID, j.SessionID, j.MediaID, frame.AssetID, frame.TimestampMS, frame.TimestampMS+1, prov); err != nil {
				return err
			}
		}
		for _, rawEvent := range result.Events {
			var event VisualEvent
			if err := json.Unmarshal(rawEvent, &event); err != nil {
				return err
			}
			var image *string
			if len(event.FrameAssetIDs) > 0 {
				image = &event.FrameAssetIDs[0]
			}
			prov := map[string]any{"model": result.Model, "track_id": event.TrackID, "event": event, "needs_review": true}
			if _, err := tx.Exec(ctx, `INSERT INTO teaching.evidence_items(run_id,session_id,media_asset_id,frame_asset_id,kind,start_ms,end_ms,description,provenance) VALUES($1,$2,$3,$4,'behavior',$5,$6,$7,$8)`, j.RunID, j.SessionID, j.MediaID, image, event.StartMS, event.EndMS, behaviorLabels[event.EventType]+"：自动识别，待核对", prov); err != nil {
				return err
			}
		}
	} else if status == "failed" || status == "skipped" {
		status = "failed"
	} else {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status=$2,error_code=$3,finished_at=now() WHERE id=$1`, j.RunID, status, code)
	return err
}

// All reads apply the same classroom role, scope, source permission and expiry gates
// as Results. Results remain immutable; deletion removes the enclosing artifacts.
func (s *Service) Visual(ctx context.Context, p identity.Principal, runID, section, trackID string, offset, limit int) (any, error) {
	run, err := s.Get(ctx, p, runID)
	if err != nil {
		return nil, err
	}
	if err = s.requireRunContentReadable(ctx, runID, true); err != nil {
		return nil, err
	}
	if run.Mode != "visual_only" {
		return nil, state("run does not contain visual analysis")
	}
	if offset < 0 || offset > 100000 || limit < 1 || limit > 200 {
		return nil, invalid("invalid visual pagination")
	}
	// Strip persons in PostgreSQL for summaries; page slices rather than transferring
	// the complete classroom observation array to the API process on each read.
	const from = ` FROM teaching.job_artifacts a JOIN teaching.analysis_jobs j ON j.id=a.job_id WHERE a.run_id=$1 AND a.artifact_key='__result/video_analysis' AND j.status='succeeded'`
	var raw []byte
	switch section {
	case "summary":
		err = s.pool.QueryRow(ctx, `SELECT ((a.metadata->'result') - 'visual') || jsonb_build_object('visual',(a.metadata->'result'->'visual')-'persons')`+from, runID).Scan(&raw)
	case "persons":
		err = s.pool.QueryRow(ctx, `SELECT COALESCE((SELECT jsonb_agg(value-'observations'||jsonb_build_object('sample_count',jsonb_array_length(value->'observations'))) FROM (SELECT value FROM jsonb_array_elements(a.metadata->'result'->'visual'->'persons') WITH ORDINALITY AS p(value,n) ORDER BY n OFFSET $2 LIMIT $3) x),'[]'::jsonb)`+from, runID, offset, limit+1).Scan(&raw)
	case "observations":
		if !trackPattern.MatchString(trackID) {
			return nil, invalid("invalid track_id")
		}
		err = s.pool.QueryRow(ctx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM jsonb_array_elements(a.metadata->'result'->'visual'->'persons') p WHERE p->>'track_id'=$4) THEN COALESCE((SELECT jsonb_agg(value) FROM (SELECT value FROM jsonb_array_elements((SELECT p FROM jsonb_array_elements(a.metadata->'result'->'visual'->'persons') p WHERE p->>'track_id'=$4)->'observations') WITH ORDINALITY AS o(value,n) ORDER BY n OFFSET $2 LIMIT $3) x),'[]'::jsonb) ELSE NULL END`+from, runID, offset, limit+1, trackID).Scan(&raw)
	default:
		return nil, invalid("invalid visual section")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, state("visual analysis has not completed")
	}
	if err != nil {
		return nil, err
	}
	// Recheck lifecycle after the potentially long JSON query.
	if err = s.requireRunContentReadable(ctx, runID, true); err != nil {
		return nil, err
	}
	if section == "summary" {
		return json.RawMessage(raw), nil
	}
	if raw == nil && section == "observations" {
		return nil, notFound()
	}
	var items []json.RawMessage
	if err = json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		v := strconv.Itoa(offset + limit)
		next = &v
	}
	return map[string]any{"items": items, "next_cursor": next}, nil
}
