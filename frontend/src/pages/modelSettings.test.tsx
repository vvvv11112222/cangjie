// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach,beforeEach,expect,it,vi } from 'vitest';
import fixture from '../../../contracts/examples/model-settings.json';
import { ApiError } from '../data/errors';
import { parseModelSettings } from '../data/modelSettings';
import { ModelSettingsPanel } from './ModelSettingsPanel';

const h=vi.hoisted(()=>({read:vi.fn(),write:vi.fn()}));
vi.mock('../data/modelSettings',async original=>({...await original<typeof import('../data/modelSettings')>(),getModelSettings:h.read,updateModelSettings:h.write}));
let container:HTMLDivElement,root:ReturnType<typeof createRoot>;
beforeEach(()=>{(globalThis as unknown as {IS_REACT_ACT_ENVIRONMENT:boolean}).IS_REACT_ACT_ENVIRONMENT=true;vi.resetAllMocks();h.read.mockResolvedValue(fixture);container=document.createElement('div');document.body.append(container);root=createRoot(container);});
afterEach(async()=>{await act(async()=>root.unmount());container.remove();});
async function mount(){await act(async()=>root.render(<ModelSettingsPanel/>));}
async function fill(selector:string,value:string){const input=container.querySelector<HTMLInputElement|HTMLTextAreaElement>(selector)!;await act(async()=>{Object.getOwnPropertyDescriptor(input.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value')!.set!.call(input,value);input.dispatchEvent(new Event('input',{bubbles:true}));});}
async function confirm(){const label=[...container.querySelectorAll('label')].find(item=>item.textContent?.includes('已核对同地域'))!;await act(async()=>label.querySelector<HTMLInputElement>('input')!.click());}
async function submit(){await act(async()=>container.querySelector('form')!.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})));}
it('rejects a response that exposes keys',()=>{expect(()=>parseModelSettings({...fixture,asr_api_key:'synthetic-leak'})).toThrow('credential');});
it('requires price confirmation and preserves existing keys when blank',async()=>{
 h.write.mockResolvedValue({...fixture,revision:'00000000-0000-4000-8000-000000000014',active_revision:'00000000-0000-4000-8000-000000000014'});
 await mount();expect(container.querySelector<HTMLInputElement>('input[type=password]')?.value).toBe('');
 await fill('textarea[placeholder]', 'Synthetic setup');await submit();expect(h.write).not.toHaveBeenCalled();
 await confirm();await submit();
 expect(h.write).toHaveBeenCalledTimes(1);const body=h.write.mock.calls[0]![0];expect(body.asr_api_key).toBeUndefined();expect(body.report_api_key).toBeUndefined();
 expect(container.textContent).toContain('配置已保存并生效');expect(container.textContent).toContain('尚未进行云端连通性测试');
});
it('blocks application while tasks or billing are outstanding',async()=>{
 h.read.mockResolvedValue({...fixture,can_apply:false});await mount();
 expect([...container.querySelectorAll('button')].find(button=>button.textContent==='保存并应用模型配置')?.disabled).toBe(true);
 await fill('textarea[placeholder]', 'Synthetic setup');await confirm();await submit();expect(h.write).not.toHaveBeenCalled();
 expect(container.textContent).toContain('待核对账单');
});
it('keeps edited fields and original revision when a refreshed config conflicts',async()=>{
 await mount();await fill('input[aria-label="语音模型名称"]','edited-synthetic-model');await fill('textarea[placeholder]','Synthetic edit');await confirm();
 h.read.mockResolvedValue({...fixture,revision:'00000000-0000-4000-8000-000000000014'});
 await act(async()=>[...container.querySelectorAll('button')].find(item=>item.textContent==='重新读取配置状态')!.click());
 h.write.mockRejectedValue(new ApiError({code:'REVISION_CONFLICT',status:409,message:'配置已被其他管理员修改，请重新读取后再保存'}));await submit();
 expect(h.write.mock.calls[0]![0].expected_revision).toBe(fixture.revision);
 expect(container.querySelector<HTMLInputElement>('input[aria-label="语音模型名称"]')?.value).toBe('edited-synthetic-model');
 expect(container.textContent).toContain('配置已被其他管理员修改');
});
it('ignores a status read that finishes after a successful application',async()=>{
 await mount();await fill('textarea[placeholder]','Synthetic edit');await confirm();
 let resolveRead!:(value:typeof fixture)=>void;
 h.read.mockReturnValue(new Promise<typeof fixture>(resolve=>{resolveRead=resolve;}));
 await act(async()=>[...container.querySelectorAll('button')].find(item=>item.textContent==='重新读取配置状态')!.click());
 const next={...fixture,revision:'00000000-0000-4000-8000-000000000014',active_revision:'00000000-0000-4000-8000-000000000014',values:{...fixture.values,asr_model:'newly-applied-model'}};
 h.write.mockResolvedValue(next);await submit();await act(async()=>resolveRead(fixture));
 expect(container.querySelector<HTMLInputElement>('input[aria-label="语音模型名称"]')?.value).toBe('newly-applied-model');
 expect(container.textContent).toContain(next.active_revision);
});
