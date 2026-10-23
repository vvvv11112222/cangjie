import type { ReactNode } from 'react';
import { createContext,useContext,useEffect,useRef,useState } from 'react';
import { createPortal } from 'react-dom';
import { useAuth } from '../auth';
import type { RoleCode } from '../data/authApi';
import { guideSteps,guideStorageKey } from '../data/guide';

type GuideState={role:RoleCode;index:number;sessionId:string|null;active:boolean;seen:boolean};
interface GuideContext { state:GuideState; change(next:Partial<GuideState>):void; }
const DEFAULT:GuideState={role:'teacher',index:0,sessionId:null,active:false,seen:false};
const Context=createContext<GuideContext>({state:DEFAULT,change:()=>{}});
export const useGuide=()=>useContext(Context);
const dismissed=new Set<string>();
function read(user:string):GuideState {
  try {
    const raw=JSON.parse(localStorage.getItem(guideStorageKey(user)) ?? 'null');
    if(raw && ['teacher','supervisor','academic_admin','sys_admin'].includes(raw.role)) return {...DEFAULT,role:raw.role,index:Number.isInteger(raw.index)&&raw.index>=0?raw.index:0,sessionId:typeof raw.sessionId==='string'?raw.sessionId:null,seen:raw.seen===true};
  } catch { /* Storage disabled: the help page remains usable. */ }
  return DEFAULT;
}
export function GuideProvider({children}:{children:ReactNode}) {
  const {me,status}=useAuth();
  const user=status==='authenticated' ? me?.user_id ?? null : null;
  const [saved,setSaved]=useState<{user:string|null;value:GuideState}>({user:null,value:DEFAULT});
  const [welcome,setWelcome]=useState(false);
  useEffect(()=>{
    if(!user){setSaved({user:null,value:DEFAULT});setWelcome(false);return;}
    const roles=me?.roles?.map(item=>item.role_code) ?? [];
    const value=read(user);
    const role=value.seen&&(roles.includes(value.role)||roles.includes('sys_admin')) ? value.role : roles[0] ?? 'teacher';
    setSaved({user,value:{...value,role}});setWelcome(!value.seen&&!dismissed.has(user));
  },[user]);
  const state=saved.user===user?saved.value:DEFAULT;
  function change(next:Partial<GuideState>){
    if(user&&saved.user!==user)return;
    const value={...state,...next};setSaved({user,value});
    if(user){try{localStorage.setItem(guideStorageKey(user),JSON.stringify({...value,active:false}));}catch{/* Browsing still works. */}}
  }
  const close=(start:boolean)=>{if(user)dismissed.add(user);change({seen:true,active:false});setWelcome(false);if(start)window.location.hash='#/help';};
  return <Context.Provider value={{state,change}}>{children}{welcome&&user&&<Welcome onClose={()=>close(false)} onStart={()=>close(true)}/>}<GuideHighlight/></Context.Provider>;
}
function Welcome({onClose,onStart}:{onClose():void;onStart():void}) {
  const box=useRef<HTMLDivElement>(null);const first=useRef<HTMLButtonElement>(null);
  useEffect(()=>{const previous=document.activeElement as HTMLElement|null;first.current?.focus();return()=>{if(previous?.isConnected)previous.focus();};},[]);
  return createPortal(<div className="guide-backdrop"><div className="guide-welcome" role="dialog" aria-modal="true" aria-labelledby="guide-welcome-title" ref={box} onKeyDown={event=>{
    if(event.key==='Escape'){event.preventDefault();onClose();}
    if(event.key==='Tab') {const nodes=box.current?.querySelectorAll<HTMLButtonElement>('button');if(!nodes?.length)return;const first=nodes[0],last=nodes[nodes.length-1];if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus();}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus();}}
  }}><h2 id="guide-welcome-title">欢迎使用观课</h2><p>用几步了解课堂、上传、分析和报告。可以随时从右上角“使用帮助”重新打开。</p><div className="action-row"><button type="button" className="btn btn-primary" ref={first} onClick={onStart}>开始使用指引</button><button type="button" className="btn btn-secondary" onClick={onClose}>以后再看</button></div></div></div>,document.body);
}
function GuideHighlight(){
  const {state}=useGuide();
  const target=guideSteps(state.role,state.sessionId)[state.index]?.target;
  useEffect(()=>{
    if(!state.active||!target)return;
    let highlighted:HTMLElement|null=null;
    const find=()=>{const node=document.querySelector<HTMLElement>(target);if(!node)return;highlighted=node;node.classList.add('guide-target');node.scrollIntoView?.({block:'center',behavior:'smooth'});observer.disconnect();};
    const observer=new MutationObserver(find);observer.observe(document.body,{childList:true,subtree:true});find();const timeout=window.setTimeout(()=>observer.disconnect(),4000);
    return()=>{observer.disconnect();clearTimeout(timeout);highlighted?.classList.remove('guide-target');};
  },[state.active,state.role,state.index,state.sessionId,target]);
  return null;
}
export function GuideToolbar(){
  const {state}=useGuide();
  return <a className="btn btn-secondary btn-sm" href={state.active?`#/help?step=${state.index}`:'#/help'}>{state.active?`返回引导 · ${state.index+1}/${guideSteps(state.role,state.sessionId).length}`:'使用帮助'}</a>;
}
