package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

const reportAuditVersion = "context-review-v9"

// An advisory model review never edits the report or certifies subject accuracy.
type ReportAudit struct {
	Version           string                 `json:"version"`
	RubricVersion     string                 `json:"rubric_version,omitempty"`
	RubricSHA256      string                 `json:"rubric_sha256,omitempty"`
	NarrativeProtocol string                 `json:"narrative_protocol,omitempty"`
	Status            string                 `json:"status"`
	Message           string                 `json:"message"`
	CheckedTargets    []string               `json:"checked_targets"`
	Findings          []AuditFinding         `json:"findings"`
	Request           ModelMeta              `json:"request"`
	CreatedAt         time.Time              `json:"created_at"`
	RawResponse       string                 `json:"raw_response,omitempty"`
	Diagnostic        string                 `json:"diagnostic,omitempty"`
	RejectedFindings  []RejectedAuditFinding `json:"rejected_findings,omitempty"`
}
type AuditFinding struct {
	Target            string             `json:"target"`
	Fields            []string           `json:"fields"`
	Kind              string             `json:"kind"`
	Criterion         string             `json:"criterion,omitempty"`
	SuggestedCoverage string             `json:"suggested_coverage_status,omitempty"`
	Reason            string             `json:"reason"`
	SourceIDs         []string           `json:"source_ids"`
	ReportQuotes      []AuditReportQuote `json:"report_quotes,omitempty"`
	SourceQuotes      []AuditSourceQuote `json:"source_quotes,omitempty"`
}
type AuditReportQuote struct {
	Field string `json:"field"`
	Text  string `json:"text"`
}
type AuditSourceQuote struct {
	SegmentID string `json:"segment_id"`
	Text      string `json:"text"`
}
type RejectedAuditFinding struct {
	Finding AuditFinding `json:"finding"`
	Reason  string       `json:"reason"`
}
type auditDraft struct {
	CheckedTargets []string       `json:"checked_targets"`
	Findings       []AuditFinding `json:"findings"`
}
type auditTarget struct {
	Key        string            `json:"key"`
	Fields     []string          `json:"fields"`
	FieldRoles map[string]string `json:"field_roles"`
}

