import { useEffect, useState } from 'react';
import { useAuth } from '../auth';
import { getSource } from './m2Api';
import type { SourceRecord } from './m2Api';
import { describeError } from './errors';

export function useSourceRecord(id: string | null, enabled = true) {
  const { sessionKey } = useAuth();
  const key = `${sessionKey}:${id}`;
  const [state,setState] = useState<{key:string;data:SourceRecord|null;error:string|null}>({key,data:null,error:null});
  const [revision,setRevision] = useState(0);
  useEffect(()=>{
    let active=true;
    if (!enabled || id===null) return;
    setState({key,data:null,error:null});
    void getSource(id).then(data=>{if(active)setState({key,data,error:null});}).catch(cause=>{if(active)setState({key,data:null,error:describeError(cause)});});
    return ()=>{active=false;};
  },[key,id,enabled,revision]);
  return { data:state.key===key ? state.data : null,error:state.key===key ? state.error : null,reload:()=>setRevision(value=>value+1) };
}
