package governance

import (
	"bytes"
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/signintech/gopdf"
)

// Noto Sans SC, SIL OFL 1.1. Embedded so Windows/Linux and PDF readers do not
// depend on installed Chinese fonts or substitute a different CID font.
//
//go:embed fonts/NotoSansSC-Regular.ttf
var reportFont []byte

type pdfRow struct {
	Cells  []string
	Header bool
}
type pdfBlock struct {
	Heading, Text string
	Rows          []pdfRow
	Widths        []float64
}

var dimensionNames = map[string]string{"content": "教学内容", "pace": "教学节奏", "thinking": "思维启发", "expression": "教学表达", "management": "课堂管理", "technology": "教学技术"}

func str(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
func clock(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%02d:%02d.%03d", ms/60000, ms/1000%60, ms%1000)
}
func localTime(value string) string {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		for _, layout := range []string{"2006-01-02 15:04:05-07", "2006-01-02 15:04:05.999999-07"} {
			if t, err = time.Parse(layout, value); err == nil {
				break
			}
		}
	}
	if err != nil {
		return value
	}
	return t.In(time.FixedZone("北京时间", 8*3600)).Format("2006-01-02 15:04")
}
func stringsOf(value any) []string {
	var out []string
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if text := str(item); text != "" {
				out = append(out, text)
			}
		}
	case []string:
		out = append(out, v...)
	}
	return out
}
func citeNumbers(value any, refs map[string]int) string {
	var out []string
	for _, id := range stringsOf(value) {
		if n, ok := refs[id]; ok {
			out = append(out, fmt.Sprintf("[%d]", n))
		}
	}
	return strings.Join(out, "")
}

