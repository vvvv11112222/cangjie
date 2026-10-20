package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

// These are hand-designed regression examples, NOT teacher-labelled classroom
// gold data. Each pair changes one judgement while keeping its source fixed.
type auditCalibrationCase struct {
	ID        string    `json:"id"`
	Purpose   string    `json:"purpose"`
	Segments  []Segment `json:"original_segments"`
	Events    EventSet  `json:"events"`
	Report    Report    `json:"candidate_report"`
	WantFlag  bool      `json:"expected_flag"`
	Target    string    `json:"expected_target,omitempty"`
	Field     string    `json:"expected_field,omitempty"`
	Rationale string    `json:"expectation_rationale"`
}

func calibrationScene(texts, kinds, summaries []string) auditCalibrationCase {
	c := auditCalibrationCase{Events: EventSet{Events: []TeachingEvent{}}}
	c.Report = Report{AnalysisVersion: analysisVersion, Summary: "仅依据所提供片段描述口头教学行为，不评价整节课或学习效果。", IDs: []string{}, Dimensions: []Dimension{}, Observations: []Observation{}}
	for i, text := range texts {
		id := fmt.Sprintf("source-%d", i+1)
		c.Segments = append(c.Segments, Segment{ID: id, Start: int64(i * 30000), End: int64((i + 1) * 30000), Text: text})
		c.Report.IDs = append(c.Report.IDs, id)
		c.Events.Events = append(c.Events.Events, TeachingEvent{ID: fmt.Sprintf("event-%d", i+1), Type: kinds[i], Summary: summaries[i], DimensionCodes: eventDimensions[kinds[i]], Quotes: []EventQuote{{SegmentID: id, Text: text}}})
	}
	for _, code := range codes {
		d := Dimension{Code: code, Status: "insufficient", Limitation: "本片段未找到该维度所需的明确口头依据，不能据此评价该维度。", IDs: []string{}}
		for _, event := range c.Events.Events {
			if contains(event.DimensionCodes, code) {
				d.Status, d.Limitation = "observed", "只记录文本中可确认的行为，不推断学生理解或整节课效果。"
				if d.Summary != "" {
					d.Summary += "；"
				}
				d.Summary += event.Summary
				d.IDs = append(d.IDs, event.Quotes[0].SegmentID)
			}
		}
		c.Report.Dimensions = append(c.Report.Dimensions, d)
	}
	return c
}

func calibrationDimension(c *auditCalibrationCase, code string) *Dimension {
	for i := range c.Report.Dimensions {
		if c.Report.Dimensions[i].Code == code {
			return &c.Report.Dimensions[i]
		}
	}
	panic("unknown calibration dimension")
}

