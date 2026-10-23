import { useEffect,useRef } from 'react';
import { useAuth } from '../auth';
import { useGuide } from '../components/Onboarding';
import { Card } from '../components/ui';
import type { RoleCode } from '../data/authApi';
import { guideSteps } from '../data/guide';
import { useSessionTasks } from '../data/hooks';
import { roleLabel } from '../format';

export function HelpPage(){
  const heading=useRef<HTMLHeadingElement>(null);const {me}=useAuth();const {state,change}=useGuide();const loaded=useSessionTasks();
  const {role,index}=state;const sessionId=state.sessionId??'';
  const actual=me?.roles?.map(item=>item.role_code)??[];
  const roles:RoleCode[]=actual.includes('sys_admin')||!actual.length?['teacher','academic_admin','supervisor','sys_admin']:[...new Set(actual)];
  const selected=loaded.data.tasks.some(item=>item.session.id===sessionId)?sessionId:'';
  const effective=roles.includes(role)?role:roles[0]??'teacher';const steps=guideSteps(effective,selected||null);const at=Math.min(index,steps.length-1);const current=steps[at]!;
  useEffect(()=>{change({active:false});heading.current?.scrollIntoView?.({block:'start'});},[]);
  function select(next:number){change({role:effective,index:next,sessionId:sessionId||null,active:false,seen:true});}
  function visit(){change({role:effective,index:at,sessionId:selected||null,active:true,seen:true});window.location.hash=current.path;}
  return <div className="page help-page"><header className="page-head"><h1 ref={heading}>使用帮助</h1><a className="btn btn-secondary" href="#/sessions">返回课堂</a></header>
    <div className="help-controls"><label className="account-field"><span>我要完成的工作</span><select aria-label="引导角色" value={effective} onChange={event=>{const next=event.target.value as RoleCode;change({role:next,index:0,active:false});}}>{roles.map(item=><option key={item} value={item}>{roleLabel(item)}</option>)}</select></label>
      <label className="account-field"><span>本次课堂（可选）</span><select aria-label="引导课堂" value={selected} onChange={event=>{change({sessionId:event.target.value||null});}}><option value="">先从课堂列表选择</option>{loaded.data.tasks.map(({session})=><option key={session.id} value={session.id}>{session.title}</option>)}</select></label>
      {loaded.actionLabel&&<button type="button" className="btn btn-secondary btn-sm" onClick={loaded.loadMore}>{loaded.actionLabel}</button>}
      {loaded.error&&<p role="alert">{loaded.error}</p>}
    </div>
    <div className="help-layout"><nav aria-label="使用步骤"><ol>{steps.map((item,n)=><li key={item.id}><button type="button" aria-current={at===n?'step':undefined} onClick={()=>select(n)}><span>{n+1}</span>{item.title}</button></li>)}</ol></nav>
      <Card title={current.title}><p className="help-intro">{current.text}</p><ul className="help-checks">{current.checks.map(text=><li key={text}>{text}</li>)}</ul><div className="action-row"><button type="button" className="btn btn-primary" onClick={visit}>去操作</button><button type="button" className="btn btn-secondary" disabled={at===0} onClick={()=>select(at-1)}>上一步</button><button type="button" className="btn btn-secondary" onClick={()=>{if(at===steps.length-1){change({active:false,seen:true,index:0});window.location.hash='#/sessions';}else select(at+1);}}>{at===steps.length-1?'结束引导':'下一步'}</button></div></Card>
    </div>
    <details className="help-reference"><summary>手机录制与常见问题</summary><h3>手机实时录制</h3><p>在课堂详情打开“实时课堂”，选择已核验的来源并创建采集，再生成手机配对链接。手机与服务器需可互访，链接必须使用可信HTTPS；127.0.0.1只指向当前设备。</p><p>手机允许摄像头和麦克风后开始录制。结束时明确点击“停止录像”，等待录像就绪，再选择分析。断开观看不会停止服务端录像。服务未配置时请联系管理员。</p><h3>没有可用来源或按钮无法点击</h3><p>先检查是否选对课堂和录像，再检查来源是否核验、是否允许所需用途、是否过期，以及当前账号是否有权限。页面出现具体错误时按提示处理；没有权限时联系教务或管理员。</p><h3>看不到转写或报告</h3><p>举手与低头识别不会生成语音文字和教学报告。需要提交语音转写与教学报告，并在完成后切换对应标签；未配置模型、预算不足或来源不允许云端处理时不能提交。</p><h3>报告与画面如何对应</h3><p>每份结果绑定自己的录像和分析记录。核对结果上方的文件名；回放选择另一段录像时，不会自动替换已有报告。只根据实际发送的材料分析，短片结论不代表整节课。</p></details>
  </div>;
}
