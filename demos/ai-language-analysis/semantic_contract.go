package main

import (
	"fmt"
	"strings"
	"unicode"
)

const semanticProtocol = "grounded-feedback-v4"

// These gates check explicit reasoning material and source links, not whether
// an LLM's interpretation is true. They must never reclassify or delete events.
type ActivityInstruction struct {
	LearnerAction string   `json:"learner_action"`
	QuoteIDs      []string `json:"quote_ids"`
}

type ProblemBasis struct {
	Criterion    string   `json:"criterion"`
	QuoteIDs     []string `json:"quote_ids"`
	Explanation  string   `json:"explanation"`
	ContextCheck string   `json:"context_check"`
}

type semanticRule struct {
	ID       string `json:"criterion"`
	Use      string `json:"use_when"`
	Boundary string `json:"boundary"`
}

func problemRules() []semanticRule {
	return []semanticRule{
		{"transcript_contradiction", "原文至少两处明确陈述相互冲突，且全文没有澄清、条件变化或自我纠正", "必须引出冲突两端；不是用外部知识判教师知识错误"},
		{"explicit_difficulty", "原文明确表达具体不理解或不清楚，且后文仍未澄清", "只描述表达出的具体困难，不推断全班理解；说话人不明确时不能认定学生身份"},
		{"instruction_conflict", "原文中的活动要求彼此冲突，且全文没有澄清或合理的任务变更", "必须引出两处要求；不能由没录到产出要求推断任务混乱"},
	}
}

func reviewRules() []semanticRule {
	return []semanticRule{
		{"context_consistency", "报告概括与全文实际陈述冲突或遗漏改变结论的上下文", "多段可共同支持小结，不要求每段分别包含整句小结；质疑具体措辞，不连带否定其他字段"},
		{"language_fidelity", "报告的中文译义改变了原文动作、对象或情境", "结合相邻块核对实际含义，例如加热语境中的炒鸡蛋不能写成仅打散生鸡蛋；允许自然中文转述，不因未逐字翻译或未补教材细节就质疑"},
		{"relation_fidelity", "报告把原文的推理方向、前提条件、因果或适用范围改成另一种关系", "必须引用支持原关系及条件的原文，指出报告哪一处改变关系；不以外部公式推导或教材完整性要求代替对照，不自动判教师知识错误"},
		{"speaker_uncertainty", "报告把说话人、等待时间或学习效果的不确定性写成已确认事实", "未来观察学生回答不等于断言回答已经发生"},
		{"subject_verification", "报告补入未经来源支持的专业机制、普遍规律或标准答案", "只提示需核实，不自行引入完整定义标准来判教师缺陷；未来复述原例不等于要求新机制"},
		{"claim_scope", "报告把个例推成普遍规律、可能价值写成已证实效果，或把未录到写成未做到", "明确的输入边界通常合理；不因没有学生推理就否定教师展示推理的价值"},
		{"dimension_definition", "依据状态与共享六维标准不一致", "thinking允许教师问题和推理；reasoning可同时支持content和thinking，不要求纯思维事件；无活动指令应为依据不足，不能直接判不适用"},
		{"advice_relevance", "未来建议确实要求教师补讲已讲内容、预设不存在的安排，或要求未铺垫的知识", "教师已讲与学生未来练习不同；检查理由不等于重复列举；必须指出不当的具体要求，不自行添加模板没有要求的答案"},
	}
}

func validRule(id string, rules []semanticRule) bool {
	for _, rule := range rules {
		if rule.ID == id {
			return true
		}
	}
	return false
}

func validateActivityInstructions(set EventSet) error {
	for i, event := range set.Events {
		if err := validateActivityInstruction(event, i); err != nil {
			return err
		}
	}
	return nil
}

// validateActivityInstruction validates one event so extraction can isolate a
// single semantically invalid activity while keeping unrelated events usable.
// The event itself is still retained in diagnostics; this function never
// changes its type or quietly rewrites its evidence.
func validateActivityInstruction(event TeachingEvent, index int) error {
	if event.Type != "organization" {
		if event.Activity != nil {
			return fmt.Errorf("第%d条课堂依据不是学生活动，却填写了活动指令材料；原始返回已保留", index+1)
		}
		return nil
	}
	if event.Activity == nil || strings.TrimSpace(event.Activity.LearnerAction) == "" || len(event.Activity.QuoteIDs) == 0 {
		return fmt.Errorf("第%d条活动组织依据未说明学生要做什么及对应原话，暂不能据此评价活动组织；已有转写已保留，无需重新转写", index+1)
	}
	known, seen := map[string]bool{}, map[string]bool{}
	for _, q := range event.Quotes {
		if q.QuoteID != "" {
			known[q.QuoteID] = true
		}
	}
	for _, id := range event.Activity.QuoteIDs {
		if !known[id] || seen[id] {
			return fmt.Errorf("第%d条活动指令没有准确关联本条原话；原始返回已保留，无需重新转写", index+1)
		}
		seen[id] = true
		for _, quote := range event.Quotes {
			if quote.QuoteID == id && !explicitLearnerTaskQuote(quote.Text) {
				return fmt.Errorf("第%d条活动指令的原话没有明确要求学生完成任务或产出；“强调两点、注意听讲”等教师讲解安排不能作为活动依据，原始返回已保留，无需重新转写", index+1)
			}
		}
	}
	return nil
}

