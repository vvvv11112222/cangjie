import { describe,expect,it } from 'vitest';
import ready from '../../../contracts/examples/service-capabilities.json';
import { parseServiceCapabilities, featureProblem } from './serviceCapabilities';
import { sessionDetailHash,sessionIntent,parseSessionRoute } from './route';
import { reportNextStep } from '../components/ReportProgress';
import { report } from './samples';

describe('readiness and workflow boundaries',()=>{
 it('requires explicit availability and preserves missing configuration reasons',()=>{
  expect(parseServiceCapabilities(ready).audio_report.available).toBe(true);
  const broken={...ready,audio_report:{available:true,reasons:['ASR_NOT_CONFIGURED']}};
  expect(()=>parseServiceCapabilities(broken)).toThrow('inconsistent');
  expect(()=>parseServiceCapabilities({...ready,checked_at:'bad'})).toThrow();
  expect(featureProblem({available:false,reasons:['ASR_NOT_CONFIGURED']})).toContain('联系系统管理员');
 });
 it('keeps the classroom, uploaded asset, and destination when continuing a task',()=>{
  const session='00000000-0000-4000-8000-000000000001',media='00000000-0000-4000-8000-000000000002';
  const hash=sessionDetailHash(session,{mediaId:media,tab:'live'});
  expect(parseSessionRoute(hash)).toEqual({kind:'detail',sessionId:session});
  expect(sessionIntent(hash)).toEqual({tab:'live',mediaId:media,upload:false});
  expect(sessionIntent('#/sessions/'+session+'?tab=admin&media=invalid')).toEqual({tab:'run',mediaId:null,upload:false});
 });
 it('distinguishes pending observations, whole-report confirmation, and publication',()=>{
  const reviewed={...report,status:'in_review' as const,observations:report.observations.map(item=>({...item,review_status:'accepted' as const})),content_sha256:'a'.repeat(64),reviewed_content_sha256:null};
  expect(reportNextStep({...reviewed,observations:[{...report.observations[0]!,review_status:'unreviewed'}]}).action).toBe('review');
  expect(reportNextStep(reviewed).action).toBe('confirm_report');
  expect(reportNextStep({...reviewed,reviewed_content_sha256:reviewed.content_sha256}).action).toBe('publish');
  expect(reportNextStep({...reviewed,reviewed_content_sha256:'b'.repeat(64)}).action).toBe('confirm_report');
  expect(reportNextStep({...reviewed,status:'published'}).action).toBe('export');
 });
});