func reportBlocks(s exportSnapshot) []pdfBlock {
	d := s.Details
	if d == nil {
		d = &reportDetails{Academic: map[string]string{}}
	}
	refs := map[string]int{}
	used := map[string]bool{}
	for _, id := range stringsOf(s.Report["summary_evidence_ids"]) {
		used[id] = true
	}
	for _, key := range []string{"dimensions", "observations"} {
		for _, raw := range anySlice(s.Report[key]) {
			item, _ := raw.(map[string]any)
			for _, id := range stringsOf(item[map[string]string{"dimensions": "summary_evidence_ids", "observations": "evidence_ids"}[key]]) {
				used[id] = true
			}
		}
	}
	for _, e := range d.Evidence {
		if used[e.ID] {
			refs[e.ID] = len(refs) + 1
		}
	}
	info := []pdfRow{{Cells: []string{"课程", d.Academic["course_name"], "任课教师", d.Academic["teacher_name"]}}, {Cells: []string{"班级", d.Academic["class_name"], "学期", d.Academic["term_name"]}}, {Cells: []string{"计划时间", localTime(d.Academic["planned_start_at"]) + "\n" + localTime(d.Academic["planned_end_at"]), "教室", d.Academic["classroom_name"]}}}
	blocks := []pdfBlock{{Heading: "一、课堂概况", Rows: info, Widths: []float64{65, 190, 65, 187}}, {Text: "分析录像：" + orText(d.Filename, "历史录像（未记录文件名）") + "    录像时长：" + clock(d.DurationMS)}, {Heading: "二、课堂综合分析", Text: orText(str(s.Report["summary"]), "本版暂无综合结论。") + citeNumbers(s.Report["summary_evidence_ids"], refs)}}
	for _, raw := range anySlice(s.Report["dimensions"]) {
		item, _ := raw.(map[string]any)
		title := dimensionNames[str(item["dimension_code"])]
		if title == "" {
			continue
		}
		text := str(item["summary"])
		if str(item["coverage_status"]) != "observed" {
			text = "证据不足：" + orText(str(item["limitation"]), "本段录像没有可核对的依据。")
			if str(item["coverage_status"]) == "not_applicable" {
				text = "本次不适用：" + str(item["limitation"])
			}
		}
		blocks = append(blocks, pdfBlock{Heading: title, Text: text + citeNumbers(item["summary_evidence_ids"], refs)})
	}
	for _, raw := range anySlice(s.Report["observations"]) {
		item, _ := raw.(map[string]any)
		if str(item["review_status"]) == "rejected" {
			continue
		}
		kind := map[string]string{"issue": "需改进", "highlight": "可保留做法", "suggestion": "改进建议"}[str(item["observation_type"])]
		if kind == "" {
			kind = "课堂观察"
		}
		blocks = append(blocks, pdfBlock{Heading: kind, Text: str(item["observation_text"]) + citeNumbers(item["evidence_ids"], refs)})
		if suggestion := str(item["suggestion"]); suggestion != "" {
			blocks = append(blocks, pdfBlock{Text: "建议：" + suggestion})
		}
	}
	blocks = append(blocks, pdfBlock{Heading: "三、学生行为汇总"})
	if d.Visual == nil {
		blocks = append(blocks, pdfBlock{Text: orText(d.VisualNote, "本版没有绑定可用视觉记录，不能生成逐人行为统计。")})
	} else {
		v := d.Visual
		visibleSum, visibleN := 0, 0
		handPeak, headPeak := -1, -1
		for _, c := range v.Visual.Counts {
			if c.VisibleCount != nil {
				visibleSum += *c.VisibleCount
				visibleN++
			}
			if c.HandRaiseCount != nil {
				handPeak = max(handPeak, *c.HandRaiseCount)
			}
			if c.HeadDownCount != nil {
				headPeak = max(headPeak, *c.HeadDownCount)
			}
		}
		avg := "看不清"
		if visibleN > 0 {
			avg = fmt.Sprintf("%.1f", float64(visibleSum)/float64(visibleN))
		}
		peak := func(n int) string {
			if n < 0 {
				return "看不清"
			}
			return fmt.Sprint(n)
		}
		scope := ""
		for _, span := range v.Coverage {
			if scope != "" {
				scope += "；"
			}
			scope += clock(span.StartMS) + " - " + clock(span.EndMS)
		}
		blocks = append(blocks, pdfBlock{Text: fmt.Sprintf("分析范围：%s；每 %.3g 秒采样，共 %d 张画面。平均可见人数 %s；同一画面举手最多 %s 人、低头最多 %s 人。", orText(scope, "未记录"), float64(v.Sampling.IntervalMS)/1000, len(v.Visual.Counts), avg, peak(handPeak), peak(headPeak))}, pdfBlock{Text: "编号只用于本段录像的匿名轨迹，不能当作姓名或出勤人数。下表统计采样画面，不是动作次数或持续时长；低头可能是阅读或书写。举手、低头列表示发现动作的画面数/能够判断的画面数，看不清的画面另列。"})
		rows := []pdfRow{{Header: true, Cells: []string{"人物编号", "出现采样范围", "画面数", "举手\n发现/可判断", "低头\n发现/可判断", "看不清\n举手/低头"}}}
		people := append([]exportPerson(nil), v.Visual.Persons...)
		sort.SliceStable(people, func(i, j int) bool { return people[i].TrackID < people[j].TrackID })
		var positions []pdfBlock
		for _, person := range people {
			obs := append([]exportObservation(nil), person.Observations...)
			sort.SliceStable(obs, func(i, j int) bool { return obs[i].TimestampMS < obs[j].TimestampMS })
			first, last := "—", "—"
			if len(obs) > 0 {
				first = clock(obs[0].TimestampMS)
				last = clock(obs[len(obs)-1].TimestampMS)
			}
			handYes, handKnown := behaviorCounts(obs, "hand_raise")
			headYes, headKnown := behaviorCounts(obs, "head_down")
			rows = append(rows, pdfRow{Cells: []string{person.TrackID, first + "\n" + last, fmt.Sprint(len(obs)), behaviorCell(handYes, handKnown), behaviorCell(headYes, headKnown), fmt.Sprintf("%d / %d", len(obs)-handKnown, len(obs)-headKnown)}})
			text := ""
			for _, k := range []string{"hand_raise", "head_down"} {
				ranges := positiveRanges(obs, k, v.Sampling.IntervalMS)
				if len(ranges) > 0 {
					if text != "" {
						text += "\n"
					}
					text += map[string]string{"hand_raise": "举手", "head_down": "低头"}[k] + "：" + strings.Join(ranges, "；")
				}
			}
			if text != "" {
				positions = append(positions, pdfBlock{Heading: person.TrackID, Text: text})
			}
		}
		blocks = append(blocks, pdfBlock{Rows: rows, Widths: []float64{58, 105, 45, 95, 95, 109}})
		if len(people) == 0 {
			blocks = append(blocks, pdfBlock{Text: "本段录像未产生可记录的人物轨迹。"})
		}
		if len(positions) > 0 {
			blocks = append(blocks, pdfBlock{Heading: "发现动作的位置", Text: "以下为实际发现动作的采样点或相邻采样点范围，仅供定位核对，不代表整个时间段持续有动作。"})
			blocks = append(blocks, positions...)
		}
	}
	blocks = append(blocks, pdfBlock{Heading: "四、语音与证据依据", Text: fmt.Sprintf("本报告对应 %d 条语音文字依据；下面只列正文实际引用的依据。时间以本段录像起点计算。", d.SpeechSegments)})
	for _, e := range d.Evidence {
		n, ok := refs[e.ID]
		if !ok {
			continue
		}
		kind := map[string]string{"transcript": "语音", "behavior": "视觉观察", "manual": "教务登记", "frame": "画面"}[e.Kind]
		if kind == "" {
			kind = "课堂依据"
		}
		state := ""
		if e.Availability != "available" {
			state = "（原始内容已不可读取）"
		}
		description := e.Description
		runes := []rune(description)
		if len(runes) > 350 {
			description = string(runes[:350]) + "…（节选）"
		}
		blocks = append(blocks, pdfBlock{Heading: fmt.Sprintf("[%d] %s · %s - %s%s", n, kind, clock(e.StartMS), clock(e.EndMS), state), Text: description})
	}
	blocks = append(blocks, pdfBlock{Heading: "五、来源与报告说明", Text: "来源：" + orText(str(s.Source["title"]), "未记录") + "；提供方：" + orText(str(s.Source["attribution"]), "未记录")})
	if license := str(s.Source["license_name"]); license != "" {
		blocks = append(blocks, pdfBlock{Text: "许可：" + license})
	}
	blocks = append(blocks, pdfBlock{Text: "本报告仅分析实际提供的录像与文字，不以动作数量评价学习能力或学习态度；教务与课表信息是登记背景。自动视觉结果需要核对原画面，未采样和看不清的部分不作判断。"})
	for _, limitation := range stringsOf(s.Report["limitations"]) {
		if limitation != "" {
			blocks = append(blocks, pdfBlock{Text: "范围限制：" + limitation})
		}
	}
	return blocks
}
func orText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func behaviorCell(positive, known int) string {
	if known == 0 {
		return "无法判断"
	}
	return fmt.Sprintf("%d / %d", positive, known)
}
func behaviorCounts(obs []exportObservation, key string) (positive, known int) {
	for _, o := range obs {
		if value := o.Behaviors[key]; value != nil {
			known++
			if *value {
				positive++
			}
		}
	}
	return
}
func positiveRanges(obs []exportObservation, key string, interval int64) []string {
	var out []string
	var start, last int64
	count := 0
	flush := func() {
		if count == 0 {
			return
		}
		text := clock(start)
		if count > 1 {
			text += " - " + clock(last) + fmt.Sprintf("（%d个采样点）", count)
		}
		out = append(out, text)
		count = 0
	}
	for _, o := range obs {
		value := o.Behaviors[key]
		if value == nil || !*value {
			flush()
			continue
		}
		if count > 0 && (interval <= 0 || o.TimestampMS-last > interval || o.TimestampMS <= last) {
			flush()
		}
		if count == 0 {
			start = o.TimestampMS
		}
		last = o.TimestampMS
		count++
	}
	flush()
	return out
}

