package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// This is an internal immutable snapshot, not a new public or Worker DTO.
// It reuses completed observations; no new inference or cloud vision call.
type reportContextSnapshot struct {
	Version            string                  `json:"version"`
	Academic           map[string]string       `json:"academic"`
	VisualRunID        string                  `json:"visual_run_id,omitempty"`
	VisualResultSHA256 string                  `json:"visual_result_sha256,omitempty"`
	Evidence           []reportContextEvidence `json:"evidence"`
	Limitations        []string                `json:"limitations"`
}

type reportContextEvidence struct {
	Kind       string         `json:"kind"`
	StartMS    int64          `json:"start_ms"`
	EndMS      int64          `json:"end_ms"`
	Text       string         `json:"text"`
	Provenance map[string]any `json:"provenance"`
}

func (s *Service) captureReportContext(ctx context.Context, tx pgx.Tx, sessionID, mediaID, inputSHA string) (*reportContextSnapshot, error) {
	c := &reportContextSnapshot{Version: "classroom-context-v1", Evidence: []reportContextEvidence{}, Limitations: []string{}}
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object(
		'course_name',c.name,'course_description',left(c.description,500),'session_title',ls.title,
		'teacher_name',u.display_name,'class_name',g.name,'expected_class_size',coalesce(g.expected_size::text,'未登记'),
		'term_name',t.name,'offering_code',o.code,'organization_name',org.name,
		'planned_start_at',ls.planned_start_at::text,'planned_end_at',ls.planned_end_at::text,
		'schedule_start_at',coalesce(se.starts_at::text,'未关联课表'),
		'schedule_end_at',coalesce(se.ends_at::text,'未关联课表'),
		'schedule_status',coalesce(se.status,'未关联课表'),'classroom_name',coalesce(room.name,'未关联教室'),
		'context_note','教务与课表为登记和计划信息，不证明实际到课、实际开始结束或教学效果；录像时间均相对本段录像起点。')
		FROM teaching.lesson_sessions ls JOIN teaching.course_offerings o ON o.id=ls.offering_id
		JOIN teaching.courses c ON c.id=o.course_id JOIN teaching.user_accounts u ON u.id=o.teacher_id
		JOIN teaching.class_groups g ON g.id=o.class_group_id JOIN teaching.academic_terms t ON t.id=o.term_id
		JOIN teaching.org_units org ON org.id=o.org_unit_id
		LEFT JOIN teaching.schedule_entries se ON se.id=ls.schedule_entry_id AND se.offering_id=o.id
		LEFT JOIN teaching.classrooms room ON room.id=se.classroom_id WHERE ls.id=$1`, sessionID).Scan(&c.Academic)
	if err != nil {
		return nil, err
	}
	for key, value := range c.Academic {
		c.Academic[key] = truncateRunes(value, 500)
	}
	c.Evidence = append(c.Evidence, reportContextEvidence{Kind: "manual", StartMS: 0, EndMS: 1,
		Text:       fmt.Sprintf("教务与课表登记快照（仅作计划背景，不是实际行为观察）：课程：%s；任课教师：%s；班级：%s；登记班级人数：%s；学期：%s；所属组织：%s；课堂标题：%s；本次课堂计划时间：%s至%s；关联课表时间：%s至%s；教室：%s；课表状态：%s。计划时间不证明实际开课或出勤，本条仅为背景登记，不能作为录像连续覆盖。", c.Academic["course_name"], c.Academic["teacher_name"], c.Academic["class_name"], c.Academic["expected_class_size"], c.Academic["term_name"], c.Academic["organization_name"], c.Academic["session_title"], c.Academic["planned_start_at"], c.Academic["planned_end_at"], c.Academic["schedule_start_at"], c.Academic["schedule_end_at"], c.Academic["classroom_name"], c.Academic["schedule_status"]),
		Provenance: map[string]any{"report_context_version": c.Version, "source": "academic_schedule", "snapshot_only": true}})
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT r.id::text,
		((a.metadata->'result')-'visual') || jsonb_build_object('visual',(a.metadata->'result'->'visual')-'persons')
		FROM teaching.analysis_runs r JOIN teaching.analysis_jobs j ON j.run_id=r.id AND j.stage='video_analysis' AND j.status='succeeded'
		JOIN teaching.job_artifacts a ON a.job_id=j.id AND a.artifact_key='__result/video_analysis'
		WHERE r.session_id=$1 AND r.media_asset_id=$2 AND r.input_sha256=$3 AND r.mode='visual_only' AND r.status='succeeded'
		AND a.metadata->'result'->'visual'->>'input_sha256'=$3
		ORDER BY r.created_at DESC,r.id DESC LIMIT 1`, sessionID, mediaID, inputSHA).Scan(&c.VisualRunID, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		c.Limitations = append(c.Limitations, "本录像尚无已完成的举手与低头分析，本报告未纳入学生动作；请先完成视觉识别，再用已有转写生成新报告。")
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	c.VisualResultSHA256 = digestBytes(raw)
	var result VideoResult
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.Visual == nil || len(result.Visual.Counts) == 0 {
		return nil, fmt.Errorf("selected visual result has no sampled observations")
	}
	c.Evidence = append(c.Evidence, summarizeVisualContext(result, c.VisualRunID, c.VisualResultSHA256)...)
	c.Limitations = append(c.Limitations, "存在本录像已完成的视觉抽样结果，实际纳入资料以报告列出的依据为准；举手和低头为自动观察，待核对，不等于出勤、专注度或学习效果。")
	c.Limitations = append(c.Limitations, result.Limitations...)
	return c, nil
}

