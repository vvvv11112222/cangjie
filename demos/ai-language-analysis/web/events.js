const eventsRoot=document.createElement('section');eventsRoot.id='events-section';eventsRoot.hidden=true;
const eventsDetails=add('details','',eventsRoot);
const eventsHeading=add('summary','查看课堂依据与原文',eventsDetails);
add('p','这里是结论的素材，不是优点清单。原文保持录入语言；回看时间是音频块范围，不是句子的精确起止时间。',eventsDetails).className='evidence-help';
const eventsList=add('div','',eventsDetails);$('report').after(eventsRoot);
let displayedEventsKey='';
function renderEvents(set){
 const segments=result?.segments||[],events=set?.events||[];
 const key=(result?.job_id||'')+'|'+(result?.report_version||result?.input_revision_no||'')+'|'+JSON.stringify(events);
 if(key!==displayedEventsKey)eventsDetails.open=false;displayedEventsKey=key;
 eventsList.replaceChildren();eventsRoot.hidden=!events.length;if(eventsRoot.hidden)return;
 eventsHeading.textContent='查看课堂依据与原文（'+events.length+' 处）';
 const ordered=[...events].sort((a,b)=>(evidenceRanges(a.quotes,segments)[0]?.start_ms??Infinity)-(evidenceRanges(b.quotes,segments)[0]?.start_ms??Infinity));
 for(const e of ordered){
  const a=add('article','',eventsList);a.className='event-card';a.id='teaching-'+e.id;a.tabIndex=-1;
  add('h3',evidenceLabel(e,segments),a);add('p',e.summary,a);
  if(e.uncertainty)add('small','待复核：'+e.uncertainty,a);
  const refs=add('div','',a),sources=new Map(segments.map(s=>[s.id,s]));
  const quotes=[...(e.quotes||[])].sort((left,right)=>(sources.get(left.segment_id)?.start_ms??Infinity)-(sources.get(right.segment_id)?.start_ms??Infinity));
  for(const q of quotes)if(sources.has(q.segment_id))add('blockquote',q.text,refs);
  const ranges=evidenceRanges(quotes,segments);
  for(const s of ranges){const b=add('button','回看 '+clock(s.start_ms)+'–'+clock(s.end_ms),refs);b.type='button';b.title='回看原文对应音频块';b.onclick=()=>{$('video').scrollIntoView({block:'center'});$('video').currentTime=s.start_ms/1000;$('video').play().catch(()=>{})}}
 }
}
function showTeachingEvidence(id){
 const target=document.getElementById('teaching-'+id);if(!target)return;
 eventsDetails.open=true;target.focus({preventScroll:true});target.scrollIntoView({block:'center'});
 const event=result?.events?.events?.find(e=>e.id===id);
 const source=evidenceRanges(event?.quotes,result?.segments)[0];
 if(source){$('video').currentTime=source.start_ms/1000;$('video').play().catch(()=>{})}
}
