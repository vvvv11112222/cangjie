package main

import "context"

// Both the background queue and the older streaming endpoint use the same
// request contract. Neither path retries or changes the configured model.
func requestSixDimReport(ctx context.Context, base, key string, segments []Segment, reading ReadingResult, events EventSet) (string, ModelMeta, error) {
	prompt, _ := assets.ReadFile("prompt.txt")
	rubric, _ := assets.ReadFile("rubric.md")
	input := map[string]any{
		"original_segments": segments, "reading_paragraphs": reading.Paragraphs,
		"dimension_summary_choices": reportSummaryChoices(events.Events),
		"evidence_bases":            reportBases(events.Events),
		"advice_methods":            adviceMethods(),
		"advice_protocol":           adviceProtocol,
		"output_schema":             reportOutputSchema(events.Events, segments),
		"evidence_protocol":         evidenceProtocol,
		"rubric_version":            rubricVersion, "analysis_version": analysisVersion,
		"semantic_protocol":     semanticProtocol,
		"problem_criteria":      problemRules(),
		"evidence_catalog":      evidenceCatalog(segments),
		"narrative_protocol":    narrativeProtocol,
		"narrative_rules":       narrativeRules(),
		"narrative_field_roles": narrativeFieldRoles(),
		"input_limits":          narrativeInputLimits(),
	}
	if feedback := reportFeedback(ctx); feedback != nil {
		input["previous_validation_feedback"] = feedback
	}
	return textTask(ctx, base, key, string(prompt)+"\n"+string(rubric)+reportContractReminder, input)
}