func auditCalibrationCases() []auditCalibrationCase {
	var cases []auditCalibrationCase
	addPair := func(base auditCalibrationCase, name, purpose, field, target, clean, risky, rationale string) {
		for _, bad := range []bool{false, true} {
			c := cloneValue(base)
			c.ID, c.Purpose, c.Rationale = name+"-bounded", purpose, rationale
			text := clean
			if bad {
				c.ID, c.WantFlag, c.Target, c.Field = name+"-overclaim", true, target, field
				text = risky
			}
			if strings.HasPrefix(target, "dimension:") {
				d := calibrationDimension(&c, strings.TrimPrefix(target, "dimension:"))
				if field == "limitation" {
					d.Limitation = text
				} else if field == "coverage_status" {
					d.Status = text
					if text != "observed" {
						d.Summary, d.IDs = "", []string{}
					} else if d.Summary == "" {
						d.Summary, d.IDs = base.Events.Events[0].Summary, []string{base.Segments[0].ID}
					}
				} else {
					d.Summary = text
				}
			} else if target == "summary" {
				c.Report.Summary = text
			} else {
				switch field {
				case "title":
					c.Report.Observations[0].Title = text
				case "observation_text":
					c.Report.Observations[0].Text = text
				case "suggestion":
					c.Report.Observations[0].Suggestion = text
				case "analysis":
					c.Report.Observations[0].Analysis = text
				case "verification":
					c.Report.Observations[0].Verification = text
				}
			}
			cases = append(cases, c)
		}
	}
	questions := calibrationScene([]string{"What can make an enzyme stop working? Yes, heat. What's yours? pH. Any other thoughts?"}, []string{"question"}, []string{"围绕影响酶的因素提问，转写还出现简短接话但未标明说话人"})
	addPair(questions, "management", "普通知识提问不等于活动组织，也不能由未见任务推出组织混乱", "limitation", "dimension:management",
		"本片段出现知识提问，但未找到分组、活动步骤或产出要求，不能据此评价活动组织。",
		"本片段没有分组或任务指令，说明教师活动组织混乱、学生没有明确任务。",
		"知识提问可支持思维线索，不自动支持管理；谨慎说明不评价管理应保留，把未见指令变成混乱判断应提醒。")
	addPair(questions, "speaker", "角色不明确不等于学生没有回答", "limitation", "dimension:thinking",
		"转写没有标明说话人，能确认提问与简短接话，不能确认每句由谁说，也不能据此判断学生是否理解。",
		"教师提问后没有任何学生回答，说明学生没有理解该问题。",
		"未分角色且存在简短接话；不得把无法确认参与者改成没有回答，更不能据此判定学习效果。")
	physics := calibrationScene([]string{"Why does the object keep moving? With no net force its velocity does not change. That is why it keeps moving."}, []string{"reasoning"}, []string{"以合力为零时速度不变解释物体继续运动的原因"})
	// Freeze the historical template as test data; do not adapt the expected
	// contrast when the active advice generation changes.
	advice := CoachingDraft{Suggestion: "若后续尚未检查回答理由，先确认是否已经收集学生回答。如果已有回答，可沿用其中一条追问“为什么这样想？本段哪句话或哪个例子能支持？”如果尚未收集，先请学生回应原问题，再追问理由。选一两份回答比较依据；说不清时回到原例补问，不必重新列举已答出的内容，也不额外要求尚未铺垫的专业知识。", Verification: "看回答能否从列举或结论推进到说明理由，并联系本段原话或例子；区分尚无回答、已有回答但理由不足、仍有分歧，再决定是否补讲。不据一两份回答认定全班掌握。"}
	physics.Report.Observations = []Observation{{Title: "用条件连接现象与解释", Code: "thinking", Type: "observation", Text: "教师以合力为零时速度不变解释继续运动。", Analysis: "该推理说明解释所用条件，不能据此判断学生已理解。", Suggestion: advice.Suggestion, Verification: advice.Verification, IDs: []string{"source-1"}, EventIDs: []string{"event-1"}}}
	addPair(physics, "future-check", "未来让学生解释不等于重复补讲，原文已讲过的内容不能说教师没讲", "suggestion", "observation:1",
		advice.Suggestion,
		"教师完全没有解释物体为什么继续运动，下次应先补讲合力为零时速度不变，再提出这个问题。",
		"让学生解释已讲过的原问题是合理的未来检查；声称教师未解释并要求补讲，与原文明示的解释矛盾。")
	later := calibrationScene([]string{"Today we will ask why a model is useful. We will return to that question after the example.", "The model gives the direction of the net force. Since acceleration has the same direction as the net force, we can predict the direction of acceleration before calculating its magnitude. This is why the model is useful."}, []string{"goal", "reasoning"}, []string{"提出模型用途的问题，并预告例子后回扣", "从合力方向推得加速度方向，以这段推理解释模型在计算前作预测的用途"})
	addPair(later, "later-context", "复核须读完后文，不把开头尚未解释当整段缺失", "summary", "summary",
		"教师先提出模型用途的问题，后文用计算前预测方向说明其用途；这里只描述讲解顺序。",
		"整段只提出模型有什么用，却始终没有解释模型的用途。",
		"第二块已明确解释用途；正确概括不提醒，否定后文解释应提醒。")
	biology := calibrationScene([]string{"Cooking an egg is an example of irreversible denaturation. This example does not mean that every instance of denaturation is irreversible."}, []string{"example"}, []string{"用煮蛋说明不可逆变性的个例，并明确不能推广到所有变性"})
	biology.Report.Observations = []Observation{{Title: "区分个例与推广范围", Code: "content", Type: "observation", Text: "教师以煮蛋为例，并提醒不能推广到所有变性。", Analysis: "该例有明确范围限制。", IDs: []string{"source-1"}, EventIDs: []string{"event-1"}}}
	addPair(biology, "example-scope", "个例的不可逆不能推广到所有情形，本例只检验文本逻辑不认证专业事实", "analysis", "observation:1",
		"这里仅以煮蛋说明一个不可逆的例子，并明确不推广到所有变性；专业内容仍需结合教材核对。",
		"这个例子证明所有蛋白质变性都不可逆，学生可以把它作为普遍规律。",
		"原文明示不得全称推广；限定个例合理，全称判断既违背原文又无依据。")
	union := calibrationScene([]string{"A matrix is a rectangular array of numbers.", "A vector here has one column. We can represent it as a column matrix."}, []string{"definition", "term_explanation"}, []string{"解释矩阵是矩形数表", "解释本段向量的单列表示，并联系矩阵"})
	addPair(union, "joint-sources", "多段共同支持小结，不要求每段都解释所有术语", "summary", "dimension:expression",
		"先说明矩阵的矩形排列，再说明向量的单列表示及其与矩阵的联系。",
		"教师还详细解释了行列式和特征值的含义及计算方法。",
		"两块联合支持有限概括；行列式与特征值未被讲解。不能以单块缺少另一术语否定合理小结。")
	addPair(physics, "teacher-reasoning", "教师展示推理支持思维线索，无需纯思维事件或学生已推理", "coverage_status", "dimension:thinking",
		"observed", "insufficient",
		"原文明确展示从合力条件到速度变化的解释，reasoning可同时支持思维与内容；缺少学生推理不否定教师展示。")
	addPair(physics, "future-verification", "未来检查的分支不是对既有学生表现的断言", "verification", "observation:1",
		"实施后看学生能否用本段条件说明理由；若尚无回答，先收集回答，再判断是否需要补问。不能据此认定全班掌握。",
		"学生已经能独立说明本段所有推理，证明全班均已掌握。",
		"合理的未来观察条件不报错；没有学生表现依据却声称全班已掌握应定位verification。")
	definition := calibrationScene([]string{"Denaturation disrupts the weak interactions maintaining a protein's shape. Some interactions may reform; excessive heat can instead lead to aggregation."}, []string{"definition"}, []string{"说明变性涉及弱相互作用，并区分重新形成与高热聚集"})
	definition.Report.Observations = []Observation{{Title: "说明结构变化的两种走向", Code: "content", Type: "observation", Text: "教师说明弱相互作用受影响，并区分重新形成和高热聚集。", Analysis: "只概括本段已给出的联系。", IDs: []string{"source-1"}, EventIDs: []string{"event-1"}}}
	addPair(definition, "definition-standard", "不能自行引入完整定义要求并判教师缺陷", "analysis", "observation:1",
		"这里说明结构变化及不同走向；是否还需补充其他知识，应结合本课目标与教材核实。",
		"教师没有补充结构变化导致功能丧失，定义不完整，因此构成明确的教学缺陷。",
		"输入没有规定完整定义的要件；专业待核实项不能直接认证为教学缺陷，本例不认证专业知识本身。")
	addPair(union, "wording-not-status", "小结范围过强应质疑小结，不连带否定已有维度依据", "summary", "dimension:content",
		"本段解释矩阵，并联系向量的单列表示。",
		"本段已经完整讲解矩阵与向量的全部性质和所有应用。",
		"内容维度有定义与概念联系依据；问题只在完整、全部、所有等过强范围，不能改判coverage_status。")
	lecture := calibrationScene([]string{"Now there are two important points I want to make about Newton's second law."}, []string{"transition"}, []string{"教师安排接下来讲解的两个要点"})
	addPair(lecture, "lecture-not-task", "教师组织自己的讲解不等于要求学生完成活动", "coverage_status", "dimension:management",
		"insufficient", "observed",
		"原话只说明教师要讲什么，没有要求学生做事；应为活动组织依据不足，不能由听讲隐含推导出活动指令。")
	absence := calibrationScene([]string{"Which condition matters here? Yes, constant mass. Can you explain why? With constant mass, differentiating momentum gives mass times acceleration."}, []string{"reasoning"}, []string{"围绕恒定质量条件提问、接话并展示化简关系，转写未标说话人"})
	absence.Report.Observations = []Observation{{Title: "联系条件与化简", Code: "thinking", Type: "highlight", Text: "原文先提恒定质量条件，再说明动量导数的化简。", Analysis: "把条件与化简联系起来，便于比较两种写法；学生理解仍不能确认。", IDs: []string{"source-1"}, EventIDs: []string{"event-1"}}}
	addPair(absence, "absence-summary", "整体小结不能凭角色不明就推断教师没有检查理解", "summary", "summary",
		"片段围绕恒定质量条件展开提问与解释，并展示动量导数的化简；转写未标说话人，不能确认学生理解。",
		"片段围绕恒定质量条件展开提问与解释，但教师没有邀请学生解释，也从未检查学生理解。",
		"提问及接话确实出现；角色未标注，不能据此断定未邀请或未检查，本例只检验报告范围，不评价教师效果。")
	addPair(absence, "absence-title", "标题也不能把输入不确定性写成问题定论", "title", "observation:1",
		"联系条件与化简",
		"教师未邀请学生解释",
		"同一份未标角色的原文含解释邀请；标题不得比事实作出更强的缺失判断。")
	addPair(absence, "absence-fact", "课堂事实不能混入未证实的缺失与角色", "observation_text", "observation:1",
		"原文先提恒定质量条件，再说明动量导数的化简。",
		"教师独自完成全部推理，未给学生任何解释机会。",
		"原文没有说话人对齐，且有解释邀请；不能把推测写进事实字段。")
	addPair(absence, "absence-highlight", "亮点分析不能附加同一无依据负面尾句", "analysis", "observation:1",
		"把条件与化简联系起来，便于比较两种写法；学生理解仍不能确认。",
		"把条件与化简联系起来，便于比较两种写法；但仅教师展示，未转化为学生思考任务。",
		"两例保持highlight类型不变；原文含解释邀请，不能借亮点标签断言未安排学生思考。")
	relation := calibrationScene([]string{"Start with force as the time derivative of momentum. For a point mass with constant mass, differentiating momentum, mass times velocity, gives mass times acceleration. This recovers F equals ma under that condition."}, []string{"reasoning"}, []string{"从动量导数写法出发，在恒定质量条件下化为F=ma"})
	addPair(relation, "relation-direction", "概括不得颠倒原文的推导方向", "summary", "dimension:thinking",
		"从动量导数写法出发，在恒定质量条件下化为F=ma。",
		"从F=ma出发，推导出动量导数写法。",
		"只核对本段明确的推导顺序；不声称反向数学推导不可能，不认证教材公式本身。")
	addPair(relation, "relation-condition", "条件下的化简不能写成无条件等价", "summary", "dimension:content",
		"说明恒定质量条件下，动量导数写法可化为F=ma。",
		"说明无论质量如何变化，动量导数写法都与F=ma完全等价。",
		"原文明示恒定质量条件；本例检验报告是否保留条件，不补写变质量系统的专业标准答案。")
	// These short scenes use the wording of the submitted biology sample.
	// Expected outcomes are authored contrasts, not a live model evaluation.
	cooking := calibrationScene([]string{
		"So with heat, it's very clear. You're going to break those weak bonds and then they can either reform or if you go to too high heat, the unfolded protein starts to form aggregates. And anyone who",
		"Has ever scrambled an egg, knows that that is an irreversible process. You don't get to cram the egg back into the shell. It's not the same anymore because what you are doing when you are scrambling eggs is denaturing proteins.",
	}, []string{"reasoning", "example"}, []string{"说明高温下展开的蛋白质可能聚集", "用炒鸡蛋承接加热后难以恢复的情形"})
	cooking.Report.Observations = []Observation{{Title: "联系加热、聚集与炒鸡蛋", Code: "content", Type: "observation",
		Text: "教师先讲高温下展开的蛋白质可能聚集，随后举炒鸡蛋的例子。", Analysis: "前后两段将展开、聚集和炒鸡蛋的情形连接起来。",
		IDs: []string{"source-1", "source-2"}, EventIDs: []string{"event-1", "event-2"}}}
	addPair(cooking, "biology-cooking-meaning", "跨语言动作需合读加热语境，不能将炒鸡蛋译为打散生鸡蛋", "observation_text", "observation:1",
		"教师先讲高温下展开的蛋白质可能聚集，随后举炒鸡蛋的例子。",
		"教师以仅把生鸡蛋打散而不加热为例，说明蛋白质变性且不可恢复。",
		"原文说heat及scrambling eggs，应保留加热炒鸡蛋的语境；测试只对照原文，不补外部机制。")
	addPair(cooking, "biology-cross-block", "后续炒鸡蛋承接上块解释，不能断言没有例子回扣", "analysis", "observation:1",
		"前后两段将展开、聚集和炒鸡蛋的情形连接起来。",
		"教师未在后续用炒鸡蛋等任何例子回扣展开与聚集的关系。",
		"例子与前句跨块连续，合读可支持有限概括；不要求给温度阈值或外部机制才能说明这层联系。")
	preview := calibrationScene([]string{"I'm going to talk about a different protein variant that is the cause of sickle cell anemia. But let me very briefly recap what we did last time."}, []string{"goal"}, []string{"预告蛋白质变体主题，并说明先复习"})
	addPair(preview, "biology-preview", "预告病例不能写成已解释致病机制", "summary", "summary",
		"教师预告接下来将谈到与镰状细胞贫血有关的蛋白质变体，并说明先复习前课内容。",
		"教师已经详细解释镰状细胞贫血的蛋白质变异及其分子致病机制。",
		"只有计划声明，不把未来主题扩写为已经展开的分子机制。")
	question := calibrationScene([]string{"So can anyone think of what kinds of things would cause protein denaturation? Yes."}, []string{"question"}, []string{"提问请听众思考可能引起变性的因素，未标明说话人"})
	addPair(question, "biology-question-intent", "允许描述提问要求，不把邀请思考当成实际作答效果", "summary", "dimension:thinking",
		"提出问题，请学生联想可能引起蛋白质变性的因素。",
		"学生已经独立列出全部变性因素，表明他们掌握了相关概念。",
		"can anyone think明确表达思考邀请；无需已有学生回答才能描述邀请，但实际作答及掌握需要另外的证据。")
	return cases
}

