import type { Report } from '../types';

export function reportNextStep(report: Report): { index:number; text:string; action:string|null } {
  if (report.status==='withdrawn' || report.status==='superseded') return {index:-1,text:'这是已撤回或被替代的历史版本。需要继续修改时，请新建修订；旧版证据与记录仍保留。',action:'revise'};
  if (report.status==='published') return {index:4,text:'报告已发布。可按已获准的导出用途下载；需要修改时新建修订。',action:'export'};
  if (report.status==='draft') return {index:0,text:'下一步：核对草稿及证据，填写操作说明，然后点击“提交复核”。',action:'submit'};
  const remaining=report.observations.filter(item=>item.review_status==='unreviewed').length;
  if(remaining) return {index:1,text:`下一步：还有 ${remaining} 条观察未复核。逐条核对证据，选择确认、确认已修正或驳回。`,action:'review'};
  if(!report.content_sha256 || report.reviewed_content_sha256!==report.content_sha256) return {index:2,text:'下一步：观察已处理，请核对整篇摘要、六维结论、证据和覆盖范围，再点击“确认整篇内容”。',action:'confirm_report'};
  return {index:3,text:'下一步：当前内容已完成整篇确认，可点击“发布报告”。发布后才成为课堂当前报告。',action:'publish'};
}
export function ReportProgress({report}:{report:Report}) {
  const next=reportNextStep(report);
  const canAct=next.action!==null && report.allowed_actions.some(action=>action===next.action);
  return <div className="onboarding-next"><ol className="workflow-steps" aria-label="报告复核发布步骤">{['草稿与提交复核','逐条处理观察','确认整篇内容','发布报告','按用途导出'].map((step,index)=><li key={step} aria-current={index===next.index ? 'step' : undefined} className={index===next.index ? 'current' : index<next.index ? 'done' : ''}>{step}</li>)}</ol><p role="status">{next.text}</p>{!canAct && next.action && <p className="field-hint">当前账号没有本步骤的操作权限，或尚未满足来源用途、报告状态等条件。复核与发布请联系有该课堂报告权限的督导或系统管理员，导出用途请联系来源核验人检查。</p>}<p className="field-hint">编辑内容后需要重新提交和确认；只有与当前内容匹配的整篇确认才有效。</p></div>;
}
