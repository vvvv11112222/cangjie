// Uses synthetic teaching feedback and local API fixtures, not real reports.
const assert=require('node:assert/strict');
const fs=require('node:fs');const path=require('node:path');const http=require('node:http');
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const root=path.resolve(__dirname,'../web'),copy=v=>JSON.parse(JSON.stringify(v));
const segments=[
 {id:'e-0001',start_ms:0,end_ms:31000,text:'本课目标是求解线性方程组。'},
 {id:'e-0002',start_ms:31000,end_ms:61000,text:'讲完矩阵后，我们再解释它在求解中的作用。接下来比较两种解法。'}
];
const events={events:[
 {id:'ev-002',event_type:'transition',summary:'预告后续回扣求解目标，再比较解法。',quotes:[{segment_id:'e-0002',text:'接下来比较两种解法。'},{segment_id:'e-0002',text:'讲完矩阵后，我们再解释它在求解中的作用。'}]},
 {id:'ev-001',event_type:'goal',summary:'先说明求解目标。',quotes:[{segment_id:'e-0001',text:segments[0].text}]}
]};
const report={analysis_version:'six-dim-coaching-v2',summary:'本段先说明求解目标，并预告讲解矩阵后回扣其作用。只能确认目标和安排，不能确认学生理解或后续执行。',summary_evidence_ids:['e-0001','e-0002'],dimensions:['content','pace','thinking','expression','management','technology'].map(code=>({dimension_code:code,coverage_status:['content','pace'].includes(code)?'observed':'insufficient',summary:code==='content'?'围绕求解问题引入知识。':code==='pace'?'口头交代后续讲解顺序，实际落实仍待回看。':'',summary_evidence_ids:code==='content'?['e-0001']:code==='pace'?['e-0002']:[],limitation:'这里只能依据本片段文字判断。'})),observations:[
 {dimension_code:'content',observation_type:'highlight',title:'先交代求解目标',observation_text:'教师首先说明本课要解决线性方程组问题。',analysis:'为后续概念提供问题背景，不代表学生已经掌握。',suggestion:'如果后续尚未回扣目标，可在讲完概念后尝试：\n1. 请学生指出这个概念在刚才例子中的位置。\n2. 追问“它在求解中起什么作用？”，让学生用一句话说明；若只重复名称，再结合原例追问。',verification:'收集一条学生解释，检查是否能说清概念与目标的关系。',event_ids:['ev-001'],evidence_ids:['e-0001']},
 {dimension_code:'pace',observation_type:'observation',title:'预告讲解顺序',observation_text:'教师预告先讲矩阵，再解释作用和比较解法。',analysis:'可以确认口头安排，不能据此认定后续已经执行或节奏合适。',suggestion:'',verification:'',event_ids:['ev-002'],evidence_ids:['e-0002']}
]};
const old=copy(report);delete old.analysis_version;for(const o of old.observations){delete o.title;delete o.analysis;delete o.verification}
let reviews=[];
let job={id:'sample',video_name:'六维反馈演示.mp4',status:'succeeded',stage:'completed',progress:100,asr_status:'complete',report_status:'ready',revision_no:1,input_revision_no:1,report_version:2,updated_at:'1',segments,events,report,report_versions:[{version:1,input_revision_no:1,asr_status:'complete',report_status:'ready',segments,events,report:old},{version:2,input_revision_no:1,asr_status:'complete',report_status:'ready',segments,events,report}]};
const targets=[{report_version:1,input_revision_no:1,report_hash:'oldhash'},{report_version:2,input_revision_no:1,report_hash:'testhash'}];
let reanalyzeCalls=0,reviewSaveCalls=0;
const server=http.createServer((req,res)=>{
 const send=v=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v))};
 if(req.url==='/api/health')return send({status:'ok',analysis_version:'six-dim-coaching-v6'});
 if(req.url==='/api/jobs')return send({jobs:[{...job,review_targets:targets}]});
 if(req.url==='/api/jobs/sample/reanalyze'&&req.method==='POST'){
  reanalyzeCalls++;job={...job,status:'queued',stage:'queued',report_status:'pending',report:null,events:{events:[]},updated_at:'queued'};return send({job});
 }
 if(req.url==='/api/jobs/sample')return send({job:{...job,review_targets:targets}});
 if(/\/api\/jobs\/sample\/reports\/[^/]+\/reviews$/.test(req.url)){
  const total=report.observations.length;const reviewed=reviews.length;
  const version=Number(req.url.split('/')[5]);
  const selected=targets.find(t=>t.report_version===version);
  return send({job_id:'sample',video_name:job.video_name,target:selected,review_source:'manual_local_unverified_identity',limitation:'本地人工填写，身份未经认证；仅统计本报告的已复核结论，不是教师评分或整体模型准确率。零分母表示暂不可计算，未复核不能视为通过。',report_snapshot:{version},reviews:version===2?copy(reviews):[],counts:{total,reviewed:version===2?reviewed:0,confirmed:reviews.filter(r=>r.fact_judgment==='supported'&&r.meaning_judgment==='bounded'&&r.suggestion_judgment==='usable').length,uncertain:0,needs_correction:reviews.filter(r=>r.fact_judgment==='unsupported'||r.meaning_judgment==='overreach'||r.suggestion_judgment==='not_usable').length,issues_reviewed:0,unsupported_issues:0,suggestions_reviewed:0,suggestions_usable:0}});
 }
 let reviewMatch=req.url.match(/^\/api\/jobs\/sample\/reports\/[^/]+\/reviews\/(\d+)$/);
 if(reviewMatch&&req.method==='PUT'){
  reviewSaveCalls++;
  let body='';req.on('data',c=>body+=c);req.on('end',()=>{
   const payload=JSON.parse(body);const index=Number(reviewMatch[1]);assert.equal(req.url.split('/')[5],'2');assert.equal(payload.report_hash,'testhash');
   const existing=reviews.findIndex(r=>r.observation_index===index);
   const record={report_version:2,input_revision_no:1,report_hash:'testhash',observation_index:index,revision:existing>=0?reviews[existing].revision+1:1,fact_judgment:payload.fact_judgment,meaning_judgment:payload.meaning_judgment,suggestion_judgment:payload.suggestion_judgment,notes:payload.notes,reviewer:payload.reviewer,updated_at:'now'};
   if(existing>=0)reviews[existing]=record;else reviews.push(record);
   res.setHeader('Content-Type','application/json');res.end(JSON.stringify({review:record}));
  });return;
 }
 if(req.url.endsWith('/media')){res.statusCode=404;return res.end()}
 const target=path.resolve(root,req.url==='/'?'index.html':req.url.slice(1));
 if(!target.startsWith(root+path.sep)||!fs.existsSync(target)){res.statusCode=404;return res.end()}
 res.setHeader('Content-Type',target.endsWith('.css')?'text/css':target.endsWith('.js')?'text/javascript':'text/html');res.end(fs.readFileSync(target));
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));let browser;
 try{
  browser=await chromium.launch({headless:true,...(process.env.BROWSER_EXECUTABLE?{executablePath:process.env.BROWSER_EXECUTABLE}:{})});
  const page=await browser.newPage({viewport:{width:1200,height:900}}),errors=[],dialogs=[];
  await page.addInitScript(()=>{window.replayAttempts=0;HTMLMediaElement.prototype.play=function(){window.replayAttempts++;return Promise.resolve()}});
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',async d=>{dialogs.push(d.message());await d.accept()});
  await page.goto('http://127.0.0.1:'+server.address().port);
  await page.waitForSelector('#report .dimension-card',{state:'attached'});
  const choose=async code=>page.locator('.report-sidebar button[data-section="'+code+'"]').click();
  assert.equal(await page.locator('#report article').count(),6);
  assert.match(await page.locator('.report-update-note').innerText(),/较早程序.*现有转写/);
  await page.evaluate(()=>renderAnalysis({...result,report:{...result.report,analysis_version:runtimeInfo.analysis_version}}));
  assert.equal(await page.locator('.report-update-note').count(),0,'current report must not be called outdated');
  await page.evaluate(()=>renderAnalysis({...result,report:{...result.report,analysis_version:'six-dim-coaching-v2'}}));
  assert.equal(await page.locator('.report-sidebar button').count(),7);
  assert.equal(await page.locator('.report-page:visible').count(),1);
  assert.equal(await page.locator('.analysis-overview').isVisible(),true);
  assert.match(await page.locator('.analysis-overview').innerText(),/本段先说明求解目标/);
  assert.equal(await page.locator('#dimension-content').isVisible(),false);
  assert.equal(await page.locator('#events-section details').getAttribute('open'),null);
  const downloadPromise=page.waitForEvent('download');
  await page.getByRole('button',{name:'导出易读报告（Markdown）',exact:true}).click();
  const download=await downloadPromise;
  assert.equal(download.suggestedFilename(),'课堂教学复盘.md');
  const stream=await download.createReadStream(),chunks=[];for await(const chunk of stream)chunks.push(chunk);
  const exported=Buffer.concat(chunks).toString('utf8');assert.match(exported,/原文 00:00–00:31/);assert.match(exported,/报告版本：2/);assert.doesNotMatch(exported,/ev-001|e-0001/);
  assert.match(exported,/教师的讲解思路/);
  await choose('content');
  assert.equal(await page.locator('.report-page:visible').count(),1);
  assert.equal(await page.locator('.analysis-overview').isVisible(),false);
  assert.equal(await page.locator('.analysis-reason').first().innerText(),'教学分析：'+report.observations[0].analysis);
  assert.match(await page.locator('.analysis-observation').first().innerText(),/事实：教师首先/);
  assert.match(await page.locator('.analysis-observation').first().innerText(),/下次课可尝试[\s\S]*1\. 请学生[\s\S]*2\. 追问/);
  assert.equal(await page.locator('.check-box p').first().isVisible(),true);
  assert.equal(await page.locator('.action-box p').first().isVisible(),true);
  assert.equal(await page.locator('.dimension-summary-details p').first().isVisible(),true);
  assert.equal(await page.locator('.analysis-observation details').count(),0);
  assert.equal(await page.locator('.analysis-observation').first().getByRole('button').count(),1);
  assert.equal(await page.locator('.analysis-nextsteps').count(),0);
  assert.equal(await page.locator('#report-review-tools').getAttribute('open'),null);
  assert.doesNotMatch(await page.locator('#report').textContent(),/为什么值得关注|完整事实/);
  // Keyboard navigation and rerendering preserve the current reading section.
  await page.locator('.report-sidebar button[data-section="content"]').focus();
  await page.keyboard.press('ArrowDown');
  assert.equal(await page.locator('#dimension-pace').isVisible(),true);
  await page.evaluate(()=>renderAnalysis(result));
  assert.equal(await page.locator('#dimension-pace').isVisible(),true);
  assert.equal(await page.locator('.teaching-flow li').count(),2);
  assert.match(await page.locator('.teaching-flow li').first().innerText(),/先说明求解目标/);
  assert.match(await page.locator('.teaching-flow li').nth(1).innerText(),/预告后续/);
  assert.match(await page.locator('.teaching-flow').innerText(),/不代表后续已经完成/);
  assert.equal(await page.locator('.dimensions').isVisible(),false,'legacy navigation must not duplicate the sidebar');
  await page.locator('.teaching-flow li button').last().click();
  assert.equal(await page.locator('#video').evaluate(v=>v.currentTime),31);
  await page.locator('#events-section summary').click();
  await choose('technology');
  assert.match(await page.locator('#dimension-technology').innerText(),/不据此判定存在教学问题/);
  await page.locator('#dimension-technology .dimension-limitation summary').click();
  assert.match(await page.locator('#dimension-technology').innerText(),/不证明真实操作/);
  await page.locator('#dimension-guide summary').click();
  assert.equal(await page.locator('#dimension-guide-content .dimension-guide-card').count(),6);
  assert.match(await page.locator('#dimension-guide').innerText(),/不能据此判断语速快慢/);
  await page.locator('#dimension-guide summary').click();
  await choose('pace');
  await page.getByRole('button',{name:'回看原文 · 教学安排与转换 · 00:31–01:01',exact:true}).click();
  assert.equal(await page.evaluate(()=>document.activeElement.id),'teaching-ev-002');
  assert.equal(await page.locator('#events-section details').getAttribute('open'),'');
  assert.equal(await page.locator('#video').evaluate(v=>v.currentTime),31,'combined entry must seek');
  assert.ok(await page.evaluate(()=>replayAttempts>0),'combined entry must attempt playback');
  const headings=await page.locator('#events-section h3').allTextContents();
  assert.equal(headings[0],'教学目标 · 00:00–00:31','events not chronological');
  assert.equal(await page.locator('#teaching-ev-002 blockquote').count(),2);
  assert.equal(await page.locator('#teaching-ev-002 button').count(),1,'duplicate playback per audio block');
  assert.doesNotMatch(await page.locator('body').innerText(),/\bev-?\d+\b|\be-\d+\b/);
  // Data linkage is retained even though technical identifiers are hidden.
  assert.equal(await page.evaluate(()=>result.report.observations[0].event_ids[0]),'ev-001');
  await page.locator('#teaching-ev-002 button').click();
  assert.equal(await page.locator('#video').evaluate(v=>v.currentTime),31);
  await page.locator('#events-section summary').click();
  await choose('content');
  await page.locator('.task-controls select').selectOption('1');
  assert.match(await page.locator('#report').textContent(),/这是旧版报告/);
  assert.equal(await page.locator('#report .verification').count(),0,'fabricated legacy check method');
  await page.locator('.task-controls select').selectOption('current');
  await choose('content');
  // Review and acceptance loop on the current report.
  await page.waitForSelector('.review-control',{state:'attached'});
  assert.equal(await page.locator('.review-control').first().isVisible(),false);
  await page.locator('#report-review-tools>summary').click();
  assert.match(await page.locator('#report-review-tools .review-purpose').innerText(),/不会修改报告、重新生成分析/);
  assert.match(await page.locator('.review-instructions').innerText(),/不会自动带入生成请求/);
  assert.match(await page.locator('#review-summary summary').textContent(),/已复核 0 \/ 2/);
  assert.equal(await page.locator('.review-control').count(),2);
  await page.locator('.review-control').first().locator('summary').click();
  await page.locator('.review-control').first().getByRole('link',{name:'查看对应课堂分析与原文入口'}).click();
  assert.equal(await page.locator('#dimension-content').isVisible(),true);
  assert.equal(await page.evaluate(()=>document.activeElement.id),'analysis-observation-0');
  assert.match(await page.locator('.review-control').first().innerText(),/课堂事实与原文相符吗[\s\S]*分析有没有超出原文[\s\S]*修改方向适合这段课吗/);
  assert.match(await page.locator('.review-control').first().innerText(),/原文只说教师提问/);
  assert.equal(await page.locator('.review-control').first().locator('select').nth(0).inputValue(),'','do not preapprove an unreviewed fact');
  await page.locator('.review-control').nth(1).locator('summary').click();
  await page.locator('.review-control').nth(1).locator('textarea').fill('另一条的未保存理由');
  await page.locator('.review-control').first().locator('select').nth(0).selectOption('supported');
  await page.locator('.review-control').first().locator('select').nth(1).selectOption('bounded');
  await page.locator('.review-control').first().locator('select').nth(2).selectOption('usable');
  await page.locator('.review-control').first().locator('select').nth(0).selectOption('unsupported');
  await page.locator('.review-control').first().getByRole('button',{name:'保存复核记录'}).click();
  assert.equal(reviewSaveCalls,0,'missing reason must be explained before sending');
  assert.match(await page.locator('.review-control').first().innerText(),/请用至少2个字/);
  await page.locator('.review-control').first().locator('select').nth(0).selectOption('supported');
  const reportBeforeReview=await page.evaluate(()=>JSON.stringify(result.report));
  await page.locator('.review-control').first().getByRole('button',{name:'保存复核记录'}).click();
  await page.waitForFunction(()=>document.querySelector('#review-summary')?.textContent?.includes('已复核 1 / 2'));
  assert.match(await page.locator('.review-control').first().locator('summary').textContent(),/原文支持/);
  assert.match(await page.locator('#review-summary').textContent(),/1 条通过人工复核/);
  assert.match(await page.locator('.review-control').first().innerText(),/报告内容未改动，不会自动重新生成分析/);
  assert.equal(reanalyzeCalls,0,'saving a review must not regenerate analysis');
  assert.equal(await page.evaluate(()=>JSON.stringify(result.report)),reportBeforeReview,'review must not rewrite report');
  job.updated_at='review-save';await page.evaluate(()=>refreshJobs());
  assert.equal(await page.locator('.review-control').nth(1).locator('textarea').inputValue(),'另一条的未保存理由','review save/poll must preserve other drafts');
  // A late response for the old report cannot overwrite the current version's reviews.
  await page.route('**/api/jobs/sample/reports/1/reviews',async route=>{await new Promise(resolve=>setTimeout(resolve,250));await route.continue()});
  const oldResponse=page.waitForResponse(r=>r.url().endsWith('/reports/1/reviews'));
  await page.locator('.task-controls select').selectOption('1');
  await page.locator('.task-controls select').selectOption('current');
  await oldResponse;
  await page.waitForSelector('.review-control',{state:'attached'});
  assert.match(await page.locator('#review-summary summary').textContent(),/已复核 1 \/ 2/);
  assert.match(await page.locator('.review-control').first().locator('summary').textContent(),/原文支持/);
  await page.unroute('**/api/jobs/sample/reports/1/reviews');
  await page.evaluate(()=>renderAnalysis({...result,report_version:undefined}));
  assert.equal(await page.locator('.review-control').count(),0,'no save controls without a valid version');
  await page.evaluate(()=>{renderStamp='';renderJob(selectedJob)});
  await page.waitForSelector('.review-control',{state:'attached'});
  const qa=process.env.ANALYSIS_QA_DIR?path.resolve(process.env.ANALYSIS_QA_DIR):path.resolve(__dirname,'../bin/qa');fs.mkdirSync(qa,{recursive:true});
  await page.locator('#report-review-tools').evaluate(el=>{el.open=true;el.querySelector('.review-control').open=true});
  await page.locator('#report-review-tools').scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(qa,'teacher-review-desktop.png')});
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'review help must fit mobile');
  await page.locator('.review-control').first().scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(qa,'teacher-review-mobile.png')});
  await page.setViewportSize({width:1200,height:900});
  await page.locator('#report-review-tools').evaluate(el=>el.open=false);
  await page.locator('#report-section').scrollIntoViewIfNeeded();
  await choose('content');
  await page.screenshot({path:path.join(qa,'analysis-panel-desktop.png')});
  await page.screenshot({path:path.join(qa,'analysis-feedback-desktop.png'),fullPage:true});
  await page.locator('#dimension-content').scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(qa,'teacher-report-desktop.png')});
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'mobile overflow');
  await page.screenshot({path:path.join(qa,'analysis-feedback-mobile.png'),fullPage:true});
  await page.locator('#dimension-content').scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(qa,'teacher-report-mobile.png')});
  // Historical source-only findings keep an accessible replay and exact original.
  await page.evaluate(()=>{const legacy=JSON.parse(JSON.stringify(result));delete legacy.report_version;legacy.report.observations[0].event_ids=[];renderAnalysis(legacy)});
  await choose('content');
  const fallback=page.locator('.analysis-observation').first();
  assert.equal(await fallback.getByRole('button').count(),1);
  await fallback.getByRole('button',{name:'回看原文 · 00:00–00:31',exact:true}).click();
  assert.equal(await fallback.locator('blockquote').textContent(),segments[0].text);
  assert.equal(await page.locator('#video').evaluate(v=>v.currentTime),0);
  // All quotes/ranges stay available when an event spans multiple audio blocks.
  await page.evaluate(()=>{const sample=JSON.parse(JSON.stringify(result));sample.report.observations[0].event_ids=['ev-001'];sample.report.observations[0].evidence_ids=['e-0001','e-0002'];sample.events.events.find(e=>e.id==='ev-001').quotes.push({segment_id:'e-0002',text:sample.segments[1].text});renderAnalysis(sample)});
  await page.getByRole('button',{name:'回看原文 · 教学目标 · 00:00–01:01',exact:true}).click();
  assert.equal(await page.locator('#teaching-ev-001 blockquote').count(),2);
  assert.equal(await page.locator('#teaching-ev-001 button').count(),2,'cross-block playback preserved');
  // Summaries without findings must be labeled, especially expression/management.
  await page.evaluate(()=>{
   const sample=JSON.parse(JSON.stringify(result));
   sample.report.observations=[{...sample.report.observations[1],dimension_code:'thinking',title:'自问自答引入概念'}];
   for(const code of ['expression','management']){const d=sample.report.dimensions.find(d=>d.dimension_code===code);d.coverage_status='observed';d.summary=code==='expression'?'教师简要解释术语的含义。':'本片段仅呈现课程信息介绍。'}
   renderAnalysis(sample);
  });
  for(const code of ['expression','management']){
   await choose(code);
   assert.equal(await page.locator('#dimension-'+code+' .dimension-summary-details>h4').textContent(),'课堂分析 · 本维度小结');
   assert.match(await page.locator('#dimension-'+code).innerText(),/未列出独立的问题或修改方向/);
  }
  await choose('thinking');
  assert.match(await page.locator('#dimension-thinking').innerText(),/课堂分析 · 课堂观察[\s\S]*不作为已确认的问题/);
  assert.equal(await page.locator('#dimension-thinking .action-box').count(),0,'do not invent an absent suggestion');
  await page.locator('#dimension-thinking').scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(qa,'teacher-classification-mobile.png')});
  // Audit flags remain visible alongside complete content; no nested duplicate
  // copies of the same suggestion or replacement of prose by placeholders.
  await page.evaluate(()=>{
   clearInterval(pollTimer);
   const sample=JSON.parse(JSON.stringify(selectedJob));
   sample.report.quality_audit={status:'needs_review',message:'部分分析需核对；模型提示不代表人工验收。',findings:[{target:'observation:1',fields:['analysis','suggestion'],kind:'context_conflict',reason:'后文已经说明作用，请先核对再决定是否补充。',source_ids:['e-0002']}]};
   window.auditFixture=sample;window.auditBefore=JSON.stringify(sample);
   renderAnalysis({...sample,job_id:sample.id,report_version:2,review_targets:[]});
  });
  await choose('content');
  assert.equal(await page.locator('.advice-pending p').isVisible(),true);
  assert.equal(await page.locator('.analysis-reason').first().isVisible(),true);
  assert.match(await page.locator('.analysis-reason').first().innerText(),/【待确认】/);
  assert.match(await page.locator('.audit-explanation').innerText(),/后文已经说明作用/);
  assert.equal(await page.locator('.advice-pending').evaluate(e=>e.tagName),'DIV');
  assert.equal(await page.locator('.audit-note').getByText(report.observations[0].suggestion,{exact:true}).count(),0);
  assert.equal(await page.locator('.advice-pending p').innerText(),report.observations[0].suggestion);
  assert.equal(await page.evaluate(()=>JSON.stringify(auditFixture)===auditBefore),true);
  assert.doesNotMatch(await page.locator('#report').innerText(),/observation:1|e-0002|ev-001/);
  await page.locator('.audit-note>details>summary').click();
  assert.match(await page.locator('.audit-note').innerText(),/回看/);
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  await page.screenshot({path:path.join(qa,'context-review-mobile.png')});
  await page.setViewportSize({width:1200,height:900});
  await page.locator('#dimension-content').scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(qa,'context-review-desktop.png')});
  await page.evaluate(()=>{const sample=JSON.parse(JSON.stringify(result));sample.report.quality_audit={status:'unavailable',message:'全文复核未完成',findings:[]};renderAnalysis(sample)});
  await choose('overview');
  assert.match(await page.locator('.audit-overview').innerText(),/未完成/);
  assert.equal(await page.locator('.advice-pending').count(),0);
  // A disputed summary remains immediately readable, explicitly marked.
  await page.evaluate(()=>{
   const sample=JSON.parse(JSON.stringify(result));
   sample.report.quality_audit={status:'partial_review',message:'部分提示通过引用检查',findings:[{target:'summary',fields:['summary'],criterion:'speaker_uncertainty',reason:'发言来源仍需核对。',report_quotes:[{field:'summary',text:sample.report.summary}],source_ids:[]}]};
   renderAnalysis(sample);
  });
  assert.equal(await page.locator('.analysis-overview').isVisible(),true);
  assert.ok((await page.locator('.analysis-overview').innerText()).includes(report.summary));
  assert.match(await page.locator('.analysis-overview').innerText(),/【待确认】/);
  assert.match(await page.locator('.analysis-overview .audit-explanation').innerText(),/发言来源仍需核对/);
  assert.doesNotMatch(await page.locator('.analysis-overview').innerText(),/本段概括待确认，请先参考/);
  // Status disputes retain Chinese labels and their reason, without protocol values.
  await page.evaluate(()=>{
   const sample=JSON.parse(JSON.stringify(result)),d=sample.report.dimensions.find(d=>d.dimension_code==='management');
   d.coverage_status='observed';d.summary='教师介绍接下来的讲解安排。';
   sample.report.quality_audit={status:'needs_review',findings:[{target:'dimension:management',fields:['coverage_status'],reason:'讲解安排不等于学生任务，需核对依据状态。',source_ids:['e-0002']}]};
   renderAnalysis(sample);
  });
  await choose('management');
  assert.match(await page.locator('#dimension-management').innerText(),/本维度依据待确认/);
  assert.match(await page.locator('#dimension-management .audit-note').innerText(),/讲解安排不等于学生任务/);
  assert.doesNotMatch(await page.locator('#dimension-management').innerText(),/coverage_status|observed|dimension:|e-0002/);
  // An uncertain speaker never becomes a confirmed highlight. The original
  // prose is present for review, with its own mark, and future advice stays open.
  await page.evaluate(()=>{
   const sample=JSON.parse(JSON.stringify(result)),o=sample.report.observations[0];
   o.title='提问激活学生已有经验';o.observation_text='教师提问后，学生提出热、pH、盐等全部因素。';
   o.suggestion='下次可请学生选择一个因素，先独立解释，再交流理由。';
   sample.segments[0].text='What causes denaturation? Yes. Heat, pH and salt.';
   sample.report.quality_audit={status:'needs_review',findings:[{target:'observation:1',fields:['observation_text'],criterion:'speaker_uncertainty',reason:'observation_text超出input_limits，Segment未区分说话人。',source_ids:['e-0001'],source_quotes:[{segment_id:'e-0001',text:'Yes.'}]}]};
   window.speakerFixture=sample;window.speakerFixtureBefore=JSON.stringify(sample);renderAnalysis(sample);
  });
  await choose('content');
  const pending=page.locator('#analysis-observation-0');
  assert.match(await pending.getAttribute('class'),/pending/);
  assert.equal(await pending.locator('.finding-kind').textContent(),'课堂分析 · 表述待确认');
  assert.match(await pending.locator('h4').textContent(),/^待确认的分析：/);
  assert.match(await pending.locator('.observation-fact').innerText(),/【待确认】教师提问后，学生提出热/);
  assert.equal(await pending.locator('.action-box p').isVisible(),true);
  assert.match(await pending.locator('.audit-explanation').innerText(),/未区分说话人/);
  assert.doesNotMatch(await pending.innerText(),/observation_text|input_limits|Segment|e-0001/);
  await pending.getByText('对照课堂原文',{exact:true}).click();
  assert.equal(await pending.locator('.audit-note blockquote').innerText(),'Yes.');
  assert.equal(await page.evaluate(()=>JSON.stringify(speakerFixture)===speakerFixtureBefore),true);
  for(const width of [768,390]){
   await page.setViewportSize({width,height:844});
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'report must fit '+width+'px');
   assert.equal(await page.locator('.report-page:visible').count(),1);
   assert.equal(await pending.locator('.action-box p').isVisible(),true);
  }
  await pending.scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(qa,'speaker-review-mobile.png')});
  await choose('pace');
  await page.locator('#dimension-pace').scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(qa,'teaching-flow-mobile.png')});
  await page.setViewportSize({width:1200,height:900});
  await page.locator('#dimension-pace').scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(qa,'teaching-flow-desktop.png')});
  await page.evaluate(()=>{renderStamp='';renderJob(selectedJob)});
  await page.getByRole('button',{name:'重新生成六维分析',exact:true}).click();
  await page.waitForFunction(()=>selectedJob.status==='queued');
  assert.equal(reanalyzeCalls,1);assert.match(dialogs.at(-1),/不调用 ASR/);assert.match(dialogs.at(-1),/文本模型费用/);
  assert.equal(await page.locator('#download').isVisible(),false);
  assert.equal(await page.locator('#report article').count(),0,'stale report shown during reanalysis');
  assert.deepEqual(errors,[]);
  console.log('PASS: section navigation and keyboard controls, visible full text/advice/checks, nonduplicated audit notes, chronological teaching flow, evidence playback, immutable reports, review linkage, historical versions, 1200/768/390px layout');
 }finally{if(browser)await browser.close();await new Promise(resolve=>server.close(resolve))}
})().catch(e=>{console.error(e);process.exitCode=1});