func interleaveReportEvidence(items []modelEvidence) []modelEvidence {
	var speech, background, visual []modelEvidence
	for _, item := range items {
		if item.ContextOnly {
			background = append(background, item)
		} else if item.Kind == "behavior" {
			visual = append(visual, item)
		} else {
			speech = append(speech, item)
		}
	}
	out := make([]modelEvidence, 0, len(items))
	for i := 0; i < max(len(speech), len(background), len(visual)); i++ {
		if i < len(speech) {
			out = append(out, speech[i])
		}
		if i < len(background) {
			out = append(out, background[i])
		}
		if i < len(visual) {
			out = append(out, visual[i])
		}
	}
	return out
}

func summarizeVisualContext(result VideoResult, runID, resultSHA string) []reportContextEvidence {
	counts := result.Visual.Counts
	// At most 24 time buckets, retaining both positive and no-positive samples.
	window := max(int64(10000), (counts[len(counts)-1].TimestampMS+24)/24)
	var out []reportContextEvidence
	for i := 0; i < len(counts); {
		end := i + 1
		bucket := counts[i].TimestampMS / window
		for end < len(counts) && counts[end].TimestampMS/window == bucket {
			end++
		}
		visibleSum, visibleKnown, handKnown, headKnown, handPeak, headPeak := 0, 0, 0, 0, 0, 0
		for _, point := range counts[i:end] {
			if point.VisibleCount != nil {
				visibleSum += *point.VisibleCount
				visibleKnown++
			}
			if point.HandRaiseCount != nil {
				handKnown++
				handPeak = max(handPeak, *point.HandRaiseCount)
			}
			if point.HeadDownCount != nil {
				headKnown++
				headPeak = max(headPeak, *point.HeadDownCount)
			}
		}
		visible := "看不清"
		if visibleKnown > 0 {
			visible = fmt.Sprintf("%.1f", float64(visibleSum)/float64(visibleKnown))
		}
		hand, head := "看不清", "看不清"
		if handKnown > 0 {
			hand = fmt.Sprint(handPeak)
		}
		if headKnown > 0 {
			head = fmt.Sprint(headPeak)
		}
		startMS, endMS := counts[i].TimestampMS, counts[end-1].TimestampMS+1
		text := fmt.Sprintf("视觉抽样：录像%.3f至%.3f秒，共检查%d张画面；平均可见人数%s（含画面人员，不是出勤人数）；单张画面举手最多%s人（%d张有计数），低头最多%s人（%d张有计数）。计数不代表每个人都可判断；未检出不等于没有动作，低头可能在读写，抽样跨度不代表持续行为。", float64(startMS)/1000, float64(endMS)/1000, end-i, visible, hand, handKnown, head, headKnown)
		out = append(out, reportContextEvidence{Kind: "behavior", StartMS: startMS, EndMS: endMS, Text: text,
			Provenance: map[string]any{"report_context_version": "classroom-context-v1", "source_visual_run_id": runID, "source_result_sha256": resultSHA, "model": result.Model, "sampling": result.Sampling, "needs_review": true}})
		i = end
	}
	// Events provide anonymous trajectories and time-local clues for speech alignment.
	kept := 0
	for _, raw := range result.Events {
		var event VisualEvent
		if json.Unmarshal(raw, &event) != nil || (event.EventType != "hand_raise" && event.EventType != "head_down") {
			continue
		}
		if kept >= 24 {
			break
		}
		kept++
		out = append(out, reportContextEvidence{Kind: "behavior", StartMS: event.StartMS, EndMS: event.EndMS,
			Text:       fmt.Sprintf("自动动作线索：编号%s在录像%.3f至%.3f秒疑似%s，需核对原录像；编号可能断裂，不能当作真实学生身份或独立人数，时间相近不证明与某次提问存在因果关系。", event.TrackID, float64(event.StartMS)/1000, float64(event.EndMS)/1000, behaviorLabels[event.EventType]),
			Provenance: map[string]any{"report_context_version": "classroom-context-v1", "source_visual_run_id": runID, "source_result_sha256": resultSHA, "event": event, "needs_review": true}})
	}
	if len(result.Events) > kept && kept == 24 && len(out) > 0 {
		out[len(out)-1].Text += "动作线索较多，仅保留前24条；其他抽样计数见时间窗口记录。"
	}
	return out
}

func (s *Service) insertReportContextEvidence(ctx context.Context, tx pgx.Tx, j goJob) error {
	var snap snapshot
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT config_snapshot FROM teaching.analysis_runs WHERE id=$1`, j.RunID).Scan(&raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		return err
	}
	c := snap.Report.Context
	if c == nil {
		return nil
	}
	if c.VisualRunID != "" {
		var current []byte
		err := tx.QueryRow(ctx, `SELECT ((a.metadata->'result')-'visual') || jsonb_build_object('visual',(a.metadata->'result'->'visual')-'persons')
			FROM teaching.job_artifacts a JOIN teaching.analysis_runs r ON r.id=a.run_id
			WHERE r.id=$1 AND r.session_id=$2 AND r.media_asset_id=$3 AND r.status='succeeded' AND a.artifact_key='__result/video_analysis'`, c.VisualRunID, j.SessionID, j.MediaID).Scan(&current)
		if err != nil {
			return err
		}
		if digestBytes(current) != c.VisualResultSHA256 {
			return fmt.Errorf("fixed visual input changed")
		}
	}
	for _, e := range c.Evidence {
		if _, err := tx.Exec(ctx, `INSERT INTO teaching.evidence_items(run_id,session_id,media_asset_id,kind,start_ms,end_ms,description,provenance)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, j.RunID, j.SessionID, j.MediaID, e.Kind, e.StartMS, e.EndMS, strings.TrimSpace(e.Text), e.Provenance); err != nil {
			return err
		}
	}
	return nil
}
