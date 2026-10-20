// Recovery decisions use explicit outcomes, not the number of warning strings.
function recoveryView(job) {
  const active = ['queued', 'running'].includes(job.status);
  const hasRevision = job.revision_no > 0;
  const incomplete = job.asr_status === 'incomplete';
  const unknown = job.asr_status === 'unknown';
  const partial = job.status === 'partial';
  const failed = job.status === 'failed';
  const unsafeCheckpoint = !hasRevision && (job.completed_asr_chunks||[]).length > 0 && (unknown || (job.warnings||[]).some(w=>/^第 .*段(?: ASR 失败|音频提取失败|音频读取失败)/.test(w)));
  const retry = !active && !unsafeCheckpoint && (partial || failed) && !!job.retryable;
  let guidance = hasRevision
    ? '转写已保存，可以重新生成分析；不需要再次上传视频或调用 ASR。'
    : '已保存成功的转写检查点；重试会从未完成的音频块继续。';
  if (hasRevision && incomplete) guidance = '当前转写不完整。重新分析现有文字不会补齐缺失转写；可以使用原视频创建新的转写任务，旧版本继续保留。';
  if (hasRevision && unknown) guidance = '旧任务的转写完整性无法确认。可以重新分析现有文字；如需确认完整覆盖，可使用原视频创建新的转写任务。';
  if (unsafeCheckpoint) guidance = '旧任务的检查点可能将失败请求记为完成，不能安全续跑。请用原视频创建独立转写任务，原有结果仍保留。';
  const error = job.error || '';
  if (hasRevision && !incomplete && !unknown && error.includes('教学事件')) {
    if (error.includes('维度')) guidance = '教学事件的维度归属未通过标准校验。转写已保存；这是分析结构问题，不需要修改转写文字。';
    else if (error.includes('引文') || error.includes('原文') || error.includes('证据编号')) guidance = '教学事件的原文证据未通过校验。转写已保存，可以重新生成分析；不需要再次上传或转写。';
  }
  return {
    retry,
    reanalyze: !active && hasRevision && !retry,
    retranscribe: !active && ((hasRevision && (incomplete || unknown)) || unsafeCheckpoint),
    showGuidance: partial || failed || (hasRevision && (incomplete || unknown)),
    guidance,
    retryLabel: hasRevision ? (incomplete || unknown ? '重新分析现有文字' : '重新生成分析') : '继续转写',
    statusText: partial ? '部分完成 · 请查看结果说明' : failed ? (hasRevision ? '分析失败（转写已保存）' : '任务失败') : null
  };
}
if (typeof module !== 'undefined') module.exports = {recoveryView};
