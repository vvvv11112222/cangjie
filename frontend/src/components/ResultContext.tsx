import { useEffect,useState } from 'react';
import { useAuth } from '../auth';
import { describeError } from '../data/errors';
import { useMediaDetail } from '../data/hooks';
import { dataSource,isApiMode } from '../data/sources';
import { formatDateTime,runModeLabel } from '../format';
import type { Run } from '../types';


export function ResultContext({run:knownRun,runId,mediaId,version}:{run?:Run|null;runId?:string|null;mediaId?:string|null;version?:string}) {
  const { sessionKey } = useAuth();
  const key=`${sessionKey}:${runId}`;
  const [loaded,setLoaded]=useState<{key:string;run:Run|null;error:string|null}>({key,run:null,error:null});
  useEffect(()=>{
    let active=true;
    if(!isApiMode() || knownRun || !runId) return;
    setLoaded({key,run:null,error:null});
    void dataSource.getRun(runId).then(run=>{if(active)setLoaded({key,run,error:null});}).catch(cause=>{if(active)setLoaded({key,run:null,error:describeError(cause)});});
    return ()=>{active=false;};
  },[key,runId,knownRun]);
  const run=knownRun ?? (loaded.key===key ? loaded.run : null);
  const id=mediaId ?? run?.media_asset_id ?? null;
  const detail=useMediaDetail(id,isApiMode() && id!==null);
  const media=detail.status==='ready' && detail.data.media?.id===id ? detail.data.media : null;
  return <div className="result-context" aria-label="当前结果来源"><strong>{version ?? '结果录像'}</strong><span>{media?.original_filename ?? (id ? '正在读取录像信息…' : '暂无对应录像')}{run && <> · {runModeLabel(run.mode)}</>}{run?.created_at && <> · {formatDateTime(run.created_at)}</>}</span>{(detail.error||loaded.error)&&<p role="alert">{detail.error??loaded.error}</p>}</div>;
}
