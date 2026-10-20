$('revision-reason').addEventListener('input',()=>{dirtyEditor=true});
const historyRoot=$('job-history');historyRoot.className='job-history';
let selectedJob=null,knownJobs=[],pollTimer=null,pollBusy=false,actionBusy=false,selectionSerial=0,viewVersion='current',renderStamp='',editorStamp='',dirtyEditor=false,editorBaseRevision=0,editorBaseSegments=[],runtimeInfo=null;
const stageNames={queued:'等待处理',starting:'准备处理',probe:'提取音频',asr:'语音转写',asr_complete:'转写完成',reading:'阅读整理',events:'教学事件',report:'六维分析',audit:'全文复核',completed:'处理完成',partial:'部分完成',failed:'处理失败',cancelled:'已取消'};
const controls=document.createElement('div');controls.className='task-controls';$('error-details').after(controls);
const cancelButton=add('button','取消任务',controls);cancelButton.type='button';
const retryButton=add('button','确认后重试',controls);retryButton.type='button';
const retranscribeButton=add('button','用原视频新建转写任务',controls);retranscribeButton.type='button';
const reanalyzeButton=add('button','重新生成六维分析',controls);reanalyzeButton.type='button';
const versionLabel=add('label','查看版本 ',controls);const versionSelect=document.createElement('select');versionLabel.append(versionSelect);
const versionNote=add('small','',controls);
const revisionPanel=$('revision-panel');$('transcript').after(revisionPanel);

