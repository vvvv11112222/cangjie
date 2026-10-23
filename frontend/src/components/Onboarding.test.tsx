// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { GuideProvider, GuideToolbar } from './Onboarding';
import { HelpPage } from '../pages/HelpPage';
import { guideSteps, guideStorageKey } from '../data/guide';
const auth=vi.hoisted(()=>({user:'new-teacher',role:'teacher',status:'authenticated'}));
vi.mock('../auth',()=>({useAuth:()=>({me:{user_id:auth.user,roles:[{role_code:auth.role}]},status:auth.status})}));
vi.mock('../data/hooks',()=>({useSessionTasks:()=>({data:{tasks:[{session:{id:'class-a',title:'周一操作系统'}}]},actionLabel:null,error:null})}));
let node:HTMLDivElement,root:Root;
beforeEach(()=>{Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true});localStorage.clear();auth.user=`user-${Math.random()}`;auth.role='teacher';auth.status='authenticated';node=document.createElement('div');document.body.append(node);root=createRoot(node);});
afterEach(async()=>{await act(async()=>root.unmount());node.remove();});
async function render(help=false){await act(async()=>root.render(<GuideProvider><GuideToolbar/>{help&&<HelpPage/>}</GuideProvider>));}
async function click(text:string){await act(async()=>{const button=[...document.querySelectorAll('button')].find(item=>item.textContent===text);if(!button)throw Error(text);button.click();});}
it('shows welcome once, supports Escape and restores the previously focused control',async()=>{
 const previous=document.createElement('button');document.body.append(previous);previous.focus();await render();
 const dialog=document.querySelector('[role=dialog]')!;expect(dialog).not.toBeNull();expect(document.activeElement?.textContent).toBe('开始使用指引');
 await act(async()=>dialog.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true})));
 expect(document.querySelector('[role=dialog]')).toBeNull();expect(document.activeElement).toBe(previous);
 expect(JSON.parse(localStorage.getItem(guideStorageKey(auth.user))!).seen).toBe(true);previous.remove();
});
it('traps focus inside first-use welcome and lets users defer it',async()=>{
 await render();const buttons=document.querySelectorAll<HTMLButtonElement>('[role=dialog] button');buttons[1]!.focus();
 await act(async()=>buttons[1]!.dispatchEvent(new KeyboardEvent('keydown',{key:'Tab',bubbles:true,cancelable:true})));expect(document.activeElement).toBe(buttons[0]);
 await click('以后再看');expect(document.querySelector('[role=dialog]')).toBeNull();
});
it('keeps welcome and selected classroom isolated between accounts',async()=>{
 await render();await click('以后再看');const first=auth.user;auth.user='different-account';await render();expect(document.querySelector('[role=dialog]')).not.toBeNull();
 expect(localStorage.getItem(guideStorageKey(first))).not.toBeNull();expect(localStorage.getItem(guideStorageKey(auth.user))).toBeNull();
});
it('provides a complete teacher guide without performing any operation automatically',async()=>{
 localStorage.setItem(guideStorageKey(auth.user),JSON.stringify({seen:true,role:'teacher'}));await render(true);
 expect(document.querySelectorAll('[aria-label="使用步骤"] button')).toHaveLength(7);
 expect(node.textContent).toContain('一条课堂记录对应一次上课');expect(node.querySelector('input[type=file]')).toBeNull();
 await click('去操作');expect(window.location.hash).toBe('#/sessions');expect(node.querySelector('a')?.textContent).toContain('返回引导');
});
it('does not give academic users the teacher analysis or report steps',async()=>{
 auth.role='academic_admin';localStorage.setItem(guideStorageKey(auth.user),JSON.stringify({seen:true,role:'academic_admin'}));await render(true);
 const steps=node.querySelector('[aria-label="使用步骤"]')!.textContent;expect(steps).toContain('准备教务');expect(steps).not.toContain('转写语音');expect(node.querySelectorAll('[aria-label="引导角色"] option')).toHaveLength(1);
});
it('uses the actual admin role on first use and ignores an obsolete saved classroom',async()=>{
 auth.role='sys_admin';localStorage.setItem(guideStorageKey(auth.user),JSON.stringify({seen:true,role:'sys_admin',index:3,sessionId:'deleted-class'}));await render(true);
 expect((node.querySelector('[aria-label="引导课堂"]') as HTMLSelectElement).value).toBe('');await click('去操作');expect(window.location.hash).toBe('#/sessions?start=upload');
});
it('highlights only the explicit guide target and cleans it up when returning to help',async()=>{
 localStorage.setItem(guideStorageKey(auth.user),JSON.stringify({seen:true,role:'teacher'}));await render(true);
 const target=document.createElement('button');target.dataset.guide='classroom-list';document.body.append(target);const action=vi.fn();target.onclick=action;
 await click('去操作');expect(target.classList.contains('guide-target')).toBe(true);expect(action).not.toHaveBeenCalled();
 await click('下一步');expect(target.classList.contains('guide-target')).toBe(false);target.remove();
});
it('still offers help when browser persistence is disabled',async()=>{
 const get=vi.spyOn(Storage.prototype,'getItem').mockImplementation(()=>{throw Error('blocked');});const set=vi.spyOn(Storage.prototype,'setItem').mockImplementation(()=>{throw Error('blocked');});
 await render();await click('以后再看');expect(node.textContent).toContain('使用帮助');get.mockRestore();set.mockRestore();
});
it('has valid destinations for every role and no write API in guide definitions',()=>{
 for(const role of ['teacher','supervisor','academic_admin','sys_admin'] as const)for(const step of guideSteps(role,'class-a')){expect(step.path).toMatch(/^#\//);expect(step.target).toContain('data-guide');}
 expect(guideSteps('teacher','class-a').find(step=>step.id==='upload')?.path).toContain('?start=upload');
});
