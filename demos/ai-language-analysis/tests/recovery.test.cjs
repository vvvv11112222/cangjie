const {test}=require('node:test');
const assert=require('node:assert/strict');
const {recoveryView}=require('../web/recovery.js');
test('完整转写的分析失败可仅重跑文本',()=>{
 const v=recoveryView({status:'partial',asr_status:'complete',report_status:'failed',revision_no:1,retryable:true});
 assert.equal(v.retry,true);assert.equal(v.retranscribe,false);assert.equal(v.showGuidance,true);
 assert.match(v.guidance,/不需要再次上传/);assert.equal(v.retryLabel,'重新生成分析');
});
test('旧缺口和未知完整性明确区分，不承诺文本重跑补齐',()=>{
 for(const status of ['incomplete','unknown']){
  const v=recoveryView({status:'partial',asr_status:status,revision_no:2,retryable:true});
  assert.equal(v.retry,true);assert.equal(v.retranscribe,true);assert.equal(v.retryLabel,'重新分析现有文字');
  assert.match(v.guidance,status==='incomplete'?/不会补齐/:/无法确认/);
 }
});
test('运行中禁止恢复，ASR 未完成可续跑',()=>{
 for(const status of ['queued','running']){
  const v=recoveryView({status,asr_status:'incomplete',revision_no:1,retryable:true});
  assert.equal(v.retry,false);assert.equal(v.retranscribe,false);
 }
 const v=recoveryView({status:'failed',asr_status:'incomplete',revision_no:0,retryable:true});
 assert.equal(v.retry,true);assert.equal(v.retranscribe,false);assert.equal(v.retryLabel,'继续转写');
});
test('合法证据不足报告不会仅因警告被标为失败',()=>{
 const v=recoveryView({status:'succeeded',asr_status:'complete',report_status:'ready',revision_no:1,warnings:['样本短'],report:{dimensions:Array(6).fill({coverage_status:'insufficient'})}});
 assert.equal(v.retry,false);assert.equal(v.showGuidance,false);assert.equal(v.statusText,null);
});
test('旧检查点无原始版本时拒绝直接续跑，提供独立任务',()=>{
 const v=recoveryView({status:'failed',asr_status:'unknown',revision_no:0,retryable:true,completed_asr_chunks:[0]});
 assert.equal(v.retry,false);assert.equal(v.retranscribe,true);assert.match(v.guidance,/不能安全续跑/);
});
