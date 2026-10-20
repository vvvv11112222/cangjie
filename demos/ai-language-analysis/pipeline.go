package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type AnalysisOutput struct {
	Coverage           *LessonCoverage   `json:"lesson_coverage,omitempty"`
	ReportStatus       string            `json:"report_status"`
	FailureStage       string            `json:"failure_stage,omitempty"`
	FailureMessage     string            `json:"failure_message,omitempty"`
	Segments           []Segment         `json:"segments"`
	AudioChunks        []AudioChunk      `json:"audio_chunks,omitempty"`
	Reading            ReadingResult     `json:"reading"`
	Events             EventSet          `json:"events"`
	Report             Report            `json:"report"`
	EventRequest       ModelMeta         `json:"event_request"`
	ReportRequest      ModelMeta         `json:"text_request"`
	ReportDiagnostic   *ReportDiagnostic `json:"-"`
	TimestampPrecision string            `json:"timestamp_precision"`
	Warnings           []string          `json:"warnings,omitempty"`
}

type progressFunc func(stage, message string, progress int)

const analysisVersion = "six-dim-coaching-v8"

func probeDuration(ctx context.Context, path string) (float64, error) {
	out, err := exec.CommandContext(ctx, env("FFPROBE_PATH", "ffprobe"), "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return 0, err
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, fmt.Errorf("媒体时长无效")
	}
	if err := validateMediaDuration(seconds); err != nil {
		return 0, err
	}
	return seconds, nil
}

func analyzeSegments(ctx context.Context, base, key string, segments []Segment, progress progressFunc) (AnalysisOutput, error) {
	if len(segments) == 0 {
		return AnalysisOutput{}, fmt.Errorf("未识别到文本")
	}
	ctx = withAnalysisProgress(ctx, progress)
	progress("reading", "结合上下文整理跨段语句（保留原始转写）", 55)
	reading := organizeReading(ctx, base, key, segments)
	out := AnalysisOutput{ReportStatus: reportPending, Segments: segments, Reading: reading, TimestampPrecision: "按停顿弹性切分的音频块（最长40秒），并非句子或词级对齐"}
	out.Coverage = &LessonCoverage{Version: mediaPipelineVersion, Windows: lessonWindows(segments), SourceCount: len(segments), Scope: "覆盖本次上传素材中全部非空转写块；不代表整节课完整、说话人已区分或音视频精确对齐"}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	progress("events", "提取教学事件并建立证据链", 65)
	events, eventMeta, err := extractEvents(ctx, base, key, segments)
	out.EventRequest = eventMeta

	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		var rejected *eventContractError
		if errors.As(err, &rejected) && len(rejected.partial.Events) > 0 && len(rejected.rejected) > 0 {
			// One invalid activity instruction is isolated, while all other
			// validated events continue into report generation. The rejected
			// model output remains available to diagnostics and is never exposed
			// as report evidence.
			events = rejected.partial
			out.Events = events
			out.ReportDiagnostic = failedReportDiagnostic(rejected.raw, nil, "event_contract_partial", err, segments, events, eventMeta)
			out.ReportDiagnostic.RejectedEvents = cloneValue(rejected.rejected)
			out.Warnings = append(out.Warnings, fmt.Sprintf("有%d条课堂依据未通过活动任务检查，已隔离；活动组织暂不能判断，其他维度仍基于有效依据生成。", len(rejected.rejected)))
		} else {
			out.Warnings = append(out.Warnings, "教学事件提取失败："+err.Error())
			if errors.As(err, &rejected) {
				out.ReportDiagnostic = failedReportDiagnostic(rejected.raw, nil, "event_contract", err, segments, events, eventMeta)
			}
			out.Events = EventSet{Events: []TeachingEvent{}}
			var windowFailure *lessonWindowError
			if errors.As(err, &windowFailure) && validateEvents(events, segments) == nil {
				out.Events = events
				out.Coverage.EventWindowsCompleted = windowFailure.completed
			}
			out.Report = insufficientReport("教学事件未完成，六维结论暂时没有可验证的事件证据")
			out.ReportStatus, out.FailureStage, out.FailureMessage = reportFailed, "events", out.Warnings[len(out.Warnings)-1]
			return out, nil
		}
	}
	out.Events, out.EventRequest = events, eventMeta
	out.Coverage.EventWindowsCompleted = len(out.Coverage.Windows)
	progress("report", "基于教学事件生成六维报告", 78)
	raw, meta, err := requestSixDimReport(ctx, base, key, segments, reading, events)
	out.ReportRequest = meta
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.Warnings = append(out.Warnings, "六维报告生成失败："+err.Error())
		out.Report = insufficientReport("六维报告模型请求失败，已保留转写、阅读整理和教学事件")
		out.ReportStatus, out.FailureStage, out.FailureMessage = reportFailed, "report", out.Warnings[len(out.Warnings)-1]
		return out, nil
	}
	draft, err := decodeReportDraft(raw)
	if err != nil {
		out.ReportDiagnostic = failedReportDiagnostic(raw, nil, "json", err, segments, events, meta)
		out.Warnings = append(out.Warnings, "六维报告生成失败："+err.Error())
		out.Report = insufficientReport("六维报告模型输出无效，已保留转写、阅读整理和教学事件")
		out.ReportStatus, out.FailureStage, out.FailureMessage = reportFailed, "report", out.Warnings[len(out.Warnings)-1]
		return out, nil
	}

	report, err := bindReportDraft(draft, events.Events, segments)
	if err != nil {
		out.ReportDiagnostic = failedReportDiagnostic(raw, nil, "basis_binding", err, segments, events, meta)
		out.Warnings = append(out.Warnings, reportBindingMessage(err))
		out.Report = insufficientReport("六维报告未通过校验，已保留转写、阅读整理和教学事件")
		out.ReportStatus, out.FailureStage, out.FailureMessage = reportFailed, "report", out.Warnings[len(out.Warnings)-1]
		return out, nil
	}
	candidate := cloneValue(report)
	normalizeReportReferenceDuplicates(&report)
	normalizeReportCoverage(&report, events.Events)
	if err = validateReport(report, segments); err != nil {
		out.ReportDiagnostic = failedReportDiagnostic(raw, &candidate, "report_schema", err, segments, events, meta)
		out.Warnings = append(out.Warnings, "六维报告校验失败："+err.Error())
		out.Report = insufficientReport("六维报告未通过证据校验，已保留转写、阅读整理和教学事件")
		out.ReportStatus, out.FailureStage, out.FailureMessage = reportFailed, "report", out.Warnings[len(out.Warnings)-1]
		return out, nil
	}
	if err = validateEventReport(report, events.Events, segments); err != nil {
		out.ReportDiagnostic = failedReportDiagnostic(raw, &candidate, "evidence_validation", err, segments, events, meta)
		out.Warnings = append(out.Warnings, "六维报告证据链校验失败："+err.Error())
		out.Report = insufficientReport("六维报告证据链未通过校验，已保留转写、阅读整理和教学事件")
		out.ReportStatus, out.FailureStage, out.FailureMessage = reportFailed, "report", out.Warnings[len(out.Warnings)-1]
		return out, nil
	}

	progress("audit", "核对整段上下文与建议", 90)
	report.Audit = auditReport(ctx, base, key, report, segments, events)
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	out.Report, out.ReportStatus = report, reportReady
	return out, nil
}

