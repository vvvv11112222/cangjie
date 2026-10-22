import { useEffect, useState } from 'react';
import { useAuth } from '../auth';
import { roleLabel } from '../format';
import type { Session } from '../types';
import { sessionDetailHash } from '../data/route';
import { Card } from './ui';

export const FIRST_STEPS = ['选择课堂', '上传与登记来源', '核验用途', '选择分析', '查看与核对结果', '复核发布报告'];
export function FirstUseGuide({ onUpload, sessions = [] }: { onUpload?(): void; sessions?: readonly Session[] }) {
  const [mobile, setMobile] = useState(false);
  const [selected, setSelected] = useState('');
  const [open, setOpen] = useState(sessions.length === 0 || (typeof window !== 'undefined' && new URLSearchParams(window.location.hash.split('?')[1] ?? '').get('help') === '1'));
  useEffect(()=>{
    const reveal=()=>{if(new URLSearchParams(window.location.hash.split('?')[1] ?? '').get('help')==='1')setOpen(true);};
    window.addEventListener('hashchange',reveal);return ()=>window.removeEventListener('hashchange',reveal);
  },[]);
  const usable = sessions.filter(session => session.allowed_actions.includes('upload'));
  return <details className="first-use-guide" open={open} onToggle={event=>setOpen(event.currentTarget.open)}><summary>使用帮助 · 从上传录像到查看报告</summary><Card title="第一次使用，从这里开始" subtitle="上传成功后还要选择分析方式；第一次可先用一段声音清晰的短录像。">
    <p>课程是一门科目；开课记录关联本学期的教师与班级；课堂是其中某一次上课。录像来源用于说明是谁提供录像、允许哪些用途。</p>
    <ol className="workflow-steps" aria-label="首次使用流程">{FIRST_STEPS.map(step => <li key={step}>{step}</li>)}</ol>
    <div className="action-row">
      {onUpload ? <button type="button" className="btn btn-primary" onClick={onUpload}>上传录像并分析</button> : <a className="btn btn-primary" href="#/sessions?start=upload">上传录像并分析</a>}
      <button type="button" className="btn btn-secondary" aria-expanded={mobile} onClick={() => setMobile(value => !value)}>使用手机录制</button>
    </div>
    {mobile && <div className="onboarding-next"><p>手机录制从一节具体课堂发起。先选择课堂，再检查网络、可信 HTTPS 和摄像头、麦克风权限。</p>{usable.length ? <><label className="account-field"><span>准备录制的课堂</span><select value={selected} onChange={event => setSelected(event.target.value)}><option value="">请选择课堂</option>{usable.map(session => <option key={session.id} value={session.id}>{session.title}</option>)}</select></label>{selected && <a className="btn btn-primary" href={sessionDetailHash(selected, { tab: 'live' })}>前往手机录制</a>}</> : <a href="#/sessions">进入课堂档案选择或创建课堂；没有权限时请联系学院教务或系统管理员。</a>}</div>}
    <details className="field-hint"><summary>这些步骤分别由谁完成？</summary><p>任课教师或获授权人员上传和分析；学院教务或系统管理员按实际许可核验来源；具备报告复核、发布权限的人员核对并发布。具体操作以当前课堂授权为准。</p></details>
    <details className="field-hint"><summary>两种分析怎么选？结果在哪里？</summary><p>“举手与低头识别”在本机运行，结果在课堂详情的“视觉观察”，可播放带框录像、查看所有人物或单个人。</p><p>“语音转写与教学报告”需要管理员配置模型和预算，且录像来源允许云端处理。“转写”显示带时间的文字，“报告”显示综合分析。先完成本录像的视觉分析，再生成报告，可以一起采用举手、低头结果。</p><p>上传后如果提示等待核验，请联系教务或管理员核验“素材与来源”；不要把未获准用途勾选为已获准。演示课堂与固定样例仅供熟悉操作，不代表真实检测结果。</p></details>
  </Card></details>;
}
export function RoleGuide() {
  const { me } = useAuth();
  if (me === null) return null;
  const explanations = { sys_admin: '管理全校组织、账号、课堂及运行配置；负责处理服务未启用和预算问题。', academic_admin: '维护授权学院的教务、账号与来源核验；录像、转写和报告内容需由任课教师、授权督导或系统管理员查看。', supervisor: '查看授权学院范围内的课堂，按课堂授权处理分析与报告复核。', teacher: '查看和处理本人任课课堂；范围或许可不足时联系学院教务或系统管理员。' };
  return <details className="role-guide"><summary>当前身份：{me.roles.map(role => roleLabel(role.role_code)).join('、')} · 查看工作范围与求助方式</summary>{me.roles.map(role => <p key={role.id}>{explanations[role.role_code]}</p>)}</details>;
}