// pdfLines is a plain text view used for content/integration assertions. It is
// deliberately separate from the JSON provenance dump.
func pdfLines(s exportSnapshot) []string {
	var out []string
	for _, b := range reportBlocks(s) {
		if b.Heading != "" {
			out = append(out, b.Heading)
		}
		if b.Text != "" {
			out = append(out, b.Text)
		}
		for _, r := range b.Rows {
			out = append(out, strings.Join(r.Cells, " | "))
		}
	}
	return out
}

type reportPainter struct {
	pdf     gopdf.GoPdf
	y       float64
	err     error
	missing bool
}

func (p *reportPainter) font(size float64) {
	if p.err == nil {
		p.err = p.pdf.SetFont("noto", "", size)
	}
}
func (p *reportPainter) text(x, y, size float64, text string) {
	p.font(size)
	p.pdf.SetXY(x, y)
	if p.err == nil {
		p.err = p.pdf.Cell(nil, text)
	}
}
func (p *reportPainter) page() {
	p.pdf.AddPage()
	p.pdf.SetTextColor(100, 115, 120)
	p.text(44, 24, 9, "观课 / 课堂分析报告")
	p.pdf.SetStrokeColor(219, 229, 228)
	p.pdf.Line(44, 44, 551, 44)
	p.y = 60
}
func (p *reportPainter) ensure(height float64) {
	if p.y+height > 780 {
		p.page()
	}
}
func (p *reportPainter) lines(text string, width, size float64) []string {
	p.font(size)
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		lines, err := p.pdf.SplitText(para, width)
		if err != nil {
			p.err = err
			return nil
		}
		// Avoid a punctuation-only line (or an opening bracket stranded at the
		// end of a line) while preserving every character and measured width.
		for i := 1; i < len(lines); i++ {
			previous, current := []rune(lines[i-1]), []rune(lines[i])
			if len(previous) > 1 && len(current) > 0 &&
				(strings.ContainsRune("，。！？；：、）》】”’", current[0]) || strings.ContainsRune("（《【“‘", previous[len(previous)-1])) {
				lines[i-1] = string(previous[:len(previous)-1])
				lines[i] = string(previous[len(previous)-1:]) + lines[i]
			}
			for {
				measured, err := p.pdf.MeasureTextWidth(lines[i])
				if err != nil {
					p.err = err
					return nil
				}
				runes := []rune(lines[i])
				if measured <= width || len(runes) < 2 {
					break
				}
				carry := string(runes[len(runes)-1:])
				lines[i] = string(runes[:len(runes)-1])
				if i+1 == len(lines) {
					lines = append(lines, carry)
				} else {
					lines[i+1] = carry + lines[i+1]
				}
			}
		}
		out = append(out, lines...)
	}
	return out
}
func (p *reportPainter) paragraph(text string) {
	for _, line := range p.lines(text, 507, 10.2) {
		p.ensure(17)
		p.pdf.SetTextColor(40, 55, 62)
		p.text(44, p.y, 10.2, line)
		p.y += 17
	}
	p.y += 7
}
func (p *reportPainter) heading(text string) {
	p.ensure(65)
	p.y += 5
	p.pdf.SetFillColor(234, 245, 241)
	p.pdf.RectFromUpperLeftWithStyle(44, p.y, 507, 28, "F")
	p.pdf.SetTextColor(14, 107, 96)
	p.text(53, p.y+5, 12, text)
	p.y += 38
}
func (p *reportPainter) table(block pdfBlock) {
	var header *pdfRow
	for _, r := range block.Rows {
		if r.Header {
			copy := r
			header = &copy
			break
		}
	}
	draw := func(row pdfRow) {
		var cells [][]string
		height := 26.0
		for i, text := range row.Cells {
			lines := p.lines(orText(text, "—"), block.Widths[i]-12, 9)
			cells = append(cells, lines)
			height = max(height, float64(len(lines))*14+12)
		}
		if row.Header {
			p.pdf.SetFillColor(226, 239, 235)
		} else {
			p.pdf.SetFillColor(249, 251, 251)
		}
		p.pdf.RectFromUpperLeftWithStyle(44, p.y, 507, height, "F")
		x := 44.0
		for i, lines := range cells {
			p.pdf.SetTextColor(35, 57, 62)
			for n, line := range lines {
				p.text(x+6, p.y+6+float64(n)*14, 9, line)
			}
			x += block.Widths[i]
		}
		p.pdf.SetStrokeColor(221, 230, 229)
		p.pdf.Line(44, p.y+height, 551, p.y+height)
		p.y += height
	}
	for _, row := range block.Rows {
		height := 26.0
		for i, text := range row.Cells {
			height = max(height, float64(len(p.lines(orText(text, "—"), block.Widths[i]-12, 9)))*14+12)
		}
		if p.y+height > 780 {
			p.page()
			if header != nil && !row.Header {
				draw(*header)
			}
		}
		draw(row)
	}
	p.y += 15
}
func renderPDF(s exportSnapshot) ([]byte, error) {
	p := &reportPainter{}
	p.pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := p.pdf.AddTTFFontDataWithOption("noto", reportFont, gopdf.TtfOption{OnGlyphNotFound: func(r rune) { p.missing = true }, OnGlyphNotFoundSubstitute: func(r rune) rune { return '□' }}); err != nil {
		return nil, err
	}
	p.page()
	p.pdf.SetTextColor(20, 52, 60)
	p.text(44, p.y, 24, "课堂分析报告")
	p.y += 42
	title := ""
	if s.Details != nil {
		title = s.Details.Academic["session_title"]
	}
	if title != "" {
		p.paragraph(title)
	}
	p.pdf.SetTextColor(100, 115, 120)
	p.text(44, p.y, 9, fmt.Sprintf("第 %v 版  ·  导出于 %s", s.Report["revision"], s.ExportedAt.In(time.FixedZone("北京时间", 8*3600)).Format("2006-01-02 15:04")))
	p.y += 30
	for _, block := range reportBlocks(s) {
		if block.Heading != "" {
			p.heading(block.Heading)
		}
		if block.Text != "" {
			p.paragraph(block.Text)
		}
		if len(block.Rows) > 0 {
			p.table(block)
		}
	}
	if p.missing {
		p.paragraph("个别特殊字符在本字体中不可用，已用方框标示；完整原文请查看系统或 JSON 导出。")
	}
	for n := 1; n <= p.pdf.GetNumberOfPages(); n++ {
		if err := p.pdf.SetPage(n); err != nil {
			return nil, err
		}
		p.pdf.SetTextColor(110, 120, 125)
		p.text(44, 805, 9, fmt.Sprintf("观课 · 第 %d / %d 页", n, p.pdf.GetNumberOfPages()))
	}
	if p.err != nil {
		return nil, p.err
	}
	var out bytes.Buffer
	if _, err := p.pdf.WriteTo(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
