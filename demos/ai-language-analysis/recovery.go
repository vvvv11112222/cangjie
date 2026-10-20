package main

import "strings"

const (
	asrComplete   = "complete"
	asrIncomplete = "incomplete"
	asrUnknown    = "unknown"
	reportPending = "pending"
	reportReady   = "ready"
	reportFailed  = "failed"
)

// Legacy checkpoints marked failed requests complete. Absence of text is not
// proof of failure (a successful ASR request can return empty text).
func transcriptWarnings(warnings []string) []string {
	var out []string
	for _, warning := range warnings {
		if strings.HasPrefix(warning, "第 ") && (strings.Contains(warning, "段 ASR 失败") || strings.Contains(warning, "段音频提取失败") || strings.Contains(warning, "段音频读取失败")) {
			out = append(out, warning)
		}
	}
	return out
}

func untrustedLegacyCheckpoint(j Job) bool {
	return j.Mode != "report_only" && j.RevisionNo == 0 && len(j.CompletedASRChunks) > 0 && (j.ASRStatus == asrUnknown || len(transcriptWarnings(j.Warnings)) > 0)
}

func legacyReportStatus(report Report, events EventSet, segments []Segment, warnings []string) string {
	for _, warning := range warnings {
		if strings.HasPrefix(warning, "教学事件提取失败：") || strings.HasPrefix(warning, "六维报告生成失败：") || strings.HasPrefix(warning, "六维报告校验失败：") || strings.HasPrefix(warning, "六维报告证据链校验失败：") {
			return reportFailed
		}
	}
	if len(report.Dimensions) == 0 {
		return reportPending
	}
	if validateEvents(events, segments) != nil {
		return reportFailed
	}
	if validateEventReport(report, events.Events, segments) != nil {
		return reportFailed
	}
	return reportReady
}

// Only enrich legacy records; never replace original transcripts or historical
// evidence. New records use explicit outcomes instead of warning text.
func normalizeJobRecovery(j *Job) {
	if j.ASRStatus == "" {
		j.ASRStatus = asrUnknown
		if len(transcriptWarnings(j.Warnings)) > 0 {
			j.ASRStatus = asrIncomplete
		}
	}
	if j.ReportStatus == "" {
		j.ReportStatus = legacyReportStatus(j.Report, j.Events, j.Segments, j.Warnings)
	}
	if j.ReportStatus == reportFailed && j.FailureStage == "" {
		j.FailureStage = "report"
		for _, warning := range j.Warnings {
			if strings.HasPrefix(warning, "教学事件提取失败：") {
				j.FailureStage = "events"
				if j.Error == "" {
					j.Error = warning
				}
			} else if strings.HasPrefix(warning, "六维报告") && strings.Contains(warning, "失败：") {
				if j.Error == "" {
					j.Error = warning
				}
			}
		}
	}
	for i := range j.ReportVersions {
		v := &j.ReportVersions[i]
		if v.ASRStatus == "" {
			v.ASRStatus = asrUnknown
			if len(transcriptWarnings(v.Warnings)) > 0 {
				v.ASRStatus = asrIncomplete
			}
		}
		if v.ReportStatus == "" {
			v.ReportStatus = legacyReportStatus(v.Report, v.Events, v.Segments, v.Warnings)
		}
	}
	if j.Status == jobSucceeded && (j.ReportStatus != reportReady || j.ASRStatus != asrComplete) {
		j.Status, j.Stage = jobPartial, "partial"
		j.Message = "部分完成：已有结果保留，转写完整性或分析结果需要确认"
	}
	if j.Status == jobPartial {
		j.Retryable = j.RevisionNo > 0
	}
}
