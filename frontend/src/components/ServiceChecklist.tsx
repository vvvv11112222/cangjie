import type { ServiceCapabilities } from '../data/serviceCapabilities';
import { featureProblem } from '../data/serviceCapabilities';
export function ServiceChecklist({data,error,onReload}:{data:ServiceCapabilities|null;error:string|null;onReload():void}) {
  const unavailable=data ? (['visual_analysis','audio_report'] as const).filter(name=>!data[name].available) : [];
  if(data && !unavailable.length) return null;
  return <div className="service-status" role={error||unavailable.length?'alert':'status'}>
    {data ? unavailable.map(name=><p key={name}>{featureProblem(data[name])}</p>) : <p>{error ?? '正在检查分析服务…'}</p>}
    {(error||unavailable.length>0)&&<button type="button" className="btn btn-secondary btn-sm" onClick={onReload}>重新检查服务状态</button>}
  </div>;
}
