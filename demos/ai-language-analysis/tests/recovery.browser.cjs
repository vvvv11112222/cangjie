// Local fixtures only: no .env, real video processing or cloud provider.
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const http=require('node:http');
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const root=path.resolve(__dirname,'../web');
const report={summary:'',summary_evidence_ids:[],dimensions:['content','pace','thinking','expression','management','technology'].map(d=>({dimension_code:d,coverage_status:'insufficient',summary:'',limitation:'证据不足',summary_evidence_ids:[]})),observations:[]};
const segment={id:'e-0001',start_ms:0,end_ms:1000,text:'课堂原文。'};
const oldVersion={version:1,input_revision_no:1,asr_status:'incomplete',report_status:'failed',segments:[segment],events:{events:[]},report};
const linkFailure='六维报告证据链校验失败：第 1 条结论「以核心问题统整多重视角」（内容组织）引用的「教师预告将从行图像开始讲解 · 01:31–02:01」当前归为教学推进，与本条结论的内容组织不一致；需核对事件分类和结论范围';
let job={id:'sample',video_name:'恢复测试.mp4',status:'partial',stage:'partial',progress:100,asr_status:'complete',report_status:'failed',error:linkFailure,warnings:[linkFailure],failure_stage:'report',revision_no:1,input_revision_no:1,retryable:true,updated_at:'1',segments:[segment],events:{events:[]},report,report_versions:[oldVersion],event_request:{request_id:'current-events'},text_request:{request_id:'current-report'}};
let jobs=[job],retryCalls=0,newCalls=0;
const server=http.createServer((req,res)=>{
 const send=value=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value))};
 if(req.url==='/api/jobs'&&req.method==='GET')return send({jobs});
 const match=req.url.match(/^\/api\/jobs\/([^/]+)(?:\/(retry|retranscribe|media))?$/);
 if(match){
  const found=jobs.find(j=>j.id===match[1]);if(!found){res.statusCode=404;return send({error:'not found'})}
  if(match[2]==='media'){res.statusCode=404;return res.end()}
  if(match[2]==='retry'&&req.method==='POST'){
   retryCalls++;found.status='queued';found.stage='queued';found.retryable=false;found.report_status='pending';found.report=null;found.error='';found.updated_at=String(Date.now());
   return send({job:found});
  }
  if(match[2]==='retranscribe'&&req.method==='POST'){
   newCalls++;const created={id:'new-task',video_name:found.video_name,status:'queued',stage:'queued',progress:0,asr_status:'incomplete',report_status:'pending',revision_no:0,updated_at:'new',segments:[],report_versions:[]};jobs.unshift(created);return send({job:created});
  }
  return send({job:found});
 }
 const target=path.resolve(root,req.url==='/'?'index.html':req.url.slice(1));
 if(!target.startsWith(root+path.sep)||!fs.existsSync(target)){res.statusCode=404;return res.end()}
 res.setHeader('Content-Type',target.endsWith('.css')?'text/css':target.endsWith('.js')?'text/javascript':'text/html');res.end(fs.readFileSync(target));
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));let browser;
 try{
  browser=await chromium.launch({headless:true,...(process.env.BROWSER_EXECUTABLE?{executablePath:process.env.BROWSER_EXECUTABLE}:{})});
  const page=await browser.newPage({viewport:{width:1200,height:900}}),errors=[],dialogs=[];
  page.on('pageerror',e=>errors.push(e.message));let accept=true;
  page.on('dialog',async d=>{dialogs.push(d.message());await (accept?d.accept():d.dismiss())});
  await page.goto('http://127.0.0.1:'+server.address().port);
  await page.waitForFunction(()=>document.getElementById('job-state').textContent.includes('部分完成'));
  assert.match(await page.locator('#report').textContent(),/分析未完成/);
  assert.equal(await page.locator('#report article').count(),0);
  assert.equal(await page.locator('#download').isVisible(),false);
  assert.match(await page.locator('#transcript').textContent(),/课堂原文/);
  assert.match(await page.locator('#job-error').textContent(),/第 1 条结论「以核心问题统整多重视角」/);
  assert.match(await page.locator('#job-error').textContent(),/当前归为教学推进，与本条结论的内容组织不一致/);
  assert.match(await page.locator('#report').textContent(),/教师预告将从行图像开始讲解 · 01:31–02:01/);
  await page.locator('#error-details summary').click();
  assert.doesNotMatch(await page.locator('body').innerText(),/\bev-?\d+\b|\be-\d+\b/);
  const initialQa=path.resolve(__dirname,'../bin/qa');fs.mkdirSync(initialQa,{recursive:true});
  await page.screenshot({path:path.join(initialQa,'evidence-feedback-failure-desktop.png'),fullPage:true});
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'diagnostic mobile overflow');
  await page.screenshot({path:path.join(initialQa,'evidence-feedback-failure-mobile.png'),fullPage:true});
  await page.setViewportSize({width:1200,height:900});
  // Regression for a model response that omitted every conclusion's event links.
  job.error='六维报告证据链校验失败：第 1 条结论「先交代求解目标」（内容组织）：模型漏填了课堂依据关联，无法核对原文来源；已有转写已保留，无需重新转写';
  job.warnings=[job.error];job.updated_at='missing-event';
  await page.reload();
  await page.waitForFunction(()=>document.getElementById('report').textContent.includes('模型漏填'));
  assert.match(await page.locator('#report').textContent(),/无需重新转写/);
  assert.equal(await page.locator('#report article').count(),0);
  assert.equal(await page.locator('#download').isVisible(),false);
  assert.match(await page.locator('#transcript').textContent(),/课堂原文/);
  assert.doesNotMatch(await page.locator('body').innerText(),/\bev-?\d+\b|\be-\d+\b|event_ids/);
  await page.locator('#report-section').scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(initialQa,'report-contract-failure-desktop.png')});
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'missing-link mobile overflow');
  await page.screenshot({path:path.join(initialQa,'report-contract-failure-mobile.png')});
  await page.setViewportSize({width:1200,height:900});
  // New generation contract failures remain readable and recoverable.
  for(const failure of [
   '第 1 条结论「先交代求解目标」模型漏填了课堂依据选择；已有转写已保留，无需重新转写',
   '第 1 条结论「先交代求解目标」选择了本次目录中不存在的课堂依据；已有转写已保留，无需重新转写',
   '模型未按本次报告格式返回：结论只需选择一项课堂依据，不能另填维度或事件列表；已有转写已保留，无需重新转写'
  ]){
   job.error='六维报告生成失败：'+failure;job.warnings=[job.error];job.updated_at=failure;
   await page.reload();
   await page.waitForFunction(()=>document.getElementById('report').textContent.includes('无需重新转写'));
   assert.equal(await page.locator('#report article').count(),0);
   assert.equal(await page.locator('#download').isVisible(),false);
   assert.match(await page.locator('#transcript').textContent(),/课堂原文/);
   assert.doesNotMatch(await page.locator('body').innerText(),/\bbasis-\d+\b|\bev-?\d+\b|\be-\d+\b|evidence_key|event_ids|dimension_code/);
  }
  await page.locator('#report-section').scrollIntoViewIfNeeded();
  await page.screenshot({path:path.join(initialQa,'evidence-basis-failure-desktop.png')});
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'basis contract mobile overflow');
  await page.screenshot({path:path.join(initialQa,'evidence-basis-failure-mobile.png')});
  await page.setViewportSize({width:1200,height:900});
  assert.equal(await page.evaluate(()=>result.text_request.request_id),'current-report','borrowed historical metadata');
  assert.equal(await page.getByRole('button',{name:'用原视频新建转写任务'}).isVisible(),false);
  await page.locator('.task-controls select').selectOption('1');
  assert.match(await page.locator('#report').textContent(),/分析未完成/);
  assert.equal(await page.locator('#download').isVisible(),false);
  await page.locator('.task-controls select').selectOption('current');
  await page.getByRole('button',{name:'重新生成分析',exact:true}).click();
  await page.waitForFunction(()=>selectedJob.status==='queued');
  assert.equal(retryCalls,1);assert.match(dialogs.at(-1),/不调用 ASR/);
  assert.equal(await page.getByRole('button',{name:'重新生成分析',exact:true}).isVisible(),false);
  // A validated all-insufficient result remains an ordinary valid report.
  Object.assign(job,{status:'succeeded',stage:'completed',asr_status:'complete',report_status:'ready',report,retryable:false,updated_at:'ready'});
  await page.evaluate(()=>refreshJobs());
  assert.equal(await page.locator('#report article').count(),6);
  assert.equal(await page.locator('#download').isVisible(),true);
  // Existing gaps remain explicit even if text analysis itself is ready.
  Object.assign(job,{status:'partial',asr_status:'incomplete',retryable:true,updated_at:'gap'});
  await page.evaluate(()=>refreshJobs());
  assert.match(await page.locator('#job-guidance').textContent(),/不会补齐/);
  assert.match(await page.locator('#report').textContent(),/不能代表完整视频/);
  const before=JSON.stringify(job);
  accept=false;await page.getByRole('button',{name:'用原视频新建转写任务',exact:true}).click();
  await page.waitForTimeout(100);assert.equal(newCalls,0,'dismissed cost confirmation still dispatched');
  accept=true;await page.getByRole('button',{name:'用原视频新建转写任务',exact:true}).click();
  await page.waitForFunction(()=>selectedJob.id==='new-task');
  assert.equal(newCalls,1);assert.equal(JSON.stringify(job),before,'original task mutated');
  assert.match(dialogs.at(-1),/ASR 和文本模型费用/);
  assert.equal(await page.locator('#download').isVisible(),false);
  assert.equal(await page.locator('#report article').count(),0);
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  assert.deepEqual(errors,[]);
  const qa=path.resolve(__dirname,'../bin/qa');fs.mkdirSync(qa,{recursive:true});
  await page.screenshot({path:path.join(qa,'recovery-new-task-mobile.png'),fullPage:true});
  await page.evaluate(()=>selectJob('sample'));
  await page.screenshot({path:path.join(qa,'recovery-gap-mobile.png'),fullPage:true});
  console.log('PASS: partial failure, hidden fallback/download, preserved transcript, current metadata, invalid legacy version, text-only retry confirmation, legitimate insufficient result, ASR gap warning, new-task confirmation/selection, immutable source, mobile layout');
 }finally{if(browser)await browser.close();await new Promise(resolve=>server.close(resolve))}
})().catch(err=>{console.error(err);process.exitCode=1});
