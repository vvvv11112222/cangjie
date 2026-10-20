package main

import (
	"fmt"
	"strings"
)

// A basis is planned before generation. Selecting it explicitly selects one
// event in one dimension, including ALL of that event's cross-block quotes.
// This is a generation contract; stored/public Report remains unchanged.
type ReportBasis struct {
	Key        string        `json:"evidence_key"`
	Code       string        `json:"dimension_code"`
	Dimension  string        `json:"dimension_name"`
	Scope      string        `json:"scope"`
	Evidence   TeachingEvent `json:"event"`
	AdviceKeys []string      `json:"allowed_advice_keys"`
}

type DimensionSummaryChoice struct {
	Code        string   `json:"dimension_code"`
	Name        string   `json:"dimension_name"`
	HasEvidence bool     `json:"has_evidence"`
	SourceIDs   []string `json:"allowed_summary_source_ids"`
}

func reportSummaryChoices(events []TeachingEvent) []DimensionSummaryChoice {
	choices := []DimensionSummaryChoice{}
	for _, choice := range reportEventChoices(events) {
		choices = append(choices, DimensionSummaryChoice{Code: choice.Code, Name: dimensionLabel(choice.Code),
			HasEvidence: len(choice.Events) > 0, SourceIDs: cloneValue(choice.SourceIDs)})
	}
	return choices
}

func reportBases(events []TeachingEvent) []ReportBasis {
	bases := []ReportBasis{}
	for _, choice := range reportEventChoices(events) {
		for _, event := range choice.Events {
			bases = append(bases, ReportBasis{
				Key: fmt.Sprintf("basis-%03d", len(bases)+1), Code: choice.Code,
				Dimension: dimensionLabel(choice.Code), Scope: basisScope(event.Type, choice.Code),
				Evidence:   cloneValue(event),
				AdviceKeys: allowedAdvice(event.Type),
			})
		}
	}
	return bases
}

func basisScope(kind, code string) string {
	if kind == "goal" {
		return "仅分析明确说出的目标；不能据此认定已讲解、比较或统整后续知识。"
	}
	if kind == "transition" {
		return "仅分析口头安排、先后顺序或转场；预告不等于已完成，不评价精确快慢。"
	}
	if code == "management" {
		return "仅分析明确要求学生做事的活动指令；课程介绍或教学计划不足以形成结论。"
	}
	if code == "technology" {
		return "仅记录明确提到的工具和用途；提及不等于操作或有效，通常无需单独形成结论。"
	}
	return "仅分析所选事件引文直接支持的具体做法，不借其他事件扩大事实，不推断学生学习效果。"
}

type ObservationDraft struct {
	Key      string         `json:"evidence_key"`
	Type     string         `json:"observation_type"`
	Title    string         `json:"title"`
	Text     string         `json:"observation_text"`
	Analysis string         `json:"analysis"`
	Problem  *ProblemBasis  `json:"problem_basis,omitempty"`
	Coaching *CoachingDraft `json:"coaching,omitempty"`
}

type ReportDraft struct {
	Summary      string             `json:"summary"`
	IDs          []string           `json:"summary_evidence_ids"`
	Dimensions   []Dimension        `json:"dimensions"`
	Observations []ObservationDraft `json:"observations"`
	// Accept the older generation hint without using it to discard advice.
	// New requests express advice only inside its owning observation.
	NextStep *int `json:"next_step_observation,omitempty"`
}

func decodeReportDraft(raw string) (ReportDraft, error) {
	var draft ReportDraft
	if err := strictJSON(raw, &draft); err != nil {
		// Do not expose internal field names or model fragments in the UI.
		return draft, fmt.Errorf("模型未按本次报告格式返回：每条结论须选择课堂依据，修改方向须附本条原文和观察方法，不能另填关联列表；已有转写已保留，无需重新转写")
	}
	return draft, nil
}

func bindReportDraft(draft ReportDraft, events []TeachingEvent, segments []Segment) (Report, error) {
	report := Report{AnalysisVersion: analysisVersion, Summary: draft.Summary,
		IDs: cloneValue(draft.IDs), Dimensions: cloneValue(draft.Dimensions)}
	if draft.Observations != nil {
		report.Observations = []Observation{}
	}
	known := map[string]ReportBasis{}
	for _, basis := range reportBases(events) {
		known[basis.Key] = basis
	}
	if draft.NextStep != nil && (*draft.NextStep < 0 || *draft.NextStep > len(draft.Observations)) {
		return report, &coachingContractError{"模型填写了无效的旧版建议序号；原始返回已保留，无需重新转写"}
	}
	for i, finding := range draft.Observations {
		o := Observation{Type: finding.Type, Title: finding.Title, Text: finding.Text,
			Analysis: finding.Analysis}
		basis, ok := known[finding.Key]
		if !ok {
			reason := "选择了本次目录中不存在的课堂依据"
			code := "unknown_basis"
			if strings.TrimSpace(finding.Key) == "" {
				reason, code = "模型漏填了课堂依据选择", "missing_basis"
			}
			return report, &EvidenceLinkError{Code: code, ObservationIndex: i + 1, Title: finding.Title,
				Message: fmt.Sprintf("第 %d 条结论「%s」%s；已有转写已保留，无需重新转写", i+1, finding.Title, reason)}
		}
		var err error
		if err = validateProblemBasis(finding, basis, segments); err != nil {
			return report, fmt.Errorf("第 %d 条发现：%w", i+1, err)
		}
		adviceKey := "none"
		legacySelected := draft.NextStep != nil && *draft.NextStep == i+1
		if finding.Type == "issue" || finding.Coaching != nil || legacySelected {
			keys := allowedAdvice(basis.Evidence.Type)
			if len(keys) < 2 {
				return report, &coachingContractError{fmt.Sprintf("第 %d 条发现只有安排或提及，当前没有适用的教学建议方法；原始返回已保留，无需重新转写", i+1)}
			}
			adviceKey = keys[1]
			o.Suggestion, o.Verification, err = bindCoaching(finding.Coaching, basis, segments)
			if err != nil {
				return report, fmt.Errorf("第 %d 条发现：%w", i+1, err)
			}
		}
		o.AdviceTemplate = adviceKey
		o.Code, o.EventIDs = basis.Code, []string{basis.Evidence.ID}
		report.Observations = append(report.Observations, o)
	}
	// Keep the same independent publication guard; never infer associations from
	// prose, change event classification or discard a rejected association.
	if err := bindObservationEvidence(&report, events, segments); err != nil {
		return report, err
	}
	return report, nil
}