async function jobRequest(url,options={}){const r=await fetch(url,options);const data=await r.json();if(!r.ok)throw Error(data.error||'任务请求失败');return data}
function jobLabel(j){return (j.video_name||j.id)+' · '+({queued:'排队中',running:'处理中',succeeded:'已完成',partial:'部分完成',failed:'失败',cancelled:'已取消'}[j.status]||'待处理')}
function renderHistory(){historyRoot.replaceChildren();if(!knownJobs.length)add('small','上传视频后，任务会保存在这里。',historyRoot);for(const j of knownJobs){const b=add('button',jobLabel(j),historyRoot);b.type='button';b.setAttribute('aria-pressed',String(selectedJob&&selectedJob.id===j.id));b.onclick=()=>selectJob(j.id)}}
function saveSelection(id){try{localStorage.setItem('classroom-demo-job',id)}catch{}}
function loadSelection(){try{return localStorage.getItem('classroom-demo-job')}catch{return null}}
function canLeaveEditor(){return !dirtyEditor||confirm('你有尚未保存的转写修改，确定离开吗？')}
async function selectJob(id){if(!canLeaveEditor())return;const serial=++selectionSerial;try{const {job}=await jobRequest('/api/jobs/'+encodeURIComponent(id));if(serial!==selectionSerial)return;selectedJob=job;saveSelection(id);viewVersion='current';renderStamp='';editorStamp='';dirtyEditor=false;revisionPanel.open=false;$('revision-reason').value='';$('video').src='/api/jobs/'+encodeURIComponent(id)+'/media';renderHistory();renderJob(job);startPolling()}catch(e){$('job-state').textContent=e.message}}
function resetDisplay(){result=null;renderTranscript([]);renderEvents(null);$('report').replaceChildren();$('download').hidden=true}
function rebuildVersions(job){const selected=viewVersion;versionSelect.replaceChildren();const current=add('option','当前结果（转写第 '+job.revision_no+' 版）',versionSelect);current.value='current';if(job.revision_no>0){const orig=add('option','原始 ASR 转写（第 1 版）',versionSelect);orig.value='original'}for(const v of job.report_versions||[]){const o=add('option','报告第 '+v.version+' 版 · 转写第 '+v.input_revision_no+' 版'+(v.report_status!=='ready'?' · 未通过校验':''),versionSelect);o.value=String(v.version)}versionSelect.value=selected;versionLabel.hidden=!job.revision_no}
function renderAnalysis(data){
 result=data;renderTranscript(data.segments||[]);if(data.reading&&data.reading.paragraphs)setReading(data.reading);renderEvents(data.events);
 const root=$('report');root.replaceChildren();$('download').hidden=true;
 for(const warning of data.warnings||[]){const box=add('p','部分结果提示：'+friendlyDiagnostic(warning,data.events?.events,data.segments),root);box.className='result-warning'}
 if(data.report_status==='failed'){add('p','分析未完成：模型请求或报告校验未通过，具体原因见错误详情。已有转写、阅读整理和有效事件仍可查看；这不是正式六维报告。',root);return}
 if(data.report_status!=='ready'||!data.report||!data.report.dimensions)return;
 if(data.asr_status!=='complete')add('p',data.asr_status==='incomplete'?'转写存在已知缺口：本报告仅分析现有文字，不能代表完整视频。':'转写完整性未确认：本报告仅分析所保存的文字。',root);
  renderReport({...data,runtime_info:runtimeInfo},root);
  wireReview(data,root);
 $('download').hidden=false;
}
function renderJob(job){const recovery=recoveryView(job);
 $('job-state').textContent=recovery.statusText||`${stageNames[job.stage]||'处理中'} · ${job.progress}%`;
 $('job-progress').value=job.progress||0;$('status').hidden=true;
 $('job-guidance').hidden=!recovery.showGuidance;
 $('job-guidance').textContent=recovery.guidance;
 $('error-details').hidden=!job.error;$('job-error').textContent=friendlyDiagnostic(job.error,job.events?.events,job.segments);
 retryButton.textContent=recovery.retryLabel;
 cancelButton.hidden=!['queued','running'].includes(job.status);retryButton.hidden=!recovery.retry;retranscribeButton.hidden=!recovery.retranscribe;reanalyzeButton.hidden=!recovery.reanalyze;rebuildVersions(job);
 // Review saves change updated_at but must preserve open evidence and other review drafts.
 const stamp=job.id+'|'+viewVersion+'|'+JSON.stringify([job.revision_no,job.input_revision_no,job.report_version,job.report_status,job.asr_status,job.segments,job.reading,job.events,job.report,job.warnings,job.review_targets,(job.report_versions||[]).map(v=>[v.version,v.report_status]),job.event_request,job.text_request]);if(stamp===renderStamp)return;renderStamp=stamp;
 if(viewVersion==='original'){revisionPanel.hidden=true;return}
 const historical=viewVersion!=='current';const v=historical?(job.report_versions||[]).find(x=>String(x.version)===viewVersion):null;
 const data=v?{...v,job_id:job.id,report_version:v.version,rubric_version:v.rubric_version,review_targets:job.review_targets}: {segments:job.segments||[],reading:job.reading,events:job.events,report:job.report,report_status:job.report_status,asr_status:job.asr_status,warnings:job.warnings||[],job_id:job.id,input_revision_no:job.input_revision_no||job.revision_no,report_version:job.report_version,review_targets:job.review_targets,event_request:job.event_request,text_request:job.text_request,audio_chunks:job.audio_chunks,lesson_coverage:job.lesson_coverage,rubric_version:'six-dim-text-v1',timestamp_precision:'音频块级时间，非逐词对齐'};
 resetDisplay();renderAnalysis(data);versionNote.textContent=historical?'正在查看历史报告；引用来自该报告使用的转写版本。':'当前结果；原始转写保留，修改后只重新调用文本模型。';
 const windows=data.lesson_coverage?.windows;if(data.report_status==='ready'&&windows?.length>1)versionNote.textContent+=` 本次报告已结合全部转写（${clock(windows[0].start_ms)}–${clock(windows.at(-1).end_ms)}），不代表上传素材覆盖整节课。`;
 revisionPanel.hidden=historical||!job.revision_no||['queued','running'].includes(job.status);if(!revisionPanel.hidden)populateRevisionEditor(job)
}
function populateRevisionEditor(job){$('revision-number').textContent=`当前转写第 ${job.revision_no} 版。只能修改文字和标点；保存后保留原版，重新生成阅读版、事件和报告。`;const stamp=job.id+'|'+job.revision_no;if(editorStamp===stamp)return;if(dirtyEditor&&editorStamp.startsWith(job.id+'|')){$('revision-number').textContent='服务器已有新转写版本，已保留你的未保存文字；保存时会校验父版本。请先复制修改内容，再重新选择当前任务。';return}editorStamp=stamp;editorBaseRevision=job.revision_no;editorBaseSegments=(job.segments||[]).map(s=>({...s}));dirtyEditor=false;const root=$('revision-editor');root.replaceChildren();for(const s of job.segments||[]){const row=add('label','',root);row.className='revision-row';add('span',`原文片段 · ${clock(s.start_ms)}–${clock(s.end_ms)}`,row);const area=document.createElement('textarea');area.value=s.text;area.dataset.segmentId=s.id;area.rows=3;area.oninput=()=>{dirtyEditor=true;sizeEditor(area)};row.append(area)}}
function sizeEditor(area){area.style.height='auto';area.style.height=Math.max(80,area.scrollHeight+2)+'px'}
revisionPanel.addEventListener('toggle',()=>{if(revisionPanel.open)document.querySelectorAll('#revision-editor textarea').forEach(sizeEditor)});
versionSelect.onchange=async()=>{if(!selectedJob)return;if(!canLeaveEditor()){versionSelect.value=viewVersion;return}dirtyEditor=false;viewVersion=versionSelect.value;renderStamp='';if(viewVersion==='original'){const serial=selectionSerial,id=selectedJob.id;try{const {revisions}=await jobRequest('/api/jobs/'+id+'/revisions');if(serial!==selectionSerial||viewVersion!=='original')return;const first=revisions.find(x=>x.revision_no===1);resetDisplay();renderAnalysis({segments:first?.segments||[],report:null});versionNote.textContent='原始 ASR 转写，未应用人工修订；报告请从上方选择对应版本。';revisionPanel.hidden=true}catch(e){$('job-state').textContent=e.message}}else renderJob(selectedJob)};
$('save-revision').onclick=async()=>{if(!selectedJob)return;const reason=$('revision-reason').value.trim();if(reason.length<2){$('job-state').textContent='请填写至少两个字的修订原因';return}const id=selectedJob.id,serial=selectionSerial;const segments=[...document.querySelectorAll('#revision-editor textarea')].map((t,i)=>({...editorBaseSegments[i],text:t.value}));$('save-revision').disabled=true;try{const {job}=await jobRequest(`/api/jobs/${id}/revisions`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({parent_revision_no:editorBaseRevision,reason,segments})});if(serial!==selectionSerial)return;dirtyEditor=false;editorStamp='';$('revision-reason').value='';selectedJob=job;renderJob(job);startPolling()}catch(e){$('job-state').textContent=e.message}finally{$('save-revision').disabled=false}};
async function jobAction(action){if(!selectedJob||actionBusy)return;const serial=selectionSerial,id=selectedJob.id;actionBusy=true;for(const b of [cancelButton,retryButton,retranscribeButton,reanalyzeButton])b.disabled=true;try{const {job}=await jobRequest(`/api/jobs/${id}/${action}`,{method:'POST'});if(serial!==selectionSerial)return;if(action==='retranscribe'){dirtyEditor=false;knownJobs.unshift(job);await selectJob(job.id)}else{viewVersion='current';selectedJob=job;renderJob(job);startPolling()}}catch(e){$('job-state').textContent=e.message}finally{actionBusy=false;for(const b of [cancelButton,retryButton,retranscribeButton,reanalyzeButton])b.disabled=false}}
cancelButton.onclick=()=>jobAction('cancel');
reanalyzeButton.onclick=()=>{if(!selectedJob||!canLeaveEditor())return;if(confirm('使用现有转写重新生成六维分析，不调用 ASR；历史报告与原始转写均保留。将产生新的文本模型费用，且不会补齐缺失转写。是否继续？')){dirtyEditor=false;editorStamp='';jobAction('reanalyze')}};
retryButton.onclick=()=>{if(!selectedJob||!canLeaveEditor())return;const message=selectedJob.revision_no>0?'只重新运行阅读整理、教学事件和六维分析，不调用 ASR，也不会补齐缺失转写。可能产生新的文本模型费用；之前失败的请求可能已计费。是否继续？':'从未完成的音频块继续转写，成功的检查点会复用。可能产生模型费用；之前失败的请求可能已计费。是否继续？';if(confirm(message)){dirtyEditor=false;editorStamp='';jobAction('retry')}};
retranscribeButton.onclick=()=>{if(!selectedJob||!canLeaveEditor())return;if(confirm('将复制保存的原视频，新建独立任务并重新转写整个视频，再运行文本分析及全文复核（启用时）。会产生新的 ASR 和文本模型费用；原任务、人工修订和历史报告均保留。是否继续？'))jobAction('retranscribe')};
async function refreshJobs(){if(pollBusy)return;pollBusy=true;const serial=selectionSerial;try{const {jobs}=await jobRequest('/api/jobs');knownJobs=jobs;renderHistory();if(serial!==selectionSerial)return;if(selectedJob){const found=jobs.find(j=>j.id===selectedJob.id);if(found){selectedJob=found;renderJob(found)}}else{const id=loadSelection();const j=jobs.find(x=>x.id===id)||jobs[0];if(j)await selectJob(j.id)}}catch(e){$('job-state').textContent=e.message}finally{pollBusy=false}}
function startPolling(){if(pollTimer)clearInterval(pollTimer);pollTimer=setInterval(refreshJobs,2000)}
$('form').onsubmit=async e=>{e.preventDefault();if(!canLeaveEditor())return;const fd=new FormData(e.target);if(fd.get('consent')!=='yes'){$('status').hidden=false;$('status').textContent='请先确认素材上传授权和可能费用';return}const file=fd.get('video');if(file&&file.size>2*1024**3){$('status').hidden=false;$('status').textContent='文件超过2GB，请压缩视频后上传';return}const button=$('form').querySelector('button');button.disabled=true;$('status').hidden=false;$('status').textContent='正在上传视频，完成后会显示分析进度。长视频请保持页面打开，等待上传完成。';try{const {job}=await jobRequest('/api/jobs',{method:'POST',body:fd});$('status').hidden=true;dirtyEditor=false;knownJobs.unshift(job);await selectJob(job.id)}catch(err){$('status').hidden=false;$('status').textContent=err.message}finally{button.disabled=false}};
window.addEventListener('beforeunload',e=>{if(dirtyEditor){e.preventDefault();e.returnValue=''}});
async function initializeJobs(){
 try{runtimeInfo=await jobRequest('/api/health')}catch{/* Version information is optional; saved reports remain readable. */}
 await refreshJobs();startPolling();
}
initializeJobs();
