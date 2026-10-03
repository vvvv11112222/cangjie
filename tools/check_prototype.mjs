// Exercise the actual inline functions with a small DOM stub. This is not a browser visual test.
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const html = fs.readFileSync(path.join(root, '系统草图.html'), 'utf8');
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
new vm.Script(script); // Parse the complete script, including event handlers.
const cutoff = script.indexOf("  document.addEventListener('click'");
assert(cutoff > 0);
const instrumented = script.slice(0, cutoff) + `
  globalThis.prototypeTest={state,rolePermissions,hasPermission,scheduleConflict,exportReport,publish,startSimulation,
    sourcesPage,reportPage: typeof reportPage === 'function' ? reportPage : null};
  toast=(message)=>globalThis.messages.push(message);render=()=>{};log=()=>{};
  download=(name,content)=>globalThis.downloads.push(JSON.parse(content));
})();`;
const context = vm.createContext({localStorage: {getItem: () => null}, messages: [], downloads: [],
  setInterval: () => { throw new Error('Unexpected simulation started'); }, Map, console});
new vm.Script(instrumented).runInContext(context);
const t = context.prototypeTest;
const as = role => { t.state.role = role; t.state.accountId = t.state.accounts.find(a => a.role === role).id; };
as('teacher'); assert(t.hasPermission('edit')); assert(!t.hasPermission('review')); assert(!t.hasPermission('publish'));
as('supervisor'); assert(t.hasPermission('review')); assert(!t.hasPermission('upload')); assert(!t.hasPermission('analyze'));
as('academic'); assert(!t.hasPermission('view'));
as('admin'); assert(t.hasPermission('publish'));
const occupied = t.state.schedule[0], offering = t.state.offerings.find(o => o.id === occupied.offeringId);
const free = {id: 'new', day: occupied.day, slot: occupied.slot, teacher: 'unused', classId: 'unused', room: 'unused'};
assert(!t.scheduleConflict(free));
for (const key of ['teacher', 'classId', 'room']) assert(t.scheduleConflict({...free, [key]: offering[key]}), key);
assert(!t.scheduleConflict({...free, day: 4}));
const session = t.state.sessions.find(s => s.report?.status === 'draft');
t.exportReport(session); assert.equal(context.downloads.length, 0, 'Draft export must be rejected');
session.report.archives.push({...JSON.parse(JSON.stringify(session.report)), status: 'published', version: 7, summary: 'Published text'});
t.exportReport(session); assert.equal(context.downloads[0].report.version, 7);
assert.equal(context.downloads[0].report.summary, 'Published text');
session.report.observations.forEach(o => o.status = 'accepted');
session.report.status = 'in_review';session.report.reviewComplete = false;
t.publish(session); assert.equal(session.report.status, 'in_review', 'Whole-report confirmation required');
session.report.reviewComplete = true;t.publish(session);assert.equal(session.report.status, 'published');
const pending = t.state.sessions.find(s => !s.report);pending.fixture = false;pending.sourceVerified = false;
t.startSimulation(pending.id);assert.match(context.messages.at(-1), /核验/);
assert.match(t.sourcesPage(), /待核验/);
console.log('PASS prototype syntax and behavior: fixed roles, three schedule conflicts, current-version export, whole-report review, pending source');