// RejectedEvent is an internal audit record for one event that was isolated
// after the model returned an otherwise usable event set. It is intentionally
// separate from the public EventSet so rejected evidence cannot feed a report.
type RejectedEvent struct {
	Index  int           `json:"index"`
	Event  TeachingEvent `json:"event"`
	Reason string        `json:"reason"`
}

func isolateInvalidActivityEvents(set EventSet) (EventSet, []RejectedEvent) {
	valid := EventSet{Events: make([]TeachingEvent, 0, len(set.Events))}
	rejected := make([]RejectedEvent, 0)
	for i, event := range set.Events {
		if err := validateActivityInstruction(event, i); err != nil {
			rejected = append(rejected, RejectedEvent{Index: i + 1, Event: cloneValue(event), Reason: err.Error()})
			continue
		}
		valid.Events = append(valid.Events, event)
	}
	return valid, rejected
}

// ActivityInstruction is a semantic material gate. A generated learner_action
// such as “注意两个要点” cannot turn teacher narration into a student task;
// every cited quote must contain a task/output marker. Attention reminders may
// accompany a real task, but politeness or learner names alone are not tasks.
func explicitLearnerTaskQuote(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	clauses := strings.FieldsFunc(lower, func(r rune) bool {
		return strings.ContainsRune(",;.!?，；。！？\n", r)
	})
	for _, clause := range clauses {
		// A separate teacher-narration clause must not mask a learner task.
		narration := false
		for _, teacher := range []string{"i want to", "i will", "we will", "i explain", "i compare", "我要", "我会", "我来"} {
			if strings.Contains(clause, teacher) {
				narration = true
				break
			}
		}
		if narration {
			continue
		}
		for _, passive := range []string{"pay attention", "focus on", "listen", "notice", "remember", "keep in mind", "注意", "关注", "听讲", "记住", "留意"} {
			clause = strings.ReplaceAll(clause, passive, " ")
		}
		for _, marker := range []string{"discuss", "write", "calculate", "explain", "compare", "solve", "submit", "turn in", "work in pairs", "in groups", "answer", "complete", "show your", "讨论", "写", "计算", "解释", "比较", "解决", "提交", "完成", "展示", "分组", "回答", "复述", "朗读", "画出", "圈出", "标注", "举手"} {
			if strings.Contains(clause, marker) {
				// Match English words, so e.g. "compared" or "writer"
				// cannot supply an imperative "compare" or "write" marker.
				if marker[0] < unicode.MaxASCII && !containsTaskWords(clause, marker) {
					continue
				}
				return true
			}
		}
	}
	return false
}

func containsTaskWords(text, marker string) bool {
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) })
	return strings.Contains(" "+strings.Join(words, " ")+" ", " "+marker+" ")
}

func validateProblemBasis(finding ObservationDraft, basis ReportBasis, segments []Segment) error {
	p := finding.Problem
	if finding.Type != "issue" {
		if p != nil {
			return fmt.Errorf("问题依据只能用于明确的问题发现，不能混入其他课堂分析")
		}
		return nil
	}
	if p == nil || !validRule(p.Criterion, problemRules()) || strings.TrimSpace(p.Explanation) == "" || strings.TrimSpace(p.ContextCheck) == "" {
		return fmt.Errorf("标为课堂问题的发现缺少可核对的问题依据或全文核对说明，不能仅凭缺少证据认定教师问题；原始返回已保留，无需重新转写")
	}
	minimum := 1
	if p.Criterion == "transcript_contradiction" || p.Criterion == "instruction_conflict" {
		minimum = 2
	}
	if len(p.QuoteIDs) < minimum {
		return fmt.Errorf("课堂问题未提供足够的原话对照；原始返回已保留，无需重新转写")
	}
	known := map[string]EventQuote{}
	for _, q := range evidenceCatalog(segments) {
		known[q.QuoteID] = q
	}
	seen := map[string]bool{}
	for _, id := range p.QuoteIDs {
		q, ok := known[id]
		if !ok || seen[id] {
			return fmt.Errorf("课堂问题的原话关联无效或重复；原始返回已保留，无需重新转写")
		}
		seen[id] = true
		linked := false
		for _, source := range basis.Evidence.Quotes {
			if source.SegmentID == q.SegmentID && (strings.Contains(source.Text, q.Text) || strings.Contains(q.Text, source.Text)) {
				linked = true
			}
		}
		if !linked {
			return fmt.Errorf("课堂问题的原话未全部包含在所选发现依据中；原始返回已保留，无需重新转写")
		}
	}
	return nil
}

// Preserve a rejected extraction separately from public events and reports.
type eventContractError struct {
	raw      string
	err      error
	partial  EventSet
	rejected []RejectedEvent
}

func (e *eventContractError) Error() string { return e.err.Error() }
func (e *eventContractError) Unwrap() error { return e.err }
