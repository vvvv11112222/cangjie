import { useState } from 'react';
import { Card,LoadControls,LoadStateNote } from '../components/ui';
import { useSessionTasks } from '../data/hooks';
import { featureProblem,useServiceCapabilities } from '../data/serviceCapabilities';
export function DevicesPage(){
 const loaded=useSessionTasks();const services=useServiceCapabilities();const [selected,setSelected]=useState('');
 const id=loaded.data.tasks.some(item=>item.session.id===selected)?selected:loaded.data.tasks[0]?.session.id;
 return <div className="page"><header className="page-head"><h1>设备接入</h1><a className="btn btn-secondary" href="#/help">手机录制帮助</a></header>
  <Card title="手机实时录制"><LoadStateNote status={loaded.status} error={loaded.error}/>
   <label className="account-field"><span>课堂</span><select value={id??''} onChange={event=>setSelected(event.target.value)}><option value="" disabled>请选择课堂</option>{loaded.data.tasks.map(({session})=><option key={session.id} value={session.id}>{session.title}</option>)}</select></label>
   <LoadControls actionLabel={loaded.actionLabel} loadMore={loaded.loadMore}/>
   {services.data&&!services.data.live_capture.available&&<p role="alert">{featureProblem(services.data.live_capture)}</p>}
   {id&&<a className="btn btn-primary" href={`#/sessions/${encodeURIComponent(id)}?tab=live`}>打开实时课堂</a>}
   {!id&&loaded.status==='ready'&&<p className="empty">请先创建本次课堂。</p>}
  </Card><Card title="固定摄像头"><span className="badge badge-gray">暂未开放</span></Card>
 </div>;
}