// JSON Object mode guarantees JSON syntax, not required fields. This schema is
// supplied as generation guidance, NOT advertised as provider-enforced output.
// Local binding and publication validation remain mandatory.
func reportOutputSchema(events []TeachingEvent, segments []Segment) map[string]any {
	text := func() map[string]any { return map[string]any{"type": "string"} }
	selection := func(values []string) map[string]any {
		item := text()
		if len(values) > 0 {
			item["enum"] = values
		}
		return item
	}
	array := func(item map[string]any) map[string]any {
		return map[string]any{"type": "array", "items": item}
	}
	object := func(properties map[string]any, required []string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	sources := make([]string, 0, len(segments))
	for _, segment := range segments {
		sources = append(sources, segment.ID)
	}
	refs := func(ids []string) map[string]any {
		result := array(selection(ids))
		result["uniqueItems"] = true
		if len(ids) == 0 {
			result["maxItems"] = 0
		}
		return result
	}
	keys := []string{}
	for _, basis := range reportBases(events) {
		keys = append(keys, basis.Key)
	}
	quoteIDs := []string{}
	for _, q := range evidenceCatalog(segments) {
		quoteIDs = append(quoteIDs, q.QuoteID)
	}
	observation := object(map[string]any{
		"evidence_key":     selection(keys),
		"observation_type": selection([]string{"highlight", "issue", "observation"}),
		"title":            map[string]any{"type": "string", "minLength": 1},
		"observation_text": map[string]any{"type": "string", "minLength": 1},
		"analysis":         map[string]any{"type": "string", "minLength": 1},
		"coaching": object(map[string]any{
			"focus_quotes": map[string]any{"type": "array", "minItems": 1, "uniqueItems": true,
				"items": object(map[string]any{
					"segment_id": selection(sources), "text": map[string]any{"type": "string", "minLength": 1},
				}, []string{"segment_id", "text"})},
			"suggestion":   map[string]any{"type": "string", "minLength": 1},
			"verification": map[string]any{"type": "string", "minLength": 1},
		}, []string{"focus_quotes", "suggestion", "verification"}),
		"problem_basis": object(map[string]any{
			"criterion":   selection([]string{"transcript_contradiction", "explicit_difficulty", "instruction_conflict"}),
			"quote_ids":   refs(quoteIDs),
			"explanation": text(), "context_check": text(),
		}, []string{"criterion", "quote_ids", "explanation", "context_check"}),
	}, []string{"evidence_key", "observation_type", "title", "observation_text", "analysis"})
	observation["allOf"] = []any{map[string]any{
		"if":   map[string]any{"properties": map[string]any{"observation_type": map[string]any{"const": "issue"}}},
		"then": map[string]any{"required": []string{"problem_basis", "coaching"}},
		"else": map[string]any{"not": map[string]any{"required": []string{"problem_basis"}}},
	}}
	observations := array(observation)
	if len(keys) == 0 {
		observations["maxItems"] = 0
	}
	dimensionBranches := []any{}
	for _, choice := range reportEventChoices(events) {
		statuses := []string{"observed", "insufficient", "not_applicable"}
		if len(choice.Events) == 0 {
			statuses = []string{"insufficient"}
		}
		dimensionBranches = append(dimensionBranches, object(map[string]any{
			"dimension_code": selection([]string{choice.Code}), "coverage_status": selection(statuses),
			"summary": text(), "limitation": text(), "summary_evidence_ids": refs(choice.SourceIDs),
		}, []string{"dimension_code", "coverage_status", "summary", "limitation", "summary_evidence_ids"}))
	}
	dimensions := array(map[string]any{"anyOf": dimensionBranches})
	dimensions["minItems"], dimensions["maxItems"] = 6, 6
	return object(map[string]any{
		"summary": text(), "summary_evidence_ids": refs(sources),
		"dimensions": dimensions, "observations": observations,
	}, []string{"summary", "summary_evidence_ids", "dimensions", "observations"})
}

const reportContractReminder = `
【交付前最后核对：证据完整优先于文字精简】
修改方向采用 grounded-advice-v7：在需要建议的发现内直接填写coaching，不输出顶层的建议序号。通常优先保留1–2条互不重复、最值得尝试的建议，不机械限制为一条；无需建议的非问题发现省略coaching，所有issue必须填写。每条coaching的focus_quotes逐句逐字引用本条依据event.quotes并保留segment_id，不得合并成事件目录中没有的长引文；程序兼容接收完全由本条事件连续覆盖的原文摘录，但不接受其中夹带未选事件的词句。suggestion和verification只能依赖本条依据事件已说明的内容；若跨事件联系有教学价值，必须先作为同一已选事件依据，不可仅靠整段上下文代替关联。例如，单独选择炒鸡蛋原句，不能在建议中借用另一个高温解释事件中才出现的“展开、聚集”。程序逐条校验建议是否能由所选事件依据支持；只要建议本身越出了已选依据，即使focus_quotes逐字正确也不能自动通过结构校验，此时应结合事件选择与全文语义复核。其余建议字段要求不变。
按用户数据中的 output_schema 组织 JSON；该 schema 是输出要求，不是需要复制到报告的内容。
每条 observations 先从 evidence_bases 选择唯一一个 evidence_key，再写该项原文直接支持的事实和解释。维度和全部来源已由该项固定，结论中严禁另填 dimension_code、event_ids、evidence_ids；这些字段由程序生成，不是让你省略证据。
只有目标声明，就只能说交代目标；只有计划，就只能说预告顺序，不能扩写成已经完成多视角讲解。选项的 scope 是范围限制，不是可以复制的分析结论。没有有价值的发现可以少写，不借其他选项扩大本条事实。
dimension_summary_choices 仅用于六维摘要核对；allowed_summary_source_ids 是该维摘要可引用的来源短表。正文不要出现内部键，evidence_key 必须保留于专用字段。
若存在 previous_validation_feedback，其中记录上次未发布候选的关联错误及原文，仅用于避免重犯，不是课堂结论或可直接复用的编号。事件已重新提取，必须使用本次目录；不能照抄旧结论、猜测对应编号、把错误事件改维度或删关联来通过校验。
总体及维度 summary_evidence_ids 保留真实原文来源，同一数组内去重。dimensions 必须六维各一次，无依据维度按前述规则写 insufficient。
只有issue必须额外提供problem_basis，按problem_criteria说明问题类型、evidence_catalog原话编号、问题理由以及核对前后全文后为何仍成立。其他类型省略problem_basis。不符合这三类正面依据的专业待核实项不判课堂问题，也不借标题、summary或limitation绕过门槛。
只输出报告 JSON 对象，不输出 schema、解释或核对过程。`
