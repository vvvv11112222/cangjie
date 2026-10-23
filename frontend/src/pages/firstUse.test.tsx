// @vitest-environment jsdom
import {act} from 'react';
import {createRoot} from 'react-dom/client';
import {afterEach,beforeEach,expect,it,vi} from 'vitest';
import {SourceSetup} from '../components/SourceSetup';
import {ServiceChecklist} from '../components/ServiceChecklist';
import {ReportProgress} from '../components/ReportProgress';
import {report} from '../data/samples';
import ready from '../../../contracts/examples/service-capabilities.json';

const h=vi.hoisted(()=>({create:vi.fn(),verify:vi.fn()}));
vi.mock('../data/m2Api',()=>({createSource:h.create,verifySource:h.verify}));
let node:HTMLDivElement,root:ReturnType<typeof createRoot>;
beforeEach(()=>{(globalThis as unknown as {IS_REACT_ACT_ENVIRONMENT:boolean}).IS_REACT_ACT_ENVIRONMENT=true;vi.resetAllMocks();node=document.createElement('div');document.body.append(node);root=createRoot(node);});
afterEach(async()=>{await act(async()=>root.unmount());node.remove();});
async function fill(placeholder:string,value:string){const input=node.querySelector<HTMLInputElement|HTMLTextAreaElement>(`[placeholder="${placeholder}"]`)!;await act(async()=>{const proto=input.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;Object.getOwnPropertyDescriptor(proto,'value')!.set!.call(input,value);input.dispatchEvent(new Event('input',{bubbles:true}));});}
async function click(text:string){await act(async()=>{const button=[...node.querySelectorAll('button')].find(item=>item.textContent===text);if(!button)throw new Error(text);button.click();});}
it('registers requested uses without automatically verifying or approving cloud processing',async()=>{
 h.create.mockResolvedValue({id:'source'});const saved=vi.fn();
 await act(async()=>root.render(<SourceSetup sessionId="class" onSaved={saved} onClose={()=>{}}/>));
 await fill('例如：数据结构第三讲课堂录像','短录像');await fill('例如：本校课程组或实际提供者','课程组');
 await fill('例如：课程组已同意用于本次课堂回放和教学分析；请按真实情况填写','已获准本地分析');
 const checkbox=node.querySelector<HTMLInputElement>('input[type=checkbox]')!;await act(async()=>checkbox.click());
 await click('保存来源登记');
 expect(h.create).toHaveBeenCalledWith(expect.objectContaining({session_id:'class',requested_uses:['playback'],external_processing_requested:false}));
 expect(h.verify).not.toHaveBeenCalled();expect(saved).toHaveBeenCalledTimes(1);
});
it('preserves entered source fields after failure and does not accept incomplete permission input',async()=>{
 h.create.mockRejectedValue(new Error('合成网络失败'));
 await act(async()=>root.render(<SourceSetup sessionId="class" onSaved={()=>{}} onClose={()=>{}}/>));
 await click('保存来源登记');expect(h.create).not.toHaveBeenCalled();
 await fill('例如：数据结构第三讲课堂录像','保留的录像名');
 expect(node.querySelector<HTMLInputElement>('[placeholder="例如：数据结构第三讲课堂录像"]')?.value).toBe('保留的录像名');
 expect(node.textContent).toContain('选择实际需要的用途');
 await fill('例如：本校课程组或实际提供者','课程组');
 await fill('例如：课程组已同意用于本次课堂回放和教学分析；请按真实情况填写','已获准本地回放');
 await act(async()=>node.querySelector<HTMLInputElement>('input[type=checkbox]')!.click());
 await click('保存来源登记');
 expect(h.create).toHaveBeenCalledTimes(1);
 expect(node.textContent).toContain('合成网络失败');
 expect(node.querySelector<HTMLInputElement>('[placeholder="例如：数据结构第三讲课堂录像"]')?.value).toBe('保留的录像名');
});
it('explains unconfigured audio and keeps machine details collapsed',async()=>{
 await act(async()=>root.render(<ServiceChecklist data={{...ready,audio_report:{available:false,reasons:['ASR_NOT_CONFIGURED']}}} error="合成技术详情" onReload={()=>{}}/>));
 expect(node.textContent).toContain('尚未配置语音识别');expect(node.textContent).toContain('联系系统管理员');
 expect(node.querySelector('details')).toBeNull();
});
it('points a reader without review rights to the responsible role',async()=>{
 await act(async()=>root.render(<ReportProgress report={{...report,status:'draft',allowed_actions:[]}}/>));
 expect(node.textContent).toContain('当前账号没有本步骤的操作权限');
 expect(node.querySelector('[aria-current=step]')?.textContent).toBe('草稿与提交复核');
});

