import { useEffect, useState } from 'react';
import type { Run } from '../types';
import { useAuth } from '../auth';
import { useMediaDetail } from '../data/hooks';
import { dataSource, isApiMode } from '../data/sources';
import { describeError } from '../data/errors';
import { formatDateTime, runModeLabel } from '../format';
import { TechDetails } from './ui';

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
  return <div className="result-context" aria-label="当前结果来源"><strong>{version ?? '当前结果对应录像'}</strong><p>{media?.original_filename ?? (media ? '历史录像（文件名未记录）' : id ? '正在确认录像信息；读取失败时请重新进入本条记录。' : '暂无对应录像信息。')}{media?.created_at && <> · 上传于 {formatDateTime(media.created_at)}</>}</p>{run && <p>{runModeLabel(run.mode)} · {run.created_at ? formatDateTime(run.created_at) : '旧记录未提供分析提交时间'}。结果属于这次分析，切换录像不会替换已有证据。</p>}{(id || runId || run) && <TechDetails summary="结果来源编号与读取详情"><p>录像：{id ?? '尚未读取'}；分析：{run?.id ?? runId ?? '尚未读取'}</p>{(detail.error || loaded.error) && <p>{detail.error ?? loaded.error}</p>}</TechDetails>}</div>;
}