func calibrationVerdict(c auditCalibrationCase, audit *ReportAudit) (bool, string) {
	if audit == nil || (audit.Status != "no_flags" && audit.Status != "needs_review") || len(audit.RejectedFindings) != 0 {
		return false, "复核未完整完成或存在不能定位的提示，不能计为判断正确"
	}
	if !c.WantFlag {
		return audit.Status == "no_flags" && len(audit.Findings) == 0, "合理文本不应被提示有问题"
	}
	matched := false
	for _, f := range audit.Findings {
		if f.Target != c.Target || len(f.Fields) != 1 || f.Fields[0] != c.Field {
			return false, "出现预期问题字段以外的提示，需核对是否误报"
		}
		matched = true
	}
	return matched && audit.Status == "needs_review", "应准确定位到预期的问题字段；不以随意报一个疑点算通过"
}

func TestAuditCalibrationFixturesAndVerdict(t *testing.T) {
	cases := auditCalibrationCases()
	seen := map[string]bool{}
	for _, c := range cases {
		if seen[c.ID] || c.Purpose == "" || c.Rationale == "" {
			t.Fatal("invalid calibration identity", c.ID)
		}
		seen[c.ID] = true
		if err := validateReport(c.Report, c.Segments); err != nil {
			t.Fatal(c.ID, err)
		}
		clean := &ReportAudit{Status: "no_flags", Findings: []AuditFinding{}}
		ok, _ := calibrationVerdict(c, clean)
		if ok == c.WantFlag {
			t.Fatal("all-clean reviewer would incorrectly pass", c.ID)
		}
		if c.WantFlag {
			f := anchoredFinding(c.Report, c.Segments, c.Target, []string{c.Field})
			a := &ReportAudit{Status: "needs_review", Findings: []AuditFinding{f}}
			if ok, _ := calibrationVerdict(c, a); !ok {
				t.Fatal("correct location not recognized", c.ID)
			}
			f.Target = "not-the-expected-target"
			a.Findings = []AuditFinding{f}
			if ok, _ := calibrationVerdict(c, a); ok {
				t.Fatal("unrelated flag accepted", c.ID)
			}
		}
	}
	if len(cases) != 42 {
		t.Fatal("expected twenty-one paired scenarios")
	}
	for i := 0; i < len(cases); i += 2 {
		if !reflect.DeepEqual(cases[i].Segments, cases[i+1].Segments) || !reflect.DeepEqual(cases[i].Events, cases[i+1].Events) || cases[i].WantFlag || !cases[i+1].WantFlag {
			t.Fatal("paired sources differ or lack both control directions")
		}
	}
}

