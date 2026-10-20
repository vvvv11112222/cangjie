const {test}=require('node:test');
const assert=require('node:assert/strict');
const {dimensionNames,dimensionGuides,evidenceRanges,evidenceLabel,friendlyDiagnostic,briefText,readableReport,reviewTarget}=require('../web/analysis.js');
const segments=[{id:'e-0001',start_ms:0,end_ms:31000},{id:'e-0002',start_ms:31000,end_ms:61000}];
test('旧报告提示区分当前规则与历史规则，不改写报告、不要求重转写',()=>{
 const {reportUpdateNotice}=require('../web/analysis.js');
 const report={analysis_version:'six-dim-coaching-v4'},runtime={analysis_version:'six-dim-coaching-v6'};
 const before=JSON.stringify(report);
 assert.match(reportUpdateNotice(report,runtime),/较早程序.*现有转写/);
 assert.match(reportUpdateNotice(report,runtime),/旧报告和校对记录保留/);
 assert.equal(reportUpdateNotice(report,{analysis_version:report.analysis_version}),'');
 assert.equal(reportUpdateNotice(report,null),'');
 assert.match(reportUpdateNotice({},runtime),/较早程序/);
 assert.match(reportUpdateNotice({analysis_version:'six-dim-coaching-v7'},runtime),/确认已启动较新的程序/);
 assert.equal(JSON.stringify(report),before);
});
test('依据状态的复核原句显示中文，保留原始协议值与文本原话',()=>{
 for(const [status,label] of Object.entries({observed:'有相关文字证据',insufficient:'本片段暂不能判断',not_applicable:'本片段不适用'})){
  const data={report:{dimensions:[{dimension_code:'management',coverage_status:status}],quality_audit:{status:'needs_review',findings:[{target:'dimension:management',fields:['coverage_status'],reason:'请对照原文核对依据状态。',report_quotes:[{field:'coverage_status',text:status}],source_ids:[]}]}}};
  const before=JSON.stringify(data),md=readableReport(data);
  assert.ok(md.includes('报告原句（依据状态）：'+label));
  assert.doesNotMatch(md,/coverage_status|observed|insufficient|not_applicable/);
  assert.equal(JSON.stringify(data),before);
 }
 assert.equal(friendlyDiagnostic('coverage_status需核对'),'依据状态需核对');
 const md=readableReport({report:{summary:'The word observed is quoted literally.',dimensions:[]}});
 assert.match(md,/The word observed is quoted literally\./,'ordinary report text must not have status values replaced');
});
test('全文复核是辅助提示，导出保留原因、来源和原建议',()=>{
 const {auditMessage,adviceNeedsReview}=require('../web/analysis.js');
 const report={summary:'片段',dimensions:[{dimension_code:'content',coverage_status:'observed'}],observations:[{dimension_code:'content',suggestion:'保留原建议'}],quality_audit:{status:'needs_review',message:'有疑点需核对，不是人工验收。',findings:[{target:'observation:1',fields:['suggestion'],reason:'后文已经解释，不必重复补做。',source_ids:['e-0001']}]}};
 assert.equal(adviceNeedsReview(report,0),true);assert.equal(adviceNeedsReview(report,1),false);
 const before=JSON.stringify(report),md=readableReport({report,segments:[{...segments[0],text:'后文原话'}]});
 assert.match(md,/模型提醒也可能误报，不等于已确认的课堂问题/);
 assert.match(md,/后文已经解释/);assert.match(md,/原文 00:00–00:31/);assert.match(md,/后文原话/);assert.match(md,/待核对的修改方向：保留原建议/);assert.doesNotMatch(md,/observation:1|e-0001/);assert.equal(JSON.stringify(report),before);
 assert.ok(md.indexOf('## 内容组织')<md.indexOf('## 附录：报告检查说明'),'teacher content must precede system inspection details in exports');
 report.quality_audit={status:'unavailable',findings:[]};assert.equal(adviceNeedsReview(report,0),false);
 report.quality_audit={status:'partial_review',message:'一条复核提示无法准确定位',findings:[{target:'summary',reason:'需核对',report_quotes:[{field:'summary',text:'片段'}]}]};
 assert.equal(adviceNeedsReview(report,0),false);
 const partial=readableReport({report});assert.match(partial,/报告原句（课堂分析小结）：片段/);assert.doesNotMatch(partial,/partial_review|report_quotes/);
 assert.match(auditMessage({}),/尚未/);
 report.quality_audit={status:'no_flags',findings:[]};assert.equal(adviceNeedsReview(report,0),false);
});
test('发言来源疑点标记对应字段，保留完整原句、建议、原文和数据',()=>{
 const {adviceNeedsReview,observationNeedsReview,reportFieldDisplay,auditReportQuotes,auditOverview}=require('../web/analysis.js');
 const fact='教师提问后，学生提出热、pH、盐等全部因素。';
 const data={asr_status:'complete',segments:[{...segments[0],text:'What causes denaturation? Yes. Heat, pH and salt.'}],report:{summary:'课堂讨论蛋白质变性因素。',dimensions:[{dimension_code:'thinking',coverage_status:'observed'}],observations:[{dimension_code:'thinking',observation_type:'highlight',title:'提问激活学生已有经验',observation_text:fact,analysis:'以问题引出因素。',suggestion:'下次可请学生选择一个因素，独立解释其作用，再交流理由。',verification:'收集学生的独立解释。'}],quality_audit:{status:'needs_review',findings:[{target:'observation:1',fields:['observation_text'],criterion:'speaker_uncertainty',kind:'transcript_uncertainty',reason:'observation_text超出input_limits，Segment没有说话人标注。',report_quotes:[{field:'observation_text',text:'学生提出热'}],source_ids:['e-0001'],source_quotes:[{segment_id:'e-0001',text:'Yes.'}]}]}}};
 const before=JSON.stringify(data),report=data.report,finding=report.quality_audit.findings[0],md=readableReport(data);
 assert.equal(observationNeedsReview(report,0),true);
 assert.equal(adviceNeedsReview(report,0),false,'an uncertain speaker does not itself invalidate a future activity');
 assert.equal(auditReportQuotes(report,finding)[0].text,fact,'complete saved field must remain available beyond the audit excerpt');
 assert.equal(reportFieldDisplay(data,'observation:1','observation_text',fact),'【待确认】'+fact);
 assert.match(md,/课堂记录待确认/);assert.match(md,/报告原句（课堂事实）：教师提问后，学生提出热、pH、盐等全部因素。/);
 assert.match(md,/原发现标题（供对照）：提问激活学生已有经验/);
 assert.doesNotMatch(md,/### .*提问激活学生已有经验/,'unconfirmed learning effect must not remain the card headline');
 assert.match(md,/修改方向 · 下次课可尝试：下次可请学生/);
 assert.match(md,/What causes denaturation\? Yes\. Heat, pH and salt\./);
 assert.doesNotMatch(md,/observation_text|input_limits|Segment|transcript_uncertainty|e-0001/);
 assert.equal(JSON.stringify(data),before);
 report.quality_audit={status:'partial_review',findings:[]};assert.match(auditOverview(report).title,/部分完成/);
 report.quality_audit={status:'unavailable',findings:[]};assert.match(auditOverview(report).title,/未完成/);
 report.quality_audit={status:'disabled',findings:[]};assert.match(auditOverview(report).title,/未完成/);
 report.quality_audit={status:'not_run',findings:[]};assert.match(auditOverview(report).title,/未启用/);
 report.quality_audit={status:'no_flags',findings:[]};assert.match(auditOverview(report).message,/只是模型检查结果/);
 report.quality_audit={status:'needs_review',findings:[{target:'observation:1',fields:['verification']}]};
 assert.equal(observationNeedsReview(report,0),false);assert.equal(adviceNeedsReview(report,0),false);
 assert.equal(reportFieldDisplay(data,'observation:1','verification','收集学生的独立解释。'),'【待确认】收集学生的独立解释。');
});
test('待确认小结保留完整正文和标记，不用提示语替换或裁剪原句',()=>{
 const {reportFieldDisplay}=require('../web/analysis.js');
 const value='课堂讲解了蛋白质结构。转写未区分师生发言。';
 const data={report:{summary:value,quality_audit:{findings:[{target:'summary',fields:['summary'],report_quotes:[{field:'summary',text:'转写未区分师生发言。'}]}]}}};
 const before=JSON.stringify(data);
 assert.equal(reportFieldDisplay(data,'summary','summary',value),'【待确认】'+value);
 assert.equal(JSON.stringify(data),before);
 for(const text of ['未区分师生','不存在的句子。',value]){
  data.report.quality_audit.findings[0].report_quotes[0].text=text;
  assert.equal(reportFieldDisplay(data,'summary','summary',value),'【待确认】'+value);
 }
 data.report.quality_audit.findings[0].report_quotes[0].text='转写未区分师生发言。';
 data.report.quality_audit.findings.push({target:'summary',fields:['summary'],report_quotes:[]});
 assert.equal(reportFieldDisplay(data,'summary','summary',value),'【待确认】'+value);
 data.report.quality_audit.findings.pop();
 assert.equal(reportFieldDisplay(data,'summary','summary','转写未区分师生发言。转写未区分师生发言。'),'【待确认】转写未区分师生发言。转写未区分师生发言。');
});

test('讲解脉络按已保存依据时间排列，保留预告及不确定性，不改事件或维度归属',()=>{
 const {teachingFlow}=require('../web/analysis.js');
 const data={segments,events:{events:[
  {id:'preview',event_type:'transition',summary:'预告稍后介绍病例，尚未展开。',uncertainty:'这里只是计划。',quotes:[{segment_id:'e-0002'}]},
  {id:'missing',event_type:'example',summary:'无来源的事件',quotes:[{segment_id:'missing'}]},
  {id:'goal',event_type:'goal',summary:'复习蛋白质结构。',quotes:[{segment_id:'e-0001'}]},
  {id:'tool',event_type:'tool_mention',summary:'提及工具',quotes:[{segment_id:'e-0001'}]}
 ]},report:{dimensions:[{dimension_code:'pace',coverage_status:'insufficient'}]}};
 const before=JSON.stringify(data),flow=teachingFlow(data);
 assert.deepEqual(flow.map(item=>item.event.id),['goal','preview']);
 assert.equal(flow[1].event.summary,'预告稍后介绍病例，尚未展开。');
 assert.equal(flow[1].event.uncertainty,'这里只是计划。');
 assert.equal(JSON.stringify(data),before);
 const md=readableReport(data);assert.match(md,/教师的讲解思路/);assert.match(md,/预告稍后介绍病例，尚未展开/);assert.match(md,/00:31–01:01/);
 assert.deepEqual(teachingFlow({}),[]);
});
test('六维中文名称、复盘问题、用途和限制齐全',()=>{
 assert.equal(Object.keys(dimensionGuides).length,6);
 for(const [code,value] of Object.entries(dimensionGuides)){
  assert.match(dimensionNames[code],/^[\u4e00-\u9fff]+$/);
  for(const key of ['question','use','limit'])assert.ok(value[key].length>10);
 }
 assert.match(dimensionGuides.pace.limit,/不能.*语速/);
 assert.match(dimensionGuides.technology.limit,/不证明真实操作/);
});
test('正文隐藏协议字段并保持中文含义',()=>{
 assert.equal(friendlyDiagnostic('allowed_events 无 organization，dimension_codes 待核对'), '可引用的课堂依据 无 活动安排，分析维度 待核对');
 assert.equal(friendlyDiagnostic('analysis 与 suggestion 需核对，summary 仅作参考'),'原因说明 与 修改方向 需核对，课堂小结 仅作参考');
 assert.equal(briefText('甲乙丙丁',3),'甲乙丙…');
});
test('易读导出保留完整事实、原话、时间与输入限制，不改写数据',()=>{
 const data={input_revision_no:2,report_version:3,asr_status:'unknown',segments:[{id:'e-0001',start_ms:0,end_ms:31000,text:'<script>alert(1)</script>原文。'}],events:{events:[{id:'ev-001',event_type:'goal',quotes:[{segment_id:'e-0001',text:'原文。'}]}]},report:{summary:'片段总结',dimensions:[{dimension_code:'content',coverage_status:'observed',summary:'内容小结',limitation:'allowed_events 中只有 ev-001'}],observations:[{dimension_code:'content',observation_type:'highlight',title:'具体发现',observation_text:'完整事实'.repeat(100),analysis:'教学意义',suggestion:'具体动作',verification:'检查点',event_ids:['ev-001'],evidence_ids:['e-0001']}]}};
 const before=JSON.stringify(data),md=readableReport(data);
 assert.match(md,/转写版本：2；报告版本：3/);assert.match(md,/原文 00:00–00:31/);assert.match(md,/> 原文。/);assert.ok(md.includes('完整事实'.repeat(100)));assert.match(md,/完整性未确认/);assert.doesNotMatch(md,/ev-001|e-0001|allowed_events/);assert.equal(JSON.stringify(data),before);
 data.report.observations[0].event_ids=[];
 assert.match(readableReport(data),/&lt;script&gt;/,'legacy raw text must not become executable HTML');
 data.segments[0].text='The goal is to solve a question with an example.';
 assert.ok(readableReport(data).includes('> The goal is to solve a question with an example.'),'original evidence must not be relabeled or translated');
});
test('复核必须绑定明确的报告版本与转写版本',()=>{
 const target={report_version:2,input_revision_no:1,report_hash:'hash'};
 assert.equal(reviewTarget({report_version:2,input_revision_no:1,review_targets:[target]}),target);
 assert.equal(reviewTarget({input_revision_no:1,review_targets:[target]}),null);
 assert.equal(reviewTarget({report_version:2,input_revision_no:3,review_targets:[target]}),null);
});
test('同音频块多个引文只需一个回看入口，并按时间排序',()=>{
 const quotes=[{segment_id:'e-0002'},{segment_id:'e-0001'},{segment_id:'e-0002'},{segment_id:'missing'}];
 assert.deepEqual(evidenceRanges(quotes,segments),segments);
 assert.equal(quotes[0].segment_id,'e-0002','input mutated');
});
test('课堂依据使用教学含义与时间，不显示内部编号',()=>{
 const event={id:'ev-001',event_type:'goal',quotes:[{segment_id:'e-0002'}]};
 assert.equal(evidenceLabel(event,segments),'教学目标 · 00:31–01:01');
 assert.equal(evidenceLabel({event_type:'internal_unknown',quotes:[]},segments),'课堂依据');
});
test('诊断文本将编号转成对应依据或原文时间，不泄露不可读编号',()=>{
 const events=[{id:'ev-001',event_type:'goal',quotes:[{segment_id:'e-0002'}]}];
 const text=friendlyDiagnostic('事件 ev-001 引用了 e-0001-q01，ev-999 和 e-9999 不存在',events,segments);
 assert.match(text,/教学目标 · 00:31–01:01/);assert.match(text,/原文 00:00–00:31/);
 assert.doesNotMatch(text,/\bev-?\d+\b|\be-\d+\b/);
 assert.equal(friendlyDiagnostic('verification需核对；limitation仅说明输入范围',[],segments),'实施效果检查需核对；判断范围与限制仅说明输入范围');
});
test('报告关联失败保留结论序号、中文维度、原因和时间',()=>{
 const text=friendlyDiagnostic('六维报告证据链校验失败：第 2 条结论「引入具体例子」（思维引导）引用的「呈现方程组 · 00:31–01:01」只支持内容组织，不支持思维引导',[],segments);
 assert.match(text,/第 2 条结论「引入具体例子」/);
 assert.match(text,/只支持内容组织，不支持思维引导/);
 assert.match(text,/00:31–01:01/);
 assert.doesNotMatch(text,/\bev-?\d+\b|\be-\d+\b/);
});
