// Original segments remain immutable; validated reading edits arrive separately.
let readingResult = null;
function setReading(value) { readingResult = value; renderTranscript(transcriptSegments); }
let transcriptSegments = [];
let transcriptMode = 'reading';
const transcriptTools = document.createElement('div');
transcriptTools.className = 'transcript-tools';
const readingButton = add('button', '连续阅读', transcriptTools);
const sourceButton = add('button', '原始分段', transcriptTools);
const transcriptHelp = add('small', '', transcriptTools);
$('transcript').before(transcriptTools);
for (const button of [readingButton, sourceButton]) button.type = 'button';
readingButton.onclick = () => { transcriptMode = 'reading'; renderTranscript(transcriptSegments); };
sourceButton.onclick = () => { transcriptMode = 'source'; renderTranscript(transcriptSegments); };
function segmentLink(s, root) {
  const b = add('button', clock(s.start_ms) + '–' + clock(s.end_ms), root);
  b.type = 'button';
  b.className = 'segment-link';
  b.title = '回看原文对应音频 ' + clock(s.start_ms) + '–' + clock(s.end_ms) + '（音频块范围）';
  b.setAttribute('aria-label', '回看 ' + clock(s.start_ms) + ' 至 ' + clock(s.end_ms));
  b.onclick = () => {
    $('video').scrollIntoView({block: 'center'});
    $('video').currentTime = s.start_ms / 1000;
    $('video').play().catch(() => {});
  };
}
function renderTranscript(segments) {
  if (!segments.length) readingResult = null;
  transcriptSegments = segments;
  const root = $('transcript');
  root.replaceChildren();
  readingButton.setAttribute('aria-pressed', String(transcriptMode === 'reading'));
  sourceButton.setAttribute('aria-pressed', String(transcriptMode === 'source'));
  transcriptHelp.textContent = transcriptMode === 'reading'
    ? (readingResult ? readingResult.message : '正在展示原始转写接续；转写结束后将结合上下文整理断句。时间按钮对应原始音频块。')
    : '优先在停顿处切分；“强制切分”表示未找到合适停顿，句子可能跨段。时间范围不是逐句对齐。';
  if (transcriptMode === 'reading') {
    if (readingResult && (readingResult.status === 'ready' || readingResult.status === 'conservative')) {
      for (const paragraph of readingResult.paragraphs) {
        const row = add('div', '', root); row.className = 'reading-group';
        const links = add('div', '', row); links.className = 'source-links';
        for (const id of paragraph.source_ids) {
          const source = segments.find(s => s.id === id);
          if (source) segmentLink(source, links);
        }
        add('p', paragraph.text, row);
      }
      return;
    }
    const joins = (left,right) => /\b(?:a|an|the|of|to|with|for|and|or)\.$/.test(left.trim()) && !!right.trim();
    for (let i = 0; i < segments.length;) {
      const group = []; let size = 0;
      do {
        const s = segments[i++]; group.push(s); size += s.text.length;
      } while (i < segments.length && (size < 900 || joins(segments[i-1].text,segments[i].text)));
      const row = add('div', '', root); row.className = 'reading-group';
      const links = add('div', '', row); links.className = 'source-links';
      for (const s of group) segmentLink(s, links);
      const p = add('p', '', row);
      for (const [j, s] of group.entries()) {
        if (j) p.append(document.createTextNode(' '));
        let text = s.text;
        if (j+1<group.length && joins(text,group[j+1].text)) text=text.trimEnd().replace(/\.$/,'');
        if (j && joins(group[j-1].text,s.text)) text=text.replace(/^(Rectangular|Exercises)\b/,word=>word[0].toLowerCase()+word.slice(1));
        const span = add('span', text, p); span.title = '原文来源：' + clock(s.start_ms) + '–' + clock(s.end_ms);
      }
    }
  } else {
    for (const s of segments) {
      const row = add('div', '', root); row.className = 'source-row';
      const meta = add('div', '', row);
      segmentLink(s, meta);
      add('small', ({silence:'停顿处切分', forced_limit:'强制切分 · 请结合下段', end_of_audio:'音频结束'})[s.boundary_reason] || '原始音频块', meta);
      add('p', s.text, row);
    }
  }
}
renderTranscript([]);
