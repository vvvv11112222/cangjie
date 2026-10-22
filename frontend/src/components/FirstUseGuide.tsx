import { useState } from 'react';
import { useAuth } from '../auth';
import { roleLabel } from '../format';
import type { Session } from '../types';
import { sessionDetailHash } from '../data/route';
import { Card } from './ui';

export const FIRST_STEPS = ['选择课堂', '上传与登记来源', '核验用途', '选择分析', '查看与核对结果', '复核发布报告'];
export function FirstUseGuide({ onUpload, sessions = [] }: { onUpload?(): void; sessions?: readonly Session[] }) {
  const [mobile, setMobile] = useState(false);
  const [selected, setSelected] = useState('');
  const usable = sessions.filter(session => session.allowed_actions.includes('upload'));
  return <Card title="第一次使用，从这里开始" subtitle="上传成功后还要选择分析方式；第一次可先用一段声音清晰的短录像。">
    <ol className="workflow-steps" aria-label="首次使用流程">{FIRST_STEPS.map(step => <li key={step}>{step}</li>)}</ol>
    <div className="action-row">
      {onUpload ? <button type="button" className="btn btn-primary" onClick={onUpload}>上传录像并分析</button> : <a className="btn btn-primary" href="#/sessions?start=upload">上传录像并分析</a>}
      <button type="button" className="btn btn-secondary" aria-expanded={mobile} onClick={() => setMobile(value => !value)}>使用手机录制</button>
    </div>
    {mobile && <div className="onboarding-next"><p>手机录制从一节具体课堂发起。先选择课堂，再检查网络、可信 HTTPS 和摄像头、麦克风权限。</p>{usable.length ? <><label className="account-field"><span>准备录制的课堂</span><select value={selected} onChange={event => setSelected(event.target.value)}><option value="">请选择课堂</option>{usable.map(session => <option key={session.id} value={session.id}>{session.title}</option>)}</select></label>{selected && <a className="btn btn-primary" href={sessionDetailHash(selected, { tab: 'live' })}>前往手机录制</a>}</> : <a href="#/sessions">进入课堂档案选择或创建课堂；没有权限时请联系学院教务或系统管理员。</a>}</div>}
    <details className="field-hint"><summary>这些步骤分别由谁完成？</summary><p>任课教师或获授权人员上传和分析；学院教务或系统管理员按实际许可核验来源；具备报告复核、发布权限的人员核对并发布。具体操作以当前课堂授权为准。</p></details>
  </Card>;
}
export function RoleGuide() {
  const { me } = useAuth();
  if (me === null) return null;
  const explanations = { sys_admin: '管理全校组织、账号、课堂及运行配置；负责处理服务未启用和预算问题。', academic_admin: '维护授权学院的教务、账号与来源核验；录像、转写和报告内容需由任课教师、授权督导或系统管理员查看。', supervisor: '查看授权学院范围内的课堂，按课堂授权处理分析与报告复核。', teacher: '查看和处理本人任课课堂；范围或许可不足时联系学院教务或系统管理员。' };
  return <details className="role-guide"><summary>当前身份：{me.roles.map(role => roleLabel(role.role_code)).join('、')} · 查看工作范围与求助方式</summary>{me.roles.map(role => <p key={role.id}>{explanations[role.role_code]}</p>)}</details>;
}