func reportAuditTargets(report Report) []auditTarget {
	roles := narrativeFieldRoles()
	targets := []auditTarget{{Key: "summary", Fields: []string{"summary"}, FieldRoles: map[string]string{"summary": roles["summary"]}}}
	for _, d := range report.Dimensions {
		targets = append(targets, auditTarget{Key: "dimension:" + d.Code, Fields: []string{"coverage_status", "summary", "limitation"}, FieldRoles: map[string]string{
			"coverage_status": roles["coverage_status"],
			"summary":         roles["dimension_summary"],
			"limitation":      roles["limitation"],
		}})
	}
	for i := range report.Observations {
		targets = append(targets, auditTarget{Key: fmt.Sprintf("observation:%d", i+1), Fields: []string{"title", "observation_text", "analysis", "suggestion", "verification"}, FieldRoles: map[string]string{
			"title":            roles["title"],
			"observation_text": roles["observation_text"],
			"analysis":         roles["analysis"],
			"suggestion":       roles["suggestion"],
			"verification":     roles["verification"],
		}})
	}
	return targets
}
func validateAuditCoverage(d auditDraft, report Report) error {
	if d.CheckedTargets == nil || d.Findings == nil || len(d.Findings) > 80 {
		return fmt.Errorf("复核列表不完整")
	}
	targets := map[string][]string{}
	for _, t := range reportAuditTargets(report) {
		targets[t.Key] = t.Fields
	}
	seen := map[string]bool{}
	for _, t := range d.CheckedTargets {
		if _, ok := targets[t]; !ok || seen[t] {
			return fmt.Errorf("复核范围不匹配")
		}
		seen[t] = true
	}
	if len(seen) != len(targets) {
		return fmt.Errorf("复核遗漏报告条目")
	}
	return nil
}
func auditFieldText(report Report, target, field string) string {
	if target == "summary" && field == "summary" {
		return report.Summary
	}
	for _, d := range report.Dimensions {
		if target == "dimension:"+d.Code {
			if field == "coverage_status" {
				return d.Status
			}
			if field == "summary" {
				return d.Summary
			}
			if field == "limitation" {
				return d.Limitation
			}
		}
	}
	for i, o := range report.Observations {
		if target == fmt.Sprintf("observation:%d", i+1) {
			return map[string]string{"title": o.Title, "observation_text": o.Text, "analysis": o.Analysis, "suggestion": o.Suggestion, "verification": o.Verification}[field]
		}
	}
	return ""
}
func validateAudit(d auditDraft, report Report, segments []Segment) error {
	if err := validateAuditCoverage(d, report); err != nil {
		return err
	}
	for _, f := range d.Findings {
		if err := validateAuditFinding(f, report, segments); err != nil {
			return err
		}
	}
	return nil
}
func validateAuditFinding(f AuditFinding, report Report, segments []Segment) error {
	targets := map[string][]string{}
	for _, t := range reportAuditTargets(report) {
		targets[t.Key] = t.Fields
	}
	sources := map[string]bool{}
	sourceText := map[string]string{}
	for _, s := range segments {
		sources[s.ID] = true
		sourceText[s.ID] = s.Text
	}
	fields, ok := targets[f.Target]
	if !ok || len(f.Fields) == 0 || strings.TrimSpace(f.Reason) == "" || f.SourceIDs == nil {
		return fmt.Errorf("复核疑点缺少定位或说明")
	}
	if !contains([]string{"context_conflict", "transcript_uncertainty", "subject_claim", "unsupported_judgment", "advice_quality"}, f.Kind) {
		return fmt.Errorf("未知复核类型")
	}
	if !validRule(f.Criterion, reviewRules()) {
		return fmt.Errorf("复核提示没有选择共享标准中的判断依据")
	}
	if f.Criterion == "relation_fidelity" && (len(f.SourceIDs) == 0 || contains(f.Fields, "coverage_status")) {
		return fmt.Errorf("推理关系疑点须定位具体叙述并提供原关系的对照原文，不能用来直接修改依据状态")
	}
	if f.Criterion == "dimension_definition" && (!strings.HasPrefix(f.Target, "dimension:") || len(f.Fields) != 1 || f.Fields[0] != "coverage_status") {
		return fmt.Errorf("维度归属疑点须定位依据状态，不能据此连带否定小结或建议")
	}
	if f.Criterion == "dimension_definition" {
		if !contains([]string{"observed", "insufficient", "not_applicable"}, f.SuggestedCoverage) || f.SuggestedCoverage == auditFieldText(report, f.Target, "coverage_status") || len(f.SourceIDs) == 0 {
			return fmt.Errorf("分类提醒须指出与当前不同的依据状态并提供对照原文；分类正确的说明不作为问题提醒")
		}
	} else if f.SuggestedCoverage != "" {
		return fmt.Errorf("只有分类提醒可以填写建议的依据状态")
	}
	fieldSeen := map[string]bool{}
	for _, field := range f.Fields {
		if !contains(fields, field) || fieldSeen[field] {
			return fmt.Errorf("复核字段不匹配")
		}
		fieldSeen[field] = true
	}
	refSeen := map[string]bool{}
	for _, id := range f.SourceIDs {
		if !sources[id] || refSeen[id] {
			return fmt.Errorf("复核来源无效")
		}
		refSeen[id] = true
	}
	if f.Kind == "context_conflict" && len(f.SourceIDs) == 0 {
		return fmt.Errorf("上下文冲突缺少对照来源")
	}
	quotedFields := map[string]bool{}
	for _, q := range f.ReportQuotes {
		if !contains(f.Fields, q.Field) || quotedFields[q.Field] || strings.TrimSpace(q.Text) == "" || !strings.Contains(auditFieldText(report, f.Target, q.Field), q.Text) {
			return fmt.Errorf("复核未逐字定位报告原句")
		}
		if q.Field == "coverage_status" && q.Text != auditFieldText(report, f.Target, q.Field) {
			return fmt.Errorf("复核未准确引用依据状态")
		}
		quotedFields[q.Field] = true
	}
	if len(quotedFields) != len(f.Fields) {
		return fmt.Errorf("复核缺少被质疑字段的报告原句")
	}
	quotedSources := map[string]bool{}
	for _, q := range f.SourceQuotes {
		if !refSeen[q.SegmentID] || quotedSources[q.SegmentID] || strings.TrimSpace(q.Text) == "" || !strings.Contains(sourceText[q.SegmentID], q.Text) {
			return fmt.Errorf("复核对照原文摘录不匹配")
		}
		quotedSources[q.SegmentID] = true
	}
	if len(quotedSources) != len(f.SourceIDs) {
		return fmt.Errorf("复核缺少对照原文摘录")
	}
	return nil
}
func auditReport(ctx context.Context, base, key string, report Report, segments []Segment, events EventSet) *ReportAudit {
	a := &ReportAudit{Version: reportAuditVersion, Status: "unavailable", CheckedTargets: []string{}, Findings: []AuditFinding{}, CreatedAt: time.Now().UTC()}
	a.NarrativeProtocol = narrativeProtocol
	if env("BAILIAN_REPORT_AUDIT", "1") == "0" {
		a.Status, a.Message = "not_run", "本报告未启用全文复核，结论与建议仍需核对。"
		return a
	}
	prompt, _ := assets.ReadFile("audit_prompt.txt")
	rubric, _ := assets.ReadFile("rubric.md")
	a.RubricVersion = rubricVersion
	a.RubricSHA256 = fmt.Sprintf("%x", sha256.Sum256(rubric))
	report.Audit = nil
	template := auditResponseTemplate(report)
	// Generation and review share the exact embedded rubric. This is a review
	// task, however: the rubric's generation protocol must not change the output.
	system := string(prompt) + "\n【生成与复核共用的六维标准】\n" + string(rubric) + "\n【本次任务】仅按前述复核协议输出 checked_targets 和 findings；不生成或修改六维报告。标准中的生成字段仅说明原报告约束，不是本次输出字段。\n"
	raw, meta, err := textTask(ctx, base, key, system, map[string]any{"original_segments": segments, "events": events, "candidate_report": report, "review_targets": reportAuditTargets(report), "response_template": template, "rubric_version": a.RubricVersion, "rubric_sha256": a.RubricSHA256, "advice_protocol": adviceProtocol, "semantic_protocol": semanticProtocol, "review_criteria": reviewRules(), "narrative_protocol": narrativeProtocol, "narrative_rules": narrativeRules(), "narrative_field_roles": narrativeFieldRoles(), "input_limits": narrativeInputLimits()})
	a.Request = meta
	a.RawResponse = raw
	a.Message = "全文复核未完成；原报告已保留，结论与建议尚需核对。"
	if err != nil {
		a.Diagnostic = "模型请求未完成"
		if strings.Contains(err.Error(), "输入预算") {
			a.Diagnostic = err.Error()
		}
		return a
	}
	var d auditDraft
	err = strictJSON(raw, &d)
	if err != nil {
		a.Diagnostic = "复核输出不是符合约定的 JSON"
	} else if err = validateAuditCoverage(d, report); err != nil {
		a.Diagnostic = err.Error()
	}
	if err != nil {
		a.Message = "全文复核返回的定位或格式不完整；原报告已保留，不能视为已完成复核。"
		return a
	}
	a.CheckedTargets = d.CheckedTargets
	for _, f := range d.Findings {
		if err := validateAuditFinding(f, report, segments); err != nil {
			a.RejectedFindings = append(a.RejectedFindings, RejectedAuditFinding{Finding: f, Reason: err.Error()})
		} else {
			a.Findings = append(a.Findings, f)
		}
	}
	a.Status, a.Message = "no_flags", "模型全文复核未发现明显疑点；这不代表专业正确或通过人工验收。"
	if len(a.Findings) > 0 {
		a.Status, a.Message = "needs_review", "模型提出了待核对的分析或建议，也可能误报，不等于已确认的课堂问题。请对照报告原句与课堂原文判断；原报告保留。"
	}
	if len(a.RejectedFindings) > 0 {
		a.Status = "partial_review"
		a.Message = fmt.Sprintf("全文复核部分完成：%d条提示通过引用与判断依据检查，%d条提示的原文引用或判断依据不完整，未作为问题展示；结论与建议仍需核对。", len(a.Findings), len(a.RejectedFindings))
	}
	return a
}

// Supply the exact coverage list instead of asking the model to expand an
// illustrative list that may have fewer observations than the real report.
func auditResponseTemplate(report Report) auditDraft {
	d := auditDraft{CheckedTargets: []string{}, Findings: []AuditFinding{}}
	for _, target := range reportAuditTargets(report) {
		d.CheckedTargets = append(d.CheckedTargets, target.Key)
	}
	return d
}
