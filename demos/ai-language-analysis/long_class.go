package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	mediaPipelineVersion        = "long-lesson-v1"
	maxMediaSeconds             = 60 * 60
	maxUploadBytes        int64 = 2 << 30
	maxUploadRequestBytes       = maxUploadBytes + (1 << 20)
	jobTimeout                  = 6 * time.Hour
	analysisWindowMS      int64 = 10 * 60 * 1000
	analysisWindowRunes         = 12000
	// Character budget, not a claim about provider tokenization/context size.
	// Requests are rejected whole, never truncated to fit this limit.
	maxTextRequestRunes = 240000
)

type lessonWindow struct {
	Number   int       `json:"number"`
	Start    int64     `json:"start_ms"`
	End      int64     `json:"end_ms"`
	IDs      []string  `json:"source_ids"`
	Segments []Segment `json:"-"`
}

type LessonCoverage struct {
	Version               string         `json:"version"`
	Windows               []lessonWindow `json:"windows"`
	EventWindowsCompleted int            `json:"event_windows_completed"`
	SourceCount           int            `json:"source_count"`
	Scope                 string         `json:"scope"`
}

// Each source has exactly one owner. A preceding block is added only to model
// context so a sentence crossing a window boundary can still be read together.
func lessonWindows(segments []Segment) []lessonWindow {
	var windows []lessonWindow
	for start := 0; start < len(segments); {
		end, size := start, 0
		for end < len(segments) {
			n := utf8.RuneCountInString(segments[end].Text)
			if end > start && (segments[end].End-segments[start].Start > analysisWindowMS || size+n > analysisWindowRunes) {
				break
			}
			size += n
			end++
		}
		w := lessonWindow{Number: len(windows) + 1, Start: segments[start].Start, End: segments[end-1].End, IDs: []string{}}
		for _, s := range segments[start:end] {
			w.IDs = append(w.IDs, s.ID)
		}
		contextStart := start
		if contextStart > 0 {
			contextStart--
		}
		w.Segments = segments[contextStart:end]
		windows = append(windows, w)
		start = end
	}
	return windows
}

type analysisProgressKey struct{}

func withAnalysisProgress(ctx context.Context, progress progressFunc) context.Context {
	return context.WithValue(ctx, analysisProgressKey{}, progress)
}
func windowProgress(ctx context.Context, stage, action string, i, count, begin, span int) {
	if progress, ok := ctx.Value(analysisProgressKey{}).(progressFunc); ok && progress != nil {
		progress(stage, fmt.Sprintf("%s：第 %d / %d 部分（保留原录像时间）", action, i+1, count), begin+i*span/count)
	}
}

func organizeReading(ctx context.Context, base, key string, segments []Segment) ReadingResult {
	windows := lessonWindows(segments)
	if len(windows) <= 1 {
		return organizeReadingWindow(ctx, base, key, segments)
	}
	boundaries := make([]BoundaryDecision, 0, len(segments)-1)
	meta := ModelMeta{}
	status, code := "ready", ""
	for i, w := range windows {
		if ctx.Err() != nil {
			return buildReading(segments, conservativeBoundaries(segments), "conservative", "cancelled", meta)
		}
		windowProgress(ctx, "reading", "整理课堂文本", i, len(windows), 55, 9)
		part := organizeReadingWindow(ctx, base, key, w.Segments)
		boundaries = append(boundaries, part.Boundaries...)
		meta.Requests = append(meta.Requests, part.Request)
		if part.Status != "ready" {
			status, code = "conservative", "partial_window_fallback"
		}
	}
	return buildReading(segments, boundaries, status, code, meta)
}

type eventWindowKey struct{}

type lessonWindowError struct {
	completed int
	message   string
}

func (e *lessonWindowError) Error() string { return e.message }

type eventWindowScope struct {
	Window    lessonWindow `json:"window"`
	Count     int          `json:"window_count"`
	MaxEvents int          `json:"max_events"`
	Boundary  string       `json:"boundary"`
}

