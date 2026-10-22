import type { ServiceCapabilities } from '../data/serviceCapabilities';
import { featureProblem } from '../data/serviceCapabilities';
import { TechDetails } from './ui';

export function ServiceChecklist({data,error,onReload}:{data:ServiceCapabilities|null;error:string|null;onReload():void}) {
  return <div className="service-checklist" aria-label="分析服务准备情况">
    <div className="analysis-options">{(['visual_analysis','audio_report'] as const).map(name=><article key={name}><strong>{name==='visual_analysis' ? '举手与低头识别' : '语音转写与教学报告'}</strong><p className="field-hint">{name==='visual_analysis' ? '输出可见人物、动作观察与原画面证据；使用本地视觉模型。' : '从带声音的录像提取音频，生成转写及有证据的六维报告；需要云端处理许可。'}</p><p className={data?.[name].available ? 'load-note' : 'load-note-error'}>{data ? data[name].available ? '处理服务已就绪；提交时还会检查当前录像用途与任务状态。' : featureProblem(data[name]) : error ? '暂时无法确认处理服务状态，请重新检查；确认前不能提交新分析。' : '正在检查处理服务…'}</p></article>)}</div>
    <button type="button" className="btn btn-secondary btn-sm" onClick={onReload}>重新检查服务状态</button>
    {error && <TechDetails summary="服务检查技术详情"><p>{error}</p></TechDetails>}
  </div>;
}
