const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function setup() {
  class Element {
    constructor(tag) { this.tag = tag; this.children = []; this.textContent = ''; this.attrs = {}; }
    append(n) { this.children.push(n); }
    before(n) { this.previous = n; }
    replaceChildren() { this.children = []; }
    setAttribute(k,v) { this.attrs[k] = v; }
    scrollIntoView() {}
    play() { return Promise.resolve(); }
  }
  const nodes = {transcript: new Element('div'), video: new Element('video')};
  const sandbox = {
    document: {createElement: tag => new Element(tag), createTextNode: text => ({textContent:text})},
    $: id => nodes[id],
    add: (tag,text,root) => { const n = new Element(tag); n.textContent = text; root.append(n); return n; },
    clock: ms => String(ms / 1000)
  };
  vm.createContext(sandbox);
  vm.runInContext(fs.readFileSync(path.join(__dirname,'../web/transcript.js'),'utf8'), sandbox);
  return {nodes, run: code => vm.runInContext(code, sandbox)};
}

test('reading preserves raw text, individual citations and resets for new upload', () => {
  const {nodes,run} = setup();
  run(`renderTranscript([
    {id:'e-0001',start_ms:0,end_ms:27350,text:'which has got.',boundary_reason:'silence'},
    {id:'e-0002',start_ms:27350,end_ms:65000,text:'<img> n 和 l; A lot.',boundary_reason:'end_of_audio'}
  ])`);
  const group = nodes.transcript.children[0];
  assert.equal(group.children[0].children.length,2);
  assert.equal(group.children[1].children.map(x=>x.textContent).join(''), 'which has got. <img> n 和 l; A lot.');
  group.children[0].children[1].onclick();
  assert.equal(nodes.video.currentTime,27.35);
  run('sourceButton.onclick()');
  assert.equal(nodes.transcript.children.length,2);
  assert.equal(nodes.transcript.children[0].children[0].children[1].textContent,'停顿处切分');
  run('renderTranscript([])');
  assert.equal(nodes.transcript.children.length,0);
});

test('embedded page scripts parse and connect stream events to new renderer', () => {
  const html = fs.readFileSync(path.join(__dirname,'../web/index.html'),'utf8');
  for (const match of html.matchAll(/<script>([\s\S]*?)<\/script>/g)) new vm.Script(match[1]);
  assert.ok(html.indexOf('src="/transcript.js"') < html.indexOf('src="/jobs.js"'));
  assert.ok(html.includes('src="/events.js"'));
  assert.ok(html.includes('<details id="revision-panel" hidden>'));
  assert.ok(!html.includes('引用精度为30秒'));
});

test('organized reading joins across boundaries, retains source view and resets', () => {
  const {nodes,run} = setup();
  run(`renderTranscript([
    {id:'e-0001',start_ms:152000,end_ms:182000,text:'A matrix is just a.',boundary_reason:'silence'},
    {id:'e-0002',start_ms:182000,end_ms:212000,text:'Rectangular array of numbers.',boundary_reason:'end_of_audio'}
  ]);
  setReading({status:'ready',message:'已整理',paragraphs:[{text:'A matrix is just a rectangular array of numbers.',source_ids:['e-0001','e-0002']}]});`);
  const row = nodes.transcript.children[0];
  assert.equal(row.children[1].textContent,'A matrix is just a rectangular array of numbers.');
  assert.equal(row.children[0].children.length,2);
  row.children[0].children[1].onclick();
  assert.equal(nodes.video.currentTime,182);
  run('sourceButton.onclick()');
  assert.equal(nodes.transcript.children[0].children[1].textContent,'A matrix is just a.');
  run('readingButton.onclick(); setReading({status:"conservative",message:"保守规则",paragraphs:[{text:"A matrix is just a rectangular array of numbers.",source_ids:["e-0001","e-0002"]}]})');
  assert.equal(nodes.transcript.children[0].children[1].textContent,'A matrix is just a rectangular array of numbers.');
  run('setReading({status:"fallback",message:"未通过校验",paragraphs:[]})');
  assert.equal(nodes.transcript.previous.children[2].textContent,'未通过校验');
  run('renderTranscript([])');
  assert.equal(run('readingResult'),null);
  assert.equal(nodes.transcript.children.length,0);
});

test('missing reading result does not split a sentence at three-block or length boundary', () => {
  const {nodes,run} = setup();
  run(`renderTranscript([
    {id:'s1',start_ms:0,end_ms:30000,text:'Earlier explanation. '.repeat(45)},
    {id:'s2',start_ms:30000,end_ms:60000,text:'More context.'},
    {id:'s3',start_ms:60000,end_ms:90000,text:'A matrix is just a.'},
    {id:'s4',start_ms:90000,end_ms:120000,text:'Rectangular array of numbers.'}
  ])`);
  const texts=nodes.transcript.children.map(row=>row.children[1].children.map(n=>n.textContent).join(''));
  assert.ok(texts.some(text=>text.includes('A matrix is just a rectangular array of numbers.')));
  assert.equal(run('transcriptSegments[2].text'),'A matrix is just a.');
});
