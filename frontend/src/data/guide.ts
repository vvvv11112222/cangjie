import type { RoleCode } from './authApi';

export interface GuideStep { id:string; title:string; text:string; checks:string[]; path:string; target:string; }
const step=(id:string,title:string,text:string,checks:string[],path:string,target:string):GuideStep=>({id,title,text,checks,path,target});
export function guideSteps(role:RoleCode,sessionId:string|null):GuideStep[] {
  const session=sessionId ? `#/sessions/${encodeURIComponent(sessionId)}` : '#/sessions';
  const choose=step('classroom','选择本次课堂','一条课堂记录对应一次上课。先找到要处理的那一次课堂。',['课程是一门科目；开课记录关联学期、教师和班级。','没有自己的课堂时，请教务检查任课关系或创建本次课堂。'], '#/sessions','[data-guide="classroom-list"]');
  const source=step('source','登记与核验来源','来源用于记录录像由谁提供、实际允许哪些用途。',['上传时可以直接登记来源，不需要填写来源编号。','教师申请用途；教务或管理员核对许可后通过。','只有获得明确授权时才允许云端处理音频和文本。'],sessionId ? `#/sources?session=${encodeURIComponent(sessionId)}` : '#/sources','[data-guide="source-list"]');
  const upload=step('upload','上传录像','进入本次课堂，点击上传录像，选择文件与对应来源。',['上传成功不等于开始分析。','同一课堂可以保存多段录像；分析前确认选中的文件名。','等待核验时联系教务，不要重复上传相同文件。'],sessionId ? `${session}?start=upload` : '#/sessions?start=upload','[data-guide="upload-recording"]');
  const visual=step('visual','分析举手与低头','选择录像后点击举手与低头识别。',['使用服务器的本地视觉模型，等待分析完成。','结果在视觉观察；勾选显示所有人物框可查看全班。','灰色表示看不清，绿色表示未见这两种动作。低头不直接代表不认真。'],`${session}${sessionId?'?tab=run':''}`,'[data-guide="visual-analyze"]');
  const speech=step('speech','转写语音并生成报告','视觉分析完成后，再对同一录像提交语音转写与教学报告。',['管理员需先配置模型、价格和预算；来源需允许云端处理。','转写标签查看带时间的文字；报告标签查看综合分析。','已有文字时，可在转写中仅重新生成报告，无需重复识别语音。','完整报告会采用实际可用的教务登记、语音与同录像视觉结果；没有的材料会注明。'],`${session}${sessionId?'?tab=run':''}`,'[data-guide="speech-analyze"]');
  const results=step('results','查看与核对结果','从课堂详情切换视觉观察、转写、报告和回放。',['结果绑定本次分析的录像；切换录像不会替换旧证据。','点击观察旁的查看依据，可以核对原录像与文字。','编号只属于这次录像，轨迹数不等于学生出勤人数。'],`${session}${sessionId?'?tab=visual':''}`,'[data-guide="classroom-tabs"]');
  const review=step('review','复核、发布与导出','核对报告草稿和证据，再提交复核。',['有权限的督导或管理员逐条确认或驳回观察。','全部观察处理后，确认整篇当前内容，再发布。修改内容后须重新确认。','发布后可按许可导出。教师没有发布权限时，请联系授权督导。'],`${session}${sessionId?'?tab=report':''}`,'[data-guide="report-review"]');
  const academic=step('academic','准备教务与课表','建立学院、班级、课程、学期和开课记录。',['开课记录关联任课教师、班级与学期。','课表关联时间和教室；课堂是一次具体授课。','有历史数据的资料通过归档保留；仅未使用资料可删除。'],'#/academic','[data-guide="academic-tabs"]');
  const create=step('create','创建本次课堂','选择课程与班级，填写这次上课的开始和结束时间。',['不要把整个学期作为一次课堂的时间范围。','演示课堂用于练习，不等同于真实分析结果。'],'#/sessions','[data-guide="create-classroom"]');
  const settings=step('settings','配置分析服务','在运行与设置中配置语音、报告模型、价格和预算。',['密钥只保存在服务端，不要发送到聊天或提交仓库。','云端分析可能产生费用；先核对供应商单价与允许预算。','视觉模型运行在服务器；需要独显时启动器会检查实际设备。'],'#/settings','[data-guide="model-settings"]');
  if(role==='academic_admin') return [academic,create,source,step('handoff','交接给任课教师','教务准备课堂并核验来源后，由任课教师或授权督导处理录像内容。',['教务账号不读取录像、转写和报告内容。','任课范围不正确时先修正开课关联；不通过重复授权绕过权限。'],'#/sessions','[data-guide="classroom-list"]')];
  if(role==='sys_admin') return [settings,academic,create,upload,source,visual,speech,results,review];
  return role==='supervisor' ? [choose,results,visual,speech,review] : [choose,upload,source,visual,speech,results,review];
}
export const GUIDE_VERSION='guide-v3';
export function guideStorageKey(userId:string):string {return `guanke:${GUIDE_VERSION}:${userId}`;}
