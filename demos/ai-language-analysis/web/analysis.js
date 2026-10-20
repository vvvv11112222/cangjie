// Human-readable labels are presentation only. IDs remain unchanged in JSON
// and DOM anchors so evidence always refers to the original revision.
const dimensionNames={content:'内容组织',pace:'教学推进',thinking:'思维引导',expression:'解释表达',management:'活动组织',technology:'工具支持'};
const statusNames={observed:'有相关文字证据',insufficient:'本片段暂不能判断',not_applicable:'本片段不适用'};
const observationNames={highlight:'课堂分析 · 值得保留',issue:'发现的问题',observation:'课堂分析 · 课堂观察'};
const eventNames={goal:'教学目标',definition:'概念定义',example:'具体举例',question:'思考问题',reasoning:'推理过程',transition:'教学安排与转换',term_explanation:'术语解释',organization:'活动安排',tool_mention:'工具提及',resource_guidance:'资源指引'};
function reportUpdateNotice(report,runtime){
 if(!runtime?.analysis_version||!report)return '';
 if(report.analysis_version===runtime.analysis_version)return '';
 const saved=/-v(\d+)$/.exec(report.analysis_version||''),current=/-v(\d+)$/.exec(runtime.analysis_version);
 if(saved&&(!current||Number(saved[1])>=Number(current[1])))return '这份报告与当前程序使用的分析规则不同，请先确认已启动较新的程序。已有报告和校对记录仍可查看。';
 return '这是较早程序生成的报告，尚未应用当前的内容概览、具体建议和复核规则。可在任务区用现有转写重新生成；旧报告和校对记录保留，无需重新上传或转写。';
}
const dimensionGuides={
 content:{question:'目标、概念和例子是怎样连接的？',use:'帮助复盘内容组织，明确某个概念或例子在本段中的作用。',limit:'不认证知识全部正确，也不证明学生已经理解。'},
 pace:{question:'教师按什么思路展开，前后环节怎样衔接？',use:'按时间复盘概念、例子与问题的讲解脉络，区分已经展开的内容和口头预告。',limit:'目前只看教学推进，不能据此判断语速快慢或精确等待时间。'},
 thinking:{question:'提出了哪些思考要求，展示了哪些推理？',use:'区分比较、解释、预测和验证，设计可收集学生回答的问题。',limit:'教师自问自答或普通举例，不等于学生参与或思维能力提升。'},
 expression:{question:'术语、条件和概念关系怎样解释？',use:'帮助定位可进一步澄清的术语、指代和解释方式。',limit:'不评价音调、口音或发音；不能把识别错误当成教师问题。'},
 management:{question:'活动要求学生做什么、产出什么？',use:'检查口头任务、步骤、讨论或作业要求是否有明确指向。',limit:'没录到组织活动不代表管理差，也不能判断纪律和参与率。'},
 technology:{question:'提到哪个工具，明确说明了什么用途？',use:'检查工具是否服务具体教学任务，而不是只列出工具名称。',limit:'提到工具不证明真实操作，更不能证明工具产生了教学效果。'}
};
function evidenceRanges(quotes,segments){
 const sources=new Map((segments||[]).map(s=>[s.id,s]));
 const seen=new Set(),ranges=[];
 for(const q of quotes||[]){const s=sources.get(q.segment_id);if(!s)continue;const key=s.start_ms+'|'+s.end_ms;if(seen.has(key))continue;seen.add(key);ranges.push(s)}
 return ranges.sort((a,b)=>a.start_ms-b.start_ms||a.end_ms-b.end_ms);
}
function evidenceTime(ms){const sec=Math.floor(ms/1000);return String(Math.floor(sec/60)).padStart(2,'0')+':'+String(sec%60).padStart(2,'0')}
function evidenceLabel(event,segments){
 const ranges=evidenceRanges(event.quotes,segments);
 const time=ranges.length?' · '+evidenceTime(ranges[0].start_ms)+'–'+evidenceTime(ranges.at(-1).end_ms):'';
 return (eventNames[event.event_type]||'课堂依据')+time;
}
function friendlyDiagnostic(text,events,segments){
 const friendly=String(text||'').replace(/\bev-?\d+\b/g,id=>{
  const event=(events||[]).find(e=>e.id===id);
  return event?evidenceLabel(event,segments):'某条课堂依据';
 }).replace(/\be-\d+(?:-q\d+)?\b/g,id=>{
  const source=(segments||[]).find(s=>s.id===id.replace(/-q\d+$/,''));
  return source?'原文 '+evidenceTime(source.start_ms)+'–'+evidenceTime(source.end_ms):'某段原文';
 }).replace(/\b(?:allowed_events|dimension_event_choices)\b/g,'可引用的课堂依据')
 .replace(/\bdimension_codes?\b/g,'分析维度')
 .replace(/\b(?:original_segments|evidence_ids|summary_evidence_ids)\b/g,'原文依据')
 .replace(/\bevent_ids\b/g,'课堂依据')
 .replace(/\bobservation_text\b/g,'课堂事实')
 .replace(/\banalysis\b/g,'原因说明')
 .replace(/\bsuggestion\b/g,'修改方向')
 .replace(/\bsummary\b/g,'课堂小结')
 .replace(/\binput_limits\b/g,'材料限制')
 .replace(/\bSegment\b/g,'转写片段')
 .replace(/\btranscript_uncertainty\b/g,'发言来源或时间信息不明确')
 .replace(/\bspeaker_uncertainty\b/g,'发言来源或学习效果不明确')
 .replace(/\bcoverage_status\b/g,'依据状态')
 .replace(/\bverification\b/g,'实施效果检查')
 .replace(/\blimitation\b/g,'判断范围与限制');
 return friendly.replace(/\b(?:goal|definition|example|question|reasoning|transition|term_explanation|organization|tool_mention|resource_guidance)\b/g,code=>eventNames[code]);
}
function displayText(value,data){return friendlyDiagnostic(value,data?.events?.events,data?.segments)}
function briefText(value,max=100){const chars=Array.from(value||'');return chars.length<=max?value:chars.slice(0,max).join('')+'…'}
function auditFindings(report,target){return (report?.quality_audit?.findings||[]).filter(f=>f.target===target)}
function auditMessage(report){return report?.quality_audit?.message||'本报告尚未做模型全文复核，结论与建议仍需核对。'}
const reportFieldNames={coverage_status:'依据状态',summary:'课堂分析小结',limitation:'判断范围与限制',title:'发现标题',observation_text:'课堂事实',analysis:'原因说明',suggestion:'修改方向',verification:'实施效果检查'};
function auditNotice(finding){
 if(finding.fields?.length===1&&finding.fields[0]==='analysis')return {title:'这段分析需要核对',message:'原分析保留供阅读，但尚不能作为已确认的判断，请对照课堂原文。'};
 const notices={
  speaker_uncertainty:['发言来源或学习效果待确认','转写未可靠区分师生，不能据此确认谁作答或学生是否理解。请回看对应片段。'],
  context_consistency:['前后文表述待确认','这段概括可能与前后文不一致，请对照报告原句和课堂原文。'],
  relation_fidelity:['推理关系待确认','这段概括的推理方向、条件或适用范围可能与原文不同，请核对。'],
  subject_verification:['专业表述待确认','这段专业表述需要结合原文和学科知识核实，暂不作为确定结论。'],
  claim_scope:['分析范围待确认','这段分析可能超出了原文能够支持的范围，请核对。'],
  dimension_definition:['本维度依据待确认','当前材料可能不足以支持这个依据状态，请核对相关课堂片段。'],
  advice_relevance:['建议适用性待确认','这项建议可能与本段已讲内容或任务不匹配，采用前请先核对。']
 };
 const key=finding.criterion||(finding.kind==='transcript_uncertainty'?'speaker_uncertainty':'');
 const [title,message]=notices[key]||['本条表述待确认','这段表述需要对照课堂原文确认，暂不作为已确认结论。'];
 return {title,message};
}
function fieldNeedsReview(report,target,field){
 return auditFindings(report,target).some(f=>f.fields?.includes(field));
}
function observationNeedsReview(report,index){
 return auditFindings(report,'observation:'+(index+1)).some(f=>f.fields?.some(field=>['title','observation_text','analysis'].includes(field)));
}
function reportFieldDisplay(data,target,field,value){
 return (fieldNeedsReview(data.report,target,field)?'【待确认】':'')+displayText(value,data);
}
function auditQuoteText(quote,data){return quote.field==='coverage_status'&&Object.hasOwn(statusNames,quote.text)?statusNames[quote.text]:displayText(quote.text,data)}
// Show the complete saved field, rather than only the model's quoted excerpt.
// Older audit records may have no fields or no corresponding saved target.
function auditReportQuotes(report,finding){
 const target=finding.target==='summary'?report:finding.target?.startsWith('dimension:')?(report.dimensions||[]).find(d=>d.dimension_code===finding.target.slice(10)):finding.target?.startsWith('observation:')?report.observations?.[Number(finding.target.slice(12))-1]:null;
 const fields=[...new Set([...(finding.fields||[]),...(finding.report_quotes||[]).map(q=>q.field)])];
 return fields.map(field=>({field,text:typeof target?.[field]==='string'?target[field]:(finding.report_quotes||[]).find(q=>q.field===field)?.text})).filter(q=>q.text);
}
function auditOverview(report){
 const audit=report.quality_audit,count=audit?.findings?.length||0;
 if(audit?.status==='not_run')return {title:'报告检查未启用',message:'本报告没有进行全文检查，请对照课堂原文使用分析与建议。',pending:true};
 if(audit?.status==='partial_review')return {title:'报告检查部分完成',message:(count?'已标出 '+count+' 处待确认表述；':'')+'另有部分复核提示未能核实，报告仍需对照原文使用。',pending:true};
 if(!audit||!['no_flags','needs_review'].includes(audit.status))return {title:'报告检查未完成',message:'以下内容仍可阅读，但尚未完成全文检查；没有标记不代表已确认。',pending:true};
 return {title:count?'有 '+count+' 处报告表述待确认':'报告检查未发现明确疑点',message:count?'需要确认的位置已在下方标出；其余分析和建议仍可阅读。':'这只是模型检查结果，请结合课堂原文使用分析与建议。',pending:count>0};
}
function adviceNeedsReview(report,index){
 return auditFindings(report,'observation:'+(index+1)).some(f=>f.fields?.includes('suggestion'));
}
function renderAuditNotes(data,target,host){
 for(const finding of auditFindings(data.report,target)){
  const notice=auditNotice(finding);
  const box=add('div','',host);box.className='audit-note';
  const fields=(finding.fields||[]).map(field=>reportFieldNames[field]).filter(Boolean);
  add('strong',notice.title+(fields.length?'（'+fields.join('、')+'）':''),box);
  add('p',displayText(finding.reason||notice.message,data),box).className='audit-explanation';
  add('small','这是模型对报告表述的提醒，仍需对照原文确认，不是已认定的教学问题。',box);
  const details=add('details','',box);add('summary','对照课堂原文',details);
  for(const q of finding.source_quotes||[]){
   const source=(data.segments||[]).find(s=>s.id===q.segment_id);
   if(source){add('small','课堂原文 '+evidenceTime(source.start_ms)+'–'+evidenceTime(source.end_ms),details);add('blockquote',q.text,details)}
  }
  cite(finding.source_ids||[],details);
  if(!(finding.source_ids||[]).length&&!(finding.source_quotes||[]).length)add('small','此提醒没有附可定位的原文，请结合完整转写核对。',details);
 }
}
function readableReport(data){
 const report=data.report,lines=['# 课堂教学复盘','',`转写版本：${data.input_revision_no||'未记录'}；报告版本：${data.report_version||'未记录'}`,'','定位精度：音频块范围，非逐句时间。结论需人工复核，不是教师评分。','','## 本段讲了什么',''],auditLines=[];
 const escape=value=>String(value||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/([\\`*_{}\[\]#|!])/g,'\\$1');
 const text=value=>escape(displayText(value,data));
 const sources=new Map((data.segments||[]).map(s=>[s.id,s]));
 const sourceLine=s=>evidenceTime(s.start_ms)+'–'+evidenceTime(s.end_ms);
 const overview=auditOverview(report);
 auditLines.push('## 附录：报告检查说明','',overview.title+'：'+overview.message,'','全文复核：'+text(auditMessage(report)),'');
 if(report.quality_audit?.findings?.length)auditLines.push('模型提醒也可能误报，不等于已确认的课堂问题；请对照报告原句与课堂原文判断。','');
 for(const f of report.quality_audit?.findings||[]){
  const label=f.target==='summary'?'整体判断':f.target.startsWith('dimension:')?(dimensionNames[f.target.slice(10)]||'维度小结'):'第 '+f.target.split(':')[1]+' 条发现';
  const notice=auditNotice(f);
  auditLines.push('待确认 · '+label+' · '+notice.title+'：'+notice.message,'','核对说明：'+text(f.reason),'');
  const observation=f.target.startsWith('observation:')?report.observations?.[Number(f.target.slice(12))-1]:null;
  if(observation?.title&&!f.fields?.includes('title'))auditLines.push('原发现标题（供对照）：'+text(observation.title),'');
  for(const q of auditReportQuotes(report,f))auditLines.push('报告原句（'+(reportFieldNames[q.field]||'待核对内容')+'）：'+escape(auditQuoteText(q,data)),'');
  for(const id of f.source_ids||[]){const s=sources.get(id);if(s)auditLines.push('对照原文 '+sourceLine(s)+'：','> '+escape(s.text),'')}
 }
 lines.push(escape(reportFieldDisplay(data,'summary','summary',report.summary||'暂不能形成总体判断。')),'');
 for(const d of report.dimensions||[]){
  lines.push('## '+(dimensionNames[d.dimension_code]||'分析维度')+' · '+(fieldNeedsReview(report,'dimension:'+d.dimension_code,'coverage_status')?'本维度依据待确认':(statusNames[d.coverage_status]||'待复核')),'');
  if(d.dimension_code==='pace'){
   lines.push('### 教师的讲解思路','','按已保存的课堂依据排列；安排与预告不代表后续已经完成，时间为音频块范围。','');
   const flow=teachingFlow(data);
   if(!flow.length)lines.push('当前没有足够的可定位课堂依据，暂不能整理讲解脉络。','');
   for(const {event,ranges} of flow){lines.push('- '+text(eventNames[event.event_type])+' · '+sourceLine({start_ms:ranges[0].start_ms,end_ms:ranges.at(-1).end_ms})+'：'+text(event.summary));if(event.uncertainty)lines.push('  依据提醒：'+text(event.uncertainty))}
   lines.push('');
  }
  if(d.summary)lines.push('课堂分析 · 本维度小结：'+escape(reportFieldDisplay(data,'dimension:'+d.dimension_code,'summary',d.summary)),'');
  for(const o of report.observations||[]){
   if(o.dimension_code!==d.dimension_code)continue;
   const index=report.observations.indexOf(o),target='observation:'+(index+1);
   lines.push('### '+(observationNeedsReview(report,index)?'课堂记录待确认':(observationNames[o.observation_type]||'课堂观察')+'：'+escape(displayText(o.title||o.observation_text,data))),'','课堂事实：'+escape(reportFieldDisplay(data,target,'observation_text',o.observation_text)),'');
   if(o.analysis)lines.push('教学分析：'+escape(reportFieldDisplay(data,target,'analysis',o.analysis)),'');
   if(o.suggestion)lines.push((adviceNeedsReview(report,report.observations.indexOf(o))?'待核对的修改方向：':'修改方向 · 下次课可尝试：')+text(o.suggestion),'');
   if(o.verification)lines.push('怎么知道是否有效（实施建议后参考）：'+escape(reportFieldDisplay(data,target,'verification',o.verification)),'');
   const seen=new Set();
   for(const id of o.event_ids||[]){
    const event=(data.events?.events||[]).find(e=>e.id===id);if(!event)continue;
    lines.push('依据：'+text(evidenceLabel(event,data.segments)),'');
    for(const q of event.quotes||[]){const s=sources.get(q.segment_id);if(!s)continue;seen.add(s.id);lines.push('原文 '+sourceLine(s),'',...escape(q.text).split(/\r?\n/).map(line=>'> '+line),'')}
   }
   for(const id of o.evidence_ids||[]){const s=sources.get(id);if(!s||seen.has(id))continue;seen.add(id);lines.push('原文 '+sourceLine(s),'',...escape(s.text).split(/\r?\n/).map(line=>'> '+line),'')}
  }
  if(d.limitation)lines.push('判断限制：'+escape(reportFieldDisplay(data,'dimension:'+d.dimension_code,'limitation',d.limitation)),'');
 }
 for(const warning of data.warnings||[])lines.push('结果提示：'+text(warning),'');
 if(data.asr_status!=='complete')lines.push('输入限制：转写存在缺口或完整性未确认，本报告仅分析所保存的文字。','');
 lines.push(...auditLines);
 return lines.join('\n');
}
function downloadReadableReport(data){const url=URL.createObjectURL(new Blob([readableReport(data)],{type:'text/markdown;charset=utf-8'}));const link=document.createElement('a');link.href=url;link.download='课堂教学复盘.md';link.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}
function renderDimensionGuide(){
 const root=$('dimension-guide-content');if(!root)return;
 for(const [code,guide] of Object.entries(dimensionGuides)){
  const card=add('div','',root);card.className='dimension-guide-card';
  add('h3',dimensionNames[code],card);add('p',guide.question,card);
  add('p','作用：'+guide.use,card);add('small','边界：'+guide.limit,card);
 }
}
// This is a reading aid built from saved events, not a newly inferred lesson
// plan. Keep each event's wording and uncertainty, with block-level timestamps.
function teachingFlow(data){
 const types=new Set(['goal','definition','term_explanation','reasoning','example','question','transition','organization']);
 return (data.events?.events||[]).map((event,index)=>({event,index,ranges:evidenceRanges(event.quotes,data.segments)}))
  .filter(item=>types.has(item.event.event_type)&&item.event.summary&&item.ranges.length)
  .sort((a,b)=>a.ranges[0].start_ms-b.ranges[0].start_ms||a.index-b.index);
}
function renderTeachingFlow(data,host){
 const section=add('section','',host);section.className='teaching-flow';
 add('h4','教师的讲解思路',section);
 add('p','按已保存的课堂依据排列，帮助回顾讲解顺序。安排与预告不代表后续已经完成；时间为音频块范围，同一块内不判断精确先后。',section).className='flow-help';
 const stages=teachingFlow(data);
 if(!stages.length){add('p','当前没有足够的可定位课堂依据，暂不能整理讲解脉络。',section);return}
 const list=add('ol','',section);
 for(const {event,ranges} of stages){
  const stage=add('li','',list);
  add('span',eventNames[event.event_type]+' · '+evidenceTime(ranges[0].start_ms)+'–'+evidenceTime(ranges.at(-1).end_ms),stage).className='flow-stage-label';
  add('p',displayText(event.summary,data),stage);
  if(event.uncertainty)add('small','依据提醒：'+displayText(event.uncertainty,data),stage);
  const button=add('button','回看这一步的原文',stage);button.type='button';button.onclick=()=>showTeachingEvidence(event.id);
 }
}
const reportSectionDescriptions={overview:'本段内容与阅读提示',content:'目标、概念与例子的联系',pace:'讲解脉络与环节衔接',thinking:'提问、比较与推理',expression:'术语、条件与解释方式',management:'学生任务与活动要求',technology:'工具用途与使用依据'};
function createReportReader(data,root){
 const key=[data.job_id||data.id||'',data.report_version||'',data.input_revision_no||''].join('|');
 if(root.dataset.readerKey!==key){root.dataset.readerKey=key;root.dataset.activeSection='overview'}
 const preferred=root.dataset.activeSection;
 const layout=add('div','',root);layout.className='report-workspace';
 const nav=add('nav','',layout);nav.className='report-sidebar';nav.setAttribute('aria-label','六维分析阅读目录');
 add('strong','阅读目录',nav);
 const pages=add('div','',layout);pages.className='report-pages';
 const entries=[];
 const select=(code,focus=false)=>{
  root.dataset.activeSection=code;
  for(const entry of entries){const active=entry.code===code;entry.panel.hidden=!active;entry.button.setAttribute('aria-pressed',String(active))}
  if(focus){const panel=entries.find(entry=>entry.code===code)?.panel;panel?.scrollIntoView({block:'start',behavior:'instant'})}
 };
 return {add(code,label,panel){
  pages.append(panel);panel.classList.add('report-page');panel.tabIndex=-1;
  if(!panel.id)panel.id='report-'+code;
  const button=add('button','',nav);button.type='button';button.setAttribute('aria-controls',panel.id);button.dataset.section=code;
  add('span',label,button);add('small',reportSectionDescriptions[code],button);
  const count=(data.report.observations||[]).filter(o=>o.dimension_code===code).length;
  if(code!=='overview')add('small',count?count+' 条分析'+((data.report.observations||[]).some(o=>o.dimension_code===code&&o.suggestion)?' · 含修改方向':''):'查看分析范围',button).className='nav-count';
  button.onclick=()=>select(code,true);
  button.onkeydown=event=>{
   const index=entries.findIndex(entry=>entry.code===code);let next;
   if(['ArrowDown','ArrowRight'].includes(event.key))next=(index+1)%entries.length;
   if(['ArrowUp','ArrowLeft'].includes(event.key))next=(index-1+entries.length)%entries.length;
   if(event.key==='Home')next=0;if(event.key==='End')next=entries.length-1;
   if(next!==undefined){event.preventDefault();entries[next].button.focus();select(entries[next].code)}
  };
  entries.push({code,panel,button});select(entries.some(entry=>entry.code===preferred)?preferred:'overview');
 }};
}
function renderReport(data,root){
 const report=data.report,observations=report.observations||[];
 const reader=createReportReader(data,root);
 const intro=add('section','',root);intro.className='analysis-overview';
 reader.add('overview','课堂概览',intro);
 const overviewStatus=auditOverview(report);
 add('h3','课堂概览',intro);
 const updateNotice=reportUpdateNotice(report,data.runtime_info);
 if(updateNotice)add('p',updateNotice,intro).className='report-update-note result-warning';
 add('strong','本段讲了什么',intro);
 add('p',reportFieldDisplay(data,'summary','summary',report.summary||'目前没有足够的文字证据形成总体判断。请查看各维度的观察限制。'),intro);
 renderAuditNotes(data,'summary',intro);
 const overview=add('details','',intro);overview.className='report-overview';
 add('summary','查看本段概览的原文依据',overview);cite(report.summary_evidence_ids||[],overview);
 const exportButton=add('button','导出易读报告（Markdown）',intro);exportButton.type='button';exportButton.onclick=()=>downloadReadableReport(data);
 add('small','“有相关文字证据”不表示优秀，“暂不能判断”也不表示教学差。这是片段复盘，不是教师评分。',intro);
 add('small','阅读提示：“课堂分析”说明观察到的做法；“发现的问题”指出有依据的问题；“修改方向”说明下次可以怎么做。',intro).className='report-reading-key';
 const auditBox=add('details','',intro);auditBox.className='audit-overview'+(overviewStatus.pending?' needs-review':'');
 add('summary',overviewStatus.title+' · 查看说明',auditBox);
 add('p',overviewStatus.message,auditBox);
 add('p',displayText(auditMessage(report),data),auditBox);
 add('small','这是系统对报告文字的检查，不是对教师的评价；检查意见也可能有误。',auditBox);
 if(!report.analysis_version&&observations.some(o=>!o.analysis)){
  add('p','这是旧版报告，部分条目缺少分析说明或后续观察方法。可在任务区用现有转写重新生成分析，获得新版反馈。',intro).className='result-warning';
 }
 for(const [code,guide] of Object.entries(dimensionGuides)){
  const d=(report.dimensions||[]).find(item=>item.dimension_code===code);if(!d)continue;
  const card=add('article','',root);card.id='dimension-'+code;card.dataset.code=code;card.className='dimension-card';
  reader.add(code,dimensionNames[code],card);
  const heading=add('h3',dimensionNames[code],card);heading.dataset.translated='true';
  add('span',fieldNeedsReview(report,'dimension:'+code,'coverage_status')?'本维度依据待确认':(statusNames[d.coverage_status]||'待复核'),heading).className='badge '+(fieldNeedsReview(report,'dimension:'+code,'coverage_status')?'insufficient':d.coverage_status);
  add('p',guide.question,card).className='dimension-question';
  if(code==='pace')renderTeachingFlow(data,card);
  const items=observations.filter(o=>o.dimension_code===code);
  if(d.summary){
   const host=add('section','',card);host.className='dimension-summary-details';
   add('h4',code==='pace'?'课堂分析 · 推进与衔接':'课堂分析 · 本维度小结',host);
   add('p',reportFieldDisplay(data,'dimension:'+code,'summary',d.summary),host).className='dimension-summary';cite(d.summary_evidence_ids||[],host);
  }
  renderAuditNotes(data,'dimension:'+code,card);
  if(!items.length)add('small',d.coverage_status==='observed'?'本维度仅提供课堂分析，未列出独立的问题或修改方向。':'课堂分析：当前依据不足或本片段不适用，不据此判定存在教学问题。',card).className='dimension-classification-note';
  for(const [type,label] of [['issue','发现的问题'],['highlight','课堂分析 · 值得保留的做法'],['observation','课堂分析 · 课堂观察'],['pending','课堂记录与分析 · 待确认']]){
   const group=items.filter(o=>observationNeedsReview(report,observations.indexOf(o))?type==='pending':o.observation_type===type);if(!group.length)continue;
   for(const o of group){
    const section=add('section','',card);section.className='analysis-observation '+type;section.id='analysis-observation-'+observations.indexOf(o);section.tabIndex=-1;
    const target='observation:'+(observations.indexOf(o)+1);
    add('span',type==='pending'?'课堂分析 · 表述待确认':observationNames[type],section).className='finding-kind '+type;
    if(o.title)add('h4',type==='pending'?'待确认的分析：'+displayText(o.title,data):displayText(o.title,data),section);
    const fact=reportFieldDisplay(data,target,'observation_text',o.observation_text);
    add('p','课堂事实：'+fact,section).className='observation-fact';
    if(type==='observation')add('small','本条记录课堂现象，不作为已确认的问题。',section).className='observation-kind-note';
    if(o.analysis){
     const meaning=add('p','',section);meaning.className='analysis-reason';
     add('strong','教学分析：',meaning);meaning.appendChild(document.createTextNode(reportFieldDisplay(data,target,'analysis',o.analysis)));
    }
    if(o.suggestion){
     const pending=adviceNeedsReview(report,observations.indexOf(o));
     const action=add('div','',section);action.className='action-box'+(pending?' advice-pending':'');
     add('strong',pending?'修改方向 · 采用前需核对':'修改方向 · 下次课可尝试',action);
     add('p',displayText(o.suggestion,data),action);
    }
    if(o.verification){
     const check=add('div','',section);check.className='check-box';
     add('strong','实施建议后，怎么知道是否有效',check);
     add('p',reportFieldDisplay(data,target,'verification',o.verification),check);
    }
    renderAuditNotes(data,target,section);
    const refs=add('div','',section);refs.className='observation-evidence';
    const covered=new Set(),seenEvents=new Set();
    for(const id of o.event_ids||[]){
     const event=(data.events?.events||[]).find(e=>e.id===id);if(!event)continue;
     if(seenEvents.has(id))continue;seenEvents.add(id);
     for(const q of event.quotes||[])covered.add(q.segment_id);
     const button=add('button','回看原文 · '+evidenceLabel(event,data.segments),refs);button.type='button';
     button.title='跳到课堂依据并回放对应音频';button.onclick=()=>showTeachingEvidence(id);
    }
    for(const source of evidenceRanges((o.evidence_ids||[]).filter(id=>!covered.has(id)).map(segment_id=>({segment_id})),data.segments)){
     const button=add('button','回看原文 · '+evidenceTime(source.start_ms)+'–'+evidenceTime(source.end_ms),refs);button.type='button';
     button.onclick=()=>{const original=refs.querySelector('.source-excerpt')||add('blockquote','',refs);original.className='source-excerpt';original.textContent=source.text;$('video').currentTime=source.start_ms/1000;$('video').play().catch(()=>{})};
    }
   }
  }
  const boundary=add('details','',card);boundary.className='dimension-limitation';add('summary','判断范围与限制',boundary);if(d.limitation)add('small',reportFieldDisplay(data,'dimension:'+code,'limitation',d.limitation),boundary);add('small',guide.limit,boundary);
 }
}
// jobs.js adds optional review tools after the teacher-facing report.
// ---- Review and acceptance ----
const reviewFactNames={supported:'原文支持',unsupported:'原文不支持',uncertain:'暂不能判断'};
const reviewMeaningNames={bounded:'解读有依据',overreach:'解读超出原文',uncertain:'暂不能判断'};
const reviewSuggestionNames={usable:'建议可执行',not_usable:'建议不可用',uncertain:'暂不能判断',not_applicable:'无建议'};
function reviewTarget(data){const version=data?.report_version||0;return (data?.review_targets||[]).find(t=>t.report_version===version&&t.input_revision_no===data.input_revision_no)||null}
async function loadReviewBundle(data){
 const target=reviewTarget(data);if(!target)return null;
 const bundle=await jobRequest('/api/jobs/'+encodeURIComponent(data.job_id)+'/reports/'+target.report_version+'/reviews');
 if(bundle.job_id!==data.job_id||bundle.target?.report_version!==target.report_version||bundle.target?.report_hash!==target.report_hash)throw Error('复核记录与当前报告不一致，请刷新后再试');
 return bundle;
}
function renderReviewSummary(root,data,bundle,message){
 const wasOpen=root.querySelector('#review-summary')?.open;
 root.querySelector('#review-summary')?.remove();
 const box=add('details','',root);box.id='review-summary';box.className='review-summary';
 box.open=!!wasOpen;
 const c=bundle?.counts;
 add('summary',c?'校对记录（已复核 '+c.reviewed+' / '+c.total+' 条）':'校对记录 · '+(message||'暂不可用'),box);
 add('p','这里汇总你对当前报告的人工判断，供之后核查和导出；记录与当前报告版本对应。',box).className='evidence-help';
 if(bundle){
  const line=[];
  if(c.confirmed)line.push(c.confirmed+' 条通过人工复核');
  if(c.needs_correction)line.push(c.needs_correction+' 条需要修正');
  if(c.uncertain)line.push(c.uncertain+' 条暂不能判断');
  if(c.total&&c.reviewed===c.total)line.unshift('全部结论均已复核。');
  add('p',line.length?line.join('；')+'。':'还没有填写复核结论。可在下面的对应条目中填写判断。',box);
  add('small',bundle.limitation,box);
  const download=add('button','导出复核记录（JSON）',box);download.type='button';
  download.onclick=()=>{const u=URL.createObjectURL(new Blob([JSON.stringify(bundle,null,2)],{type:'application/json'}));const a=document.createElement('a');a.href=u;a.download='review-report-v'+bundle.target.report_version+'.json';a.click();setTimeout(()=>URL.revokeObjectURL(u),1000)};
 }else{
  add('p',message||'本报告尚未匹配到可复核的已保存版本。',box);
 }
}
function renderReviewControl(observation,index,section,data,bundle,refreshSummary){
 const target=reviewTarget(data);if(!target)return;
 let item=(bundle.reviews||[]).find(r=>r.observation_index===index+1);
 const wrap=add('details','',section);wrap.className='review-control';
 const summary=add('summary',item?'复核：'+(reviewFactNames[item.fact_judgment]||item.fact_judgment)+' / '+(reviewMeaningNames[item.meaning_judgment]||item.meaning_judgment):'复核这条结论',wrap);
 if(item)summary.className='reviewed';
 const form=add('div','',wrap);form.className='review-form';
 const sourceLink=add('a','查看对应课堂分析与原文入口',form);sourceLink.href='#analysis-observation-'+index;
 sourceLink.onclick=event=>{event.preventDefault();rootReportSection(observation.dimension_code);const finding=document.getElementById('analysis-observation-'+index);finding?.focus({preventScroll:true});finding?.scrollIntoView({block:'start'})};
 const mkSelect=(labelText,names,current,help)=>{const label=add('label',labelText,form);const sel=document.createElement('select');if(!current)sel.append(new Option('请选择判断',''));for(const [value,name] of Object.entries(names))sel.append(new Option(name,value));sel.value=current||'';label.append(sel);add('small',help,label);return sel};
 const factSel=mkSelect('1. 课堂事实与原文相符吗？',reviewFactNames,item?.fact_judgment,'先回看对应原文。原文确实说了这件事，选“原文支持”；找不到或与原文相反，选“原文不支持”；拿不准就选“暂不能判断”。');
 const meaningSel=mkSelect('2. 分析有没有超出原文？',reviewMeaningNames,item?.meaning_judgment,'例如，原文只说教师提问，分析却断定学生已经理解，应选“解读超出原文”；解释谨慎且有依据时选“解读有依据”。');
 const hasSuggestion=!!(observation.suggestion||'').trim();
 const suggestionNames=hasSuggestion?{usable:reviewSuggestionNames.usable,not_usable:reviewSuggestionNames.not_usable,uncertain:reviewSuggestionNames.uncertain}:{not_applicable:reviewSuggestionNames.not_applicable};
 const sugSel=mkSelect('3. 修改方向适合这段课吗？',suggestionNames,item?.suggestion_judgment||(hasSuggestion?'':'not_applicable'),hasSuggestion?'针对本段内容、步骤清楚且能照做，选“建议可执行”；内容不适合或无法实施，选“建议不可用”。不要求先实施才能判断。':'本条未给修改方向，已标记“无建议”，无需另选。');
 const rowNotes=add('label','具体说明（不认可或无法判断时必填，至少2字）',form);const notes=document.createElement('textarea');notes.rows=2;notes.maxLength=2000;notes.placeholder='例如：原文只是预告比较解法，还没有实际比较，不能认定已完成比较。';notes.value=item?.notes||'';rowNotes.append(notes);
 const rowReviewer=add('label','填写人（可留空，仅作本地记录）',form);const reviewer=document.createElement('input');reviewer.maxLength=80;reviewer.placeholder='姓名或称呼';reviewer.value=item?.reviewer||'';rowReviewer.append(reviewer);
 const status=add('small','',form);
 const save=add('button',item?'更新复核记录':'保存复核记录',form);save.type='button';
 save.onclick=async()=>{
  if(!factSel.value||!meaningSel.value||!sugSel.value){status.textContent='请先逐项选择复核判断。';return}
  if((factSel.value!=='supported'||meaningSel.value!=='bounded'||['not_usable','uncertain'].includes(sugSel.value))&&Array.from(notes.value.trim()).length<2){status.textContent='请用至少2个字说明哪里不符合原文、建议为何不适用，或为什么暂时无法判断。';notes.focus();return}
  save.disabled=true;status.textContent='保存中…';
  try{
   const payload={report_hash:target.report_hash,expected_revision:item?item.revision:0,fact_judgment:factSel.value,meaning_judgment:meaningSel.value,suggestion_judgment:sugSel.value,notes:notes.value.trim(),reviewer:reviewer.value.trim()};
   const saved=await jobRequest('/api/jobs/'+encodeURIComponent(data.job_id)+'/reports/'+target.report_version+'/reviews/'+(index+1),{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
   if(result!==data||!section.isConnected)return;
   item=saved.review;
   summary.textContent='复核：'+reviewFactNames[item.fact_judgment]+' / '+reviewMeaningNames[item.meaning_judgment];summary.className='reviewed';save.textContent='更新复核记录';
   status.textContent='校对记录已保存（第 '+item.revision+' 次）。报告内容未改动，不会自动重新生成分析。';
   try{const latest=await loadReviewBundle(data);if(result===data&&section.isConnected)refreshSummary(latest)}catch(e){status.textContent+=' 汇总暂未刷新：'+e.message}
  }catch(e){status.textContent='保存失败：'+e.message}finally{save.disabled=false}
 };
}
async function wireReview(data,root){
 if(!root.querySelector('.analysis-overview'))return;
 const target=reviewTarget(data);
 if(!target)return;
 const tools=add('details','',root);tools.id='report-review-tools';tools.className='report-review-tools';
 add('summary','分析校对（记录你对报告的判断）',tools);
 add('p','保存校对只记录你的判断和理由，不会修改报告、重新生成分析或自动训练模型。日常查看课堂建议无需填写。',tools).className='review-purpose';
 const steps=add('ol','',tools);steps.className='review-instructions';
 add('li','选择下面一条结论，通过“查看对应课堂分析与原文入口”回看原文。',steps);
 add('li','分别判断课堂事实、原因说明和修改方向；不确定时可选“暂不能判断”，并写明原因。',steps);
 add('li','点击“保存复核记录”保存本条判断。若想重新生成报告，请到当前任务区单独操作；校对意见目前不会自动带入生成请求，重新生成会调用文本模型并可能计费。',steps);
 const summaryHost=add('div','',tools);
 const refreshSummary=bundle=>renderReviewSummary(summaryHost,data,bundle);
 renderReviewSummary(summaryHost,data,null,target?'正在读取复核记录…':'尚未匹配到可复核的报告版本');
 if(!target)return;
 let bundle;
 try{bundle=await loadReviewBundle(data)}catch(e){if(result===data&&summaryHost.isConnected)renderReviewSummary(summaryHost,data,null,'读取失败：'+e.message);return}
 if(result!==data||!summaryHost.isConnected)return;
 refreshSummary(bundle);
 const sections=root.querySelectorAll('.analysis-observation');
 const observations=data.report.observations||[];
 sections.forEach(section=>{const idx=Number(section.id.replace('analysis-observation-',''));if(!Number.isNaN(idx)&&observations[idx]){
  const host=add('section','',tools);host.className='review-item';
  add('h4',dimensionNames[observations[idx].dimension_code]+' · '+displayText(observations[idx].title||observations[idx].observation_text,data),host);
  renderReviewControl(observations[idx],idx,host,data,bundle,refreshSummary);
 }});
}
function rootReportSection(code){document.querySelector('#report .report-sidebar button[data-section="'+code+'"]')?.click()}
if(typeof module!=='undefined')module.exports={dimensionNames,dimensionGuides,evidenceRanges,evidenceLabel,friendlyDiagnostic,briefText,readableReport,reviewTarget,wireReview,loadReviewBundle,auditFindings,auditMessage,adviceNeedsReview,auditNotice,fieldNeedsReview,observationNeedsReview,reportFieldDisplay,auditReportQuotes,auditOverview,reportUpdateNotice,teachingFlow};