// Explicit opt-in: forty-two text-only requests, one per case, no retries, no ASR,
// no job store access. Normal regression scripts forcibly disable this test.
func TestLiveAuditCalibration(t *testing.T) {
	if os.Getenv("BAILIAN_LIVE_AUDIT_TEST") != "1" {
		t.Skip("live audit calibration disabled")
	}
	if err := godotenv.Load(); err != nil {
		t.Fatal("cannot load demo .env")
	}
	key, base := os.Getenv("DASHSCOPE_API_KEY"), os.Getenv("BAILIAN_BASE_URL")
	if key == "" || base == "" {
		t.Fatal("model configuration missing")
	}
	t.Setenv("BAILIAN_REPORT_AUDIT", "1")
	cases := auditCalibrationCases()
	// An explicit pair selector supports a bounded retest after fixture repairs;
	// unknown selectors fail before any model request. No case is silently skipped.
	if prefix := os.Getenv("BAILIAN_AUDIT_CASE_PREFIX"); prefix != "" {
		selected := []auditCalibrationCase{}
		for _, c := range cases {
			if strings.HasPrefix(c.ID, prefix+"-") {
				selected = append(selected, c)
			}
		}
		if len(selected) != 2 {
			t.Fatal("case prefix must identify exactly one paired scenario")
		}
		cases = selected
	}
	runDir := filepath.Join("evaluation", "2026-10-17-audit-calibration", "runs", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(filepath.Dir(runDir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runDir, 0755); err != nil {
		t.Fatal("cannot reserve a new output directory", err)
	}
	t.Log("isolated results:", runDir)
	type result struct {
		Case    auditCalibrationCase `json:"case"`
		Audit   *ReportAudit         `json:"audit"`
		Passed  bool                 `json:"matched_expectation"`
		Message string               `json:"verdict_rule"`
	}
	results := []result{}
	prompt, _ := assets.ReadFile("audit_prompt.txt")
	rubric, _ := assets.ReadFile("rubric.md")
	writeArtifact := func(name string, value any) {
		t.Helper()
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(filepath.Join(runDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := f.Write(append(raw, '\n'))
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal("cannot persist calibration artifact", writeErr, closeErr)
		}
	}
	writeArtifact("manifest.json", map[string]any{"created_at": time.Now().UTC(), "model": env("BAILIAN_TEXT_MODEL", "qwen-plus"), "audit_version": reportAuditVersion, "semantic_protocol": semanticProtocol, "review_criteria": reviewRules(), "narrative_protocol": narrativeProtocol, "narrative_rules": narrativeRules(), "narrative_field_roles": narrativeFieldRoles(), "input_limits": narrativeInputLimits(), "rubric_sha256": fmt.Sprintf("%x", sha256.Sum256(rubric)), "audit_prompt_sha256": fmt.Sprintf("%x", sha256.Sum256(prompt)), "planned_text_requests": len(cases), "case_prefix": os.Getenv("BAILIAN_AUDIT_CASE_PREFIX"), "asr_requests": 0, "scope": "hand-designed regression examples; not teacher gold data or a classroom accuracy estimate"})
	for _, c := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		audit := auditReport(ctx, base, key, c.Report, c.Segments, c.Events)
		cancel()
		passed, message := calibrationVerdict(c, audit)
		r := result{c, audit, passed, message}
		results = append(results, r)
		writeArtifact(c.ID+".json", r)
		t.Logf("%s: status=%s flags=%d matched=%v request=%s", c.ID, audit.Status, len(audit.Findings), passed, audit.Request.ID)
	}
	writeArtifact("results.json", results)
	for _, r := range results {
		if !r.Passed {
			t.Errorf("%s did not match expectation: %s", r.Case.ID, r.Message)
		}
	}
}
