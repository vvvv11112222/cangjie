package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// A read-only PDF projection of the report's fixed input. The JSON export's
// existing protocol and full provenance remain unchanged.
type reportDetails struct {
	Academic       map[string]string
	Filename       string
	DurationMS     int64
	Visual         *exportVisual
	VisualNote     string
	Evidence       []exportEvidence
	SpeechSegments int
}
type exportEvidence struct {
	ID, Kind, Description, Availability string
	StartMS, EndMS                      int64
}
type exportObservation struct {
	TimestampMS int64            `json:"timestamp_ms"`
	Behaviors   map[string]*bool `json:"behaviors"`
}
type exportPerson struct {
	TrackID      string              `json:"track_id"`
	Observations []exportObservation `json:"observations"`
}
type exportCount struct {
	TimestampMS    int64 `json:"timestamp_ms"`
	VisibleCount   *int  `json:"visible_count"`
	HandRaiseCount *int  `json:"hand_raise_count"`
	HeadDownCount  *int  `json:"head_down_count"`
}
type exportVisual struct {
	Visual struct {
		InputSHA string         `json:"input_sha256"`
		Counts   []exportCount  `json:"counts"`
		Persons  []exportPerson `json:"persons"`
	} `json:"visual"`
	Sampling struct {
		IntervalMS int64 `json:"interval_ms"`
	} `json:"sampling"`
	Coverage []struct {
		StartMS int64 `json:"start_ms"`
		EndMS   int64 `json:"end_ms"`
	} `json:"coverage"`
}

func (s *Service) reportDetails(ctx context.Context, reportID string) (*reportDetails, error) {
	d := &reportDetails{Academic: map[string]string{}, Evidence: []exportEvidence{}}
	var raw []byte
	var sessionID, mediaID, inputSHA, title string
	err := s.pool.QueryRow(ctx, `SELECT ar.config_snapshot->'report'->'classroom_context',ls.id::text,m.id::text,ar.input_sha256,
 coalesce(m.original_filename,''),coalesce(m.duration_ms,0),ls.title
 FROM teaching.reports r JOIN teaching.analysis_runs ar ON ar.id=r.run_id
 JOIN teaching.lesson_sessions ls ON ls.id=r.session_id JOIN teaching.media_assets m ON m.id=ar.media_asset_id WHERE r.id=$1`, reportID).Scan(&raw, &sessionID, &mediaID, &inputSHA, &d.Filename, &d.DurationMS, &title)
	if err != nil {
		return nil, err
	}
	var fixed struct {
		Academic    map[string]string `json:"academic"`
		VisualRunID string            `json:"visual_run_id"`
		VisualSHA   string            `json:"visual_result_sha256"`
	}
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &fixed); err != nil {
			return nil, err
		}
	}
	if fixed.Academic != nil {
		d.Academic = fixed.Academic
	}
	if d.Academic["session_title"] == "" {
		d.Academic["session_title"] = title
	}
	// Never look up "latest" visual results: a newer recording/run must not be
	// silently attached to a previously reviewed/published report.
	if fixed.VisualRunID != "" {
		var result, summary []byte
		err = s.pool.QueryRow(ctx, `SELECT a.metadata->'result',((a.metadata->'result')-'visual') || jsonb_build_object('visual',(a.metadata->'result'->'visual')-'persons')
  FROM teaching.analysis_runs vr JOIN teaching.analysis_jobs j ON j.run_id=vr.id AND j.stage='video_analysis' AND j.status='succeeded'
  JOIN teaching.job_artifacts a ON a.job_id=j.id AND a.artifact_key='__result/video_analysis'
  WHERE vr.id::text=$1 AND vr.session_id=$2 AND vr.media_asset_id=$3 AND vr.input_sha256=$4 AND vr.mode='visual_only' AND vr.status='succeeded'`, fixed.VisualRunID, sessionID, mediaID, inputSHA).Scan(&result, &summary)
		if errors.Is(err, pgx.ErrNoRows) {
			d.VisualNote = "本报告绑定的视觉记录当前不可用，未用其他分析替代。"
		} else if err != nil {
			return nil, err
		} else {
			digest := sha256.Sum256(summary)
			if fixed.VisualSHA == "" || hex.EncodeToString(digest[:]) != fixed.VisualSHA {
				return nil, fmt.Errorf("report visual snapshot changed")
			}
			var visual exportVisual
			if err = json.Unmarshal(result, &visual); err != nil {
				return nil, err
			}
			if visual.Visual.InputSHA != inputSHA {
				return nil, fmt.Errorf("report visual media digest mismatch")
			}
			d.Visual = &visual
		}
	} else {
		d.VisualNote = "生成本报告时未绑定已完成的视觉分析，暂无逐人行为汇总。请先完成同一录像的举手与低头识别，再从已有转写生成新报告。"
	}
	rows, err := s.pool.Query(ctx, `SELECT e.id::text,e.kind,e.start_ms,e.end_ms,e.description,e.availability
 FROM teaching.evidence_items e JOIN teaching.reports r ON r.run_id=e.run_id WHERE r.id=$1 ORDER BY e.start_ms,e.end_ms,e.id`, reportID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e exportEvidence
		if err = rows.Scan(&e.ID, &e.Kind, &e.StartMS, &e.EndMS, &e.Description, &e.Availability); err != nil {
			return nil, err
		}
		d.Evidence = append(d.Evidence, e)
		if e.Kind == "transcript" {
			d.SpeechSegments++
		}
	}
	return d, rows.Err()
}