func insufficientReport(limitation string) Report {
	dimensions := make([]Dimension, 0, len(codes))
	for _, code := range codes {
		dimensions = append(dimensions, Dimension{Code: code, Status: "insufficient", Limitation: limitation, IDs: []string{}})
	}
	return Report{Summary: "", IDs: []string{}, Dimensions: dimensions, Observations: []Observation{}}
}

func analyzeVideo(ctx context.Context, base, key, video string, existing []Segment, completedIndices []int, storedChunks []AudioChunk, progress progressFunc, save func([]Segment, []AudioChunk, []int) error) (AnalysisOutput, []ModelMeta, error) {
	progress("probe", "检查视频并提取音频", 5)
	if _, err := probeDuration(ctx, video); err != nil {
		return AnalysisOutput{}, nil, fmt.Errorf("ffprobe检查失败: %w", err)
	}
	dir := filepath.Dir(video)
	audio := filepath.Join(dir, "audio.wav")
	if err := exec.CommandContext(ctx, env("FFMPEG_PATH", "ffmpeg"), "-nostdin", "-v", "error", "-y", "-i", video, "-map", "0:a:0", "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", audio).Run(); err != nil {
		return AnalysisOutput{}, nil, fmt.Errorf("提取失败: %w", err)
	}
	seconds, err := probeDuration(ctx, audio)
	if err != nil {
		return AnalysisOutput{}, nil, fmt.Errorf("音频时长检查失败: %w", err)
	}
	duration := int64(math.Round(seconds * 1000))
	pauses, err := detectSilences(ctx, env("FFMPEG_PATH", "ffmpeg"), audio, duration)
	if err != nil {
		return AnalysisOutput{}, nil, err
	}
	chunks := planChunks(duration, pauses)
	if len(storedChunks) > 0 && !reflect.DeepEqual(chunks, storedChunks) {
		return AnalysisOutput{}, nil, fmt.Errorf("音频切片与保存的检查点不一致，不能续跑")
	}
	if err = save(existing, chunks, completedIndices); err != nil {
		return AnalysisOutput{}, nil, err
	}
	segments := append([]Segment(nil), existing...)
	done := map[int]bool{}
	for _, i := range completedIndices {
		if i < 0 || i >= len(chunks) {
			return AnalysisOutput{}, nil, fmt.Errorf("stored ASR checkpoint exceeds current audio segmentation")
		}
		done[i] = true
	}
	for _, s := range segments {
		if s.ChunkIndex < 0 || s.ChunkIndex >= len(chunks) || !done[s.ChunkIndex] || s.Start != chunks[s.ChunkIndex].Start || s.End != chunks[s.ChunkIndex].End {
			return AnalysisOutput{}, nil, fmt.Errorf("转写检查点来源不一致")
		}
	}
	for i, interval := range chunks {
		if done[i] {
			continue
		}
		start, end := float64(interval.Start)/1000, float64(interval.End)/1000
		progress("asr", fmt.Sprintf("语音转写：第 %d / %d 段（%.1f–%.1f 秒）", i+1, len(chunks), start, end), 10+(i*40/len(chunks)))
		chunk := filepath.Join(dir, "chunk.wav")
		if err = exec.CommandContext(ctx, env("FFMPEG_PATH", "ffmpeg"), "-nostdin", "-v", "error", "-y", "-ss", fmt.Sprintf("%.3f", start), "-i", audio, "-t", fmt.Sprintf("%.3f", end-start), "-c:a", "pcm_s16le", chunk).Run(); err != nil {
			if ctx.Err() != nil {
				return AnalysisOutput{}, nil, ctx.Err()
			}
			return AnalysisOutput{}, nil, fmt.Errorf("第 %d 段音频提取失败，检查点已保留：%w", i+1, err)
		}
		b, e := os.ReadFile(chunk)
		if e != nil {
			return AnalysisOutput{}, nil, fmt.Errorf("第 %d 段音频读取失败，检查点已保留：%w", i+1, e)
		}
		asrOptions := map[string]any{"enable_itn": false}
		// Classroom recordings often mix Chinese, English and pinyin.  Keep
		// this configurable because a monolingual recording may benefit from a
		// narrower hint.  ITN stays disabled so variables, pinyin letters and
		// mathematical speech are not silently rewritten.
		if hints := strings.TrimSpace(env("BAILIAN_ASR_LANGUAGE_HINTS", "zh,en")); hints != "" {
			parts := strings.Split(hints, ",")
			clean := make([]string, 0, len(parts))
			for _, part := range parts {
				if part = strings.TrimSpace(part); part != "" {
					clean = append(clean, part)
				}
			}
			if len(clean) > 0 {
				asrOptions["language_hints"] = clean
			}
		}
		body := map[string]any{"model": env("BAILIAN_ASR_MODEL", "qwen3-asr-flash"), "stream": false, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_audio", "input_audio": map[string]string{"data": "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(b)}}}}}, "asr_options": asrOptions}
		text, _, e := callModel(ctx, base, key, body, false)
		if e != nil {
			if ctx.Err() != nil {
				return AnalysisOutput{}, nil, ctx.Err()
			}
			return AnalysisOutput{}, nil, fmt.Errorf("第 %d 段 ASR 失败，检查点已保留；请求可能已计费：%w", i+1, e)
		}
		if strings.TrimSpace(text) != "" {
			segments = append(segments, Segment{ID: fmt.Sprintf("e-%04d", len(segments)+1), ChunkIndex: i, Start: interval.Start, End: interval.End, Text: text, Boundary: interval.Boundary})
		}
		done[i] = true
		doneList := make([]int, 0, len(done))
		for index := range done {
			doneList = append(doneList, index)
		}
		sort.Ints(doneList)
		if e = save(segments, chunks, doneList); e != nil {
			return AnalysisOutput{}, nil, e
		}
	}
	progress("asr_complete", "原始转写完成，开始文本分析", 52)
	out, e := analyzeSegments(ctx, base, key, segments, progress)
	out.AudioChunks = chunks
	return out, nil, e
}
