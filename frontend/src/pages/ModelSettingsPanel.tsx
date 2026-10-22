import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { ApiError, describeError } from '../data/errors';
import { getModelSettings, updateModelSettings } from '../data/modelSettings';
import type { ModelSettings, ModelValues } from '../data/modelSettings';
import { Card, TechDetails } from '../components/ui';

export function ModelSettingsPanel({onApplied}:{onApplied?():void}) {
 const [view,setView]=useState<ModelSettings|null>(null);
 const [values,setValues]=useState<ModelValues|null>(null);
 const [revision,setRevision]=useState('');
 const [asrKey,setASRKey]=useState(''),[reportKey,setReportKey]=useState('');
 const [clearASR,setClearASR]=useState(false),[clearReport,setClearReport]=useState(false);
 const [confirmed,setConfirmed]=useState(false),[reason,setReason]=useState('');
 const [busy,setBusy]=useState(false),[error,setError]=useState<string|null>(null),[message,setMessage]=useState<string|null>(null);
 const [refresh,setRefresh]=useState(0);
 const alive=useRef(true),dirty=useRef(false),saving=useRef(false);
 const epoch=useRef(0);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;};},[]);
 useEffect(()=>{
  let active=true,inFlight=false,stopped=false;
  async function load(){
   if(inFlight||saving.current||stopped)return;inFlight=true;const generation=epoch.current;
   try{const next=await getModelSettings();if(active&&generation===epoch.current){setView(next);setError(null);if(!dirty.current){setValues(next.values);setRevision(next.revision);}}}
   catch(cause){if(cause instanceof ApiError&&[401,403,410].includes(cause.status))stopped=true;if(active&&generation===epoch.current)setError(describeError(cause));}
   finally{inFlight=false;}
  }
  void load();const timer=window.setInterval(()=>{if(document.visibilityState!=='hidden')void load();},15000);
  return()=>{active=false;window.clearInterval(timer);};
 },[refresh]);
 function change<K extends keyof ModelValues>(key:K,value:ModelValues[K]){dirty.current=true;setValues(current=>current ? {...current,[key]:value}:current);setMessage(null);}
 async function submit(event:FormEvent){
  event.preventDefault();if(!values||!view||busy||!view.can_apply)return;
  if(!reason.trim()){setError('请填写本次配置变更说明。');return;}
  if(values.enabled&&!confirmed){setError('请先核对语音、报告模型的实际单价与共享预算，再勾选确认。');return;}
  epoch.current+=1;saving.current=true;setBusy(true);setError(null);setMessage(null);
  try{
   const next=await updateModelSettings({expected_revision:revision,values,pricing_confirmed:confirmed,reason:reason.trim(),...(clearASR ? {asr_api_key:''}:asrKey ? {asr_api_key:asrKey}:{}),...(clearReport ? {report_api_key:''}:reportKey ? {report_api_key:reportKey}:{})});
   if(!alive.current)return;
   dirty.current=false;setView(next);setValues(next.values);setRevision(next.revision);setASRKey('');setReportKey('');setClearASR(false);setClearReport(false);setReason('');setConfirmed(false);
   setMessage(next.revision===next.active_revision ? '配置已保存并生效，后续新分析使用此配置；历史分析保持原版本。尚未进行云端连通性测试。':'配置已保存，但当前进程未确认应用。请重新读取状态，再处理后续任务。');onApplied?.();
  }catch(cause){if(alive.current)setError(cause instanceof ApiError&&[400,409].includes(cause.status) ? cause.message : describeError(cause));}
  finally{saving.current=false;if(alive.current)setBusy(false);}
 }
 const textField=(key:keyof ModelValues,label:string,options:{numeric?:boolean;url?:boolean;hint?:string}={})=><label className="account-field" key={key}><span>{label}</span><input aria-label={label} value={String(values?.[key]??'')} type={options.url?'url':options.numeric?'number':'text'} min={options.numeric?1:undefined} step={options.numeric?1:undefined} maxLength={options.numeric?undefined:2048} onChange={event=>change(key,options.numeric?Number(event.target.value):event.target.value)} />{options.hint&&<small className="field-hint">{options.hint}</small>}</label>;
 const keyField=(kind:'asr'|'report')=>{
  const isASR=kind==='asr',configured=isASR?view?.asr_key_configured:view?.report_key_configured;
  return <div className="model-key-field"><label className="account-field"><span>{isASR?'语音':'报告'} API Key</span><input type="password" autoComplete="new-password" maxLength={4096} value={isASR?asrKey:reportKey} disabled={isASR?clearASR:clearReport} placeholder={configured?'已设置；留空保留现有密钥':'尚未设置，请输入密钥'} onChange={event=>{dirty.current=true;(isASR?setASRKey:setReportKey)(event.target.value);}} /></label><p className="field-hint">{configured?'已有密钥已保存，不会返回到浏览器。':'该模型尚未配置密钥。'}接口地址变更时需重新填写密钥。</p>{configured&&<label className="check-row"><input type="checkbox" checked={isASR?clearASR:clearReport} onChange={event=>{dirty.current=true;(isASR?setClearASR:setClearReport)(event.target.checked);}}/>清除已有{isASR?'语音':'报告'}密钥（需同时关闭云端分析）</label>}</div>;
 };
 return <Card title="语音与报告模型配置" subtitle="仅系统管理员可修改，沿用当前百炼兼容接口。空闲时保存即可应用，无需手动改文件或重启。">
  <p className="field-hint">保存配置不会请求云端模型。单价与预算以人民币填写，两个模型共用月预算；报告使用系统内置六维教学分析模板。</p>
  <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={()=>setRefresh(value=>value+1)}>重新读取配置状态</button>
  {view&&!view.can_apply&&<p className="notice-strip" role="status">存在排队、运行中的分析或待核对账单。可以填写配置，处理完这些任务或账单后再保存应用。</p>}
  {view&&revision!==view.revision&&<p className="notice-strip">配置已被其他管理员更新。你填写的内容仍保留；点击“放弃本次编辑并读取最新配置”后重新核对。</p>}
  {view&&dirty.current&&<button type="button" className="text-button" disabled={busy} onClick={()=>{dirty.current=false;setValues(view.values);setRevision(view.revision);setASRKey('');setReportKey('');setClearASR(false);setClearReport(false);setConfirmed(false);setReason('');setMessage(null);}}>放弃本次编辑并读取最新配置</button>}
  {!values&&!error&&<p className="load-note">正在读取模型配置…</p>}
  {values&&<form onSubmit={event=>void submit(event)}><fieldset disabled={busy} className="model-settings-fields"><legend>模型与预算</legend>
   <label className="check-row"><input type="checkbox" checked={values.enabled} onChange={event=>change('enabled',event.target.checked)}/>启用云端语音转写与教学报告</label>
   <div className="model-settings-grid"><section><h3>语音转写</h3>{textField('asr_api_base','语音接口基础地址',{url:true,hint:'填写兼容接口的基础地址，不包含 /chat/completions、密钥或查询参数。'})}{textField('asr_model','语音模型名称')}{textField('asr_revision','语音模型版本 / 修订标识',{hint:'按供应商实际标识填写；使用可变别名时在变更说明中注明。'})}{keyField('asr')}{textField('asr_language_hints','语音语言提示',{hint:'例如 zh,en；按模型支持范围填写，可留空。'})}{textField('asr_input_cny_per_million_tokens','语音输入单价（元 / 百万 token）')}{textField('asr_output_cny_per_million_tokens','语音输出单价（元 / 百万 token）')}</section>
   <section><h3>教学报告</h3>{textField('report_api_base','报告接口基础地址',{url:true})}{textField('report_model','报告模型名称')}{textField('report_revision','报告模型版本 / 修订标识')}{keyField('report')}{textField('report_input_cny_per_million_tokens','报告输入单价（元 / 百万 token）')}{textField('report_output_cny_per_million_tokens','报告输出单价（元 / 百万 token）')}</section></div>
   <div className="model-settings-grid">{textField('monthly_budget_cny','语音与报告共享月预算（元）')}{textField('price_version','价格版本标识',{hint:'例如按实际核对日期及计费档位命名。'})}</div>
   <label className="account-field"><span>价格来源与计费档位说明</span><textarea maxLength={2048} value={values.price_source} onChange={event=>change('price_source',event.target.value)} /></label>
   <details><summary>单次请求 token 上限</summary><p className="field-hint">这些上限参与单次费用预留。月预算需要覆盖至少一次语音与一次报告请求。</p><div className="model-settings-grid">{textField('asr_max_input_tokens','语音输入 token 上限',{numeric:true})}{textField('asr_max_output_tokens','语音输出 token 上限',{numeric:true})}{textField('report_max_input_tokens','报告输入 token 上限',{numeric:true})}{textField('report_max_output_tokens','报告输出 token 上限',{numeric:true})}</div></details>
   <label className="check-row"><input type="checkbox" checked={confirmed} onChange={event=>{dirty.current=true;setConfirmed(event.target.checked);}}/>已核对同地域、同模型和相应档位的实际 token 单价及共享月预算</label>
   <label className="account-field"><span>本次配置变更说明</span><textarea value={reason} maxLength={1000} onChange={event=>{dirty.current=true;setReason(event.target.value);}} placeholder="记录配置目的、实际模型版本或别名限制；不要填写密钥。" required /></label>
   <button type="submit" className="btn btn-primary" disabled={busy||!view?.can_apply}>{busy?'正在保存并应用…':'保存并应用模型配置'}</button>
  </fieldset></form>}
  {error&&<p role="alert" className="notice-strip load-note-error">{error}</p>}{message&&<p role="status" className="notice-strip">{message}</p>}
  {view&&<TechDetails summary="配置版本"><p>已保存版本：{view.revision}；当前进程版本：{view.active_revision}。</p></TechDetails>}
 </Card>;
}