func extractEvents(ctx context.Context, base, key string, segments []Segment) (EventSet, ModelMeta, error) {
	windows := lessonWindows(segments)
	if len(windows) <= 1 {
		return extractEventsWindow(ctx, base, key, segments)
	}
	set, meta := EventSet{Events: []TeachingEvent{}}, ModelMeta{}
	if len(windows) > 80 {
		return set, meta, fmt.Errorf("转写内容过密，超过分段分析预算；原始转写保留，请缩短片段后分析")
	}
	seen := map[string]bool{}
	var rejected []RejectedEvent
	for i, w := range windows {
		if ctx.Err() != nil {
			return set, meta, ctx.Err()
		}
		windowProgress(ctx, "events", "分析课堂依据", i, len(windows), 65, 12)
		scope := eventWindowScope{Window: w, Count: len(windows), MaxEvents: 80 / len(windows), Boundary: "仅描述本窗口实际讲解，前一块仅供衔接；不把本窗口未提到认定为整堂缺失。优先保留概念联系、推理、具体提问和活动，合并同义事件，按max_events预算选代表依据。"}
		part, request, err := extractEventsWindow(context.WithValue(ctx, eventWindowKey{}, scope), base, key, w.Segments)
		meta.Requests = append(meta.Requests, request)
		if err != nil {
			var contract *eventContractError
			if errors.As(err, &contract) && len(contract.rejected) > 0 {
				part = contract.partial
				rejected = append(rejected, contract.rejected...)
			} else {
				// Do not turn an incomplete classroom into a complete report.
				return set, meta, &lessonWindowError{completed: i, message: fmt.Sprintf("第 %d / %d 部分课堂依据分析失败，整堂报告未生成；转写保留，无需重新转写：%v", i+1, len(windows), err)}
			}
		}
		if len(part.Events) > scope.MaxEvents {
			return set, meta, &lessonWindowError{completed: i, message: fmt.Sprintf("第 %d 部分的课堂依据超过本次预算，未截断或发布报告；请重新分析", i+1)}
		}
		for _, event := range part.Events {
			// Only remove identical repeated evidence introduced by overlap.
			identity, _ := json.Marshal(struct {
				Type   string
				Quotes []EventQuote
			}{event.Type, event.Quotes})
			if seen[string(identity)] {
				continue
			}
			seen[string(identity)] = true
			event.ID = fmt.Sprintf("window-%02d-%s", i+1, event.ID)
			set.Events = append(set.Events, event)
		}
	}
	if err := validateEvents(set, segments); err != nil {
		return set, meta, err
	}
	if len(rejected) > 0 {
		return set, meta, &eventContractError{err: fmt.Errorf("有%d条活动依据未通过校验，已隔离", len(rejected)), partial: set, rejected: rejected}
	}
	return set, meta, nil
}

func textRequestBudget(prompt string, input any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if utf8.RuneCountInString(prompt)+utf8.RuneCount(raw) > maxTextRequestRunes {
		return fmt.Errorf("本次文本超过完整分析输入预算，未截断正文、未发送本次模型请求；转写已保留，请缩短片段或减少转写冗余后重新分析")
	}
	return nil
}

func validateMediaDuration(seconds float64) error {
	if !(seconds > 0 && seconds <= maxMediaSeconds) {
		return fmt.Errorf("仅接受60分钟以内的视频或音频")
	}
	return nil
}

// A tabular encoding removes repeated JSON field names without changing any
// quote ID, source ID or verbatim text used by local evidence validation.
func compactEvidenceCatalog(segments []Segment) any {
	rows := make([][3]string, 0)
	for _, q := range evidenceCatalog(segments) {
		rows = append(rows, [3]string{q.QuoteID, q.SegmentID, q.Text})
	}
	return struct {
		Columns [3]string   `json:"columns"`
		Rows    [][3]string `json:"rows"`
	}{[3]string{"quote_id", "segment_id", "text"}, rows}
}

func longLessonReminder(segments []Segment) string {
	if len(lessonWindows(segments)) <= 1 {
		return ""
	}
	return strings.TrimSpace(`
长课堂的evidence_catalog采用无损表格格式：columns依次为quote_id、segment_id、text，rows每行按此顺序保存一个完整原话条目。引用时选择该行真实quote_id，text仍为逐字原话；不能把列名或行号当作引用编号。
【长课堂报告】original_segments包含本次全部转写，lesson_windows说明每个时间窗口的覆盖范围。先通读首、中、尾及后文澄清，再概括讲解主线；结合各窗口的代表依据，不仅分析开头。按阶段呈现概念联系和教学推进，不逐段复述，不增加同义建议。事件是窗口内提取的事实线索，不证明整堂课缺失；某概念后文已解释不能建议教师再次补讲。总体摘要可以串联不同阶段，但具体发现和建议仍只依赖本条选定依据。不能根据上传时长认定它覆盖整节课。`)
}
