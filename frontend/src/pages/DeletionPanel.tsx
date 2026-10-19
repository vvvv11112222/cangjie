import { useEffect, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import { newIdempotencyKey } from '../data/actions';
import { describeError, errorCodeText, isTerminalReadError } from '../data/errors';
import { createDeletion, getDeletion } from '../data/m4Api';
import { deletionStatusBadge, formatDateTime } from '../format';
import type { Deletion, Session } from '../types';
import { BadgeTag, Card, IdChip, TechDetails } from '../components/ui';
import { IconAlert } from '../components/icons';

function isTerminal(status: Deletion['status']): boolean {
  return status === 'succeeded' || status === 'failed';
}

export function DeletionPanel({ session, api, onSessionChanged }: {
  session: Session;
  api: boolean;
  onSessionChanged(): void;
}): ReactElement {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [deletion, setDeletion] = useState<Deletion | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pollStopped, setPollStopped] = useState(false);
  const key = useRef(newIdempotencyKey());

  useEffect(() => {
    if (deletion === null || isTerminal(deletion.status) || pollStopped) return;
    let active = true;
    let pending = false;
    const timer = window.setInterval(() => {
      if (pending) return;
      pending = true;
      void getDeletion(deletion.id).then((next) => {
        if (!active) return;
        setDeletion(next);
        if (isTerminal(next.status)) onSessionChanged();
      }).catch((cause: unknown) => {
        if (!active) return;
        const terminal = isTerminalReadError(cause);
        setError(`${describeError(cause)}${terminal ? ' 已停止自动刷新。' : ' 页面会继续尝试读取。'}`);
        if (terminal) setPollStopped(true);
      }).finally(() => { pending = false; });
    }, 2000);
    return () => { active = false; window.clearInterval(timer); };
  }, [deletion?.id, deletion?.status, pollStopped, onSessionChanged]);

  async function submit(): Promise<void> {
    if (reason.trim() === '' || confirmation !== '彻底清理') return;
    setBusy(true); setError(null);
    try {
      const next = await createDeletion(session.id, reason.trim(), key.current);
      setDeletion(next); setOpen(false); setPollStopped(false);
      onSessionChanged();
    } catch (cause) {
      // 请求结果不确定时保留同一个幂等键；再次提交不会创建第二个清理请求。
      setError(describeError(cause));
    } finally {
      setBusy(false);
    }
  }

  const canCreate = session.allowed_actions.includes('delete') && session.status !== 'deleting' && session.status !== 'deleted';

  return (
    <Card
      title="彻底清理课堂"
      subtitle="清理会立即撤销新读取和新处理，并等待数据库、存储及 Worker 副本全部核验。"
      actions={deletion === null ? undefined : <BadgeTag badge={deletionStatusBadge(deletion.status)} />}
    >
      {deletion !== null ? (
        <div className="deletion-status">
          <p>清理请求 <IdChip id={deletion.id} /> 当前状态：<strong>{deletionStatusBadge(deletion.status).label}</strong></p>
          {deletion.status === 'blocked' ? <p className="notice-strip"><IconAlert size={14} aria-hidden="true" />清理受阻，通常表示仍有文件句柄、Worker 临时副本或清理回执尚未核验；不能把它当作完成。</p> : null}
          {deletion.error_code === null ? null : <p className="load-note load-note-error">{errorCodeText(deletion.error_code)}（错误码 {deletion.error_code}）</p>}
          {deletion.completed_at === null ? null : <p className="muted small">完成时间：{formatDateTime(deletion.completed_at)}</p>}
          <TechDetails>
            <ul className="tech-list">
              <li className="mono">deletion.id = {deletion.id}</li>
              <li className="mono">deletion.session_id = {deletion.session_id}</li>
              <li className="mono">deletion.status = {deletion.status}</li>
              <li className="mono">deletion.error_code = {deletion.error_code ?? 'null'}</li>
            </ul>
          </TechDetails>
        </div>
      ) : session.status === 'deleting' ? (
        <p className="notice-strip"><IconAlert size={14} aria-hidden="true" />课堂已经处于清理中。协议没有清理请求列表，刷新页面后无法仅凭课堂记录找回请求编号；请到原操作页面继续查看，或由后端管理员核验。</p>
      ) : session.status === 'deleted' ? (
        <p className="notice-strip">课堂已经完成清理，历史内容不可再读取。</p>
      ) : canCreate ? (
        <>
          <p className="danger-copy">这是不可逆操作。归档只隐藏默认列表；彻底清理会撤销媒体、转写、报告和导出读取，并由后台继续清扫在途文件。</p>
          {!open ? <button type="button" className="btn btn-danger" disabled={!api} onClick={() => setOpen(true)}>发起彻底清理</button> : (
            <div className="deletion-confirm">
              <label className="account-field"><span>清理原因（必填）</span><textarea value={reason} onChange={(event) => setReason(event.target.value)} placeholder="说明为什么需要彻底清理该课堂" /></label>
              <label className="account-field"><span>输入“彻底清理”确认</span><input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} autoComplete="off" /></label>
              <div className="form-actions">
                <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => { setOpen(false); setConfirmation(''); }}>取消</button>
                <button type="button" className="btn btn-danger" disabled={!api || busy || reason.trim() === '' || confirmation !== '彻底清理'} onClick={() => void submit()}>{busy ? '提交中…' : '确认彻底清理'}</button>
              </div>
            </div>
          )}
          {!api ? <p className="load-note">固定样例模式不会提交真实清理请求。</p> : null}
        </>
      ) : (
        <p className="muted">服务端没有授予该课堂的彻底清理操作。</p>
      )}
      {error === null ? null : <p className="load-note load-note-error">{error}</p>}
    </Card>
  );
}
