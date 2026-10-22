import { useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { requestJson } from '../data/http';
import { ApiError, describeError } from '../data/errors';

export type DeletableResource = 'org-units' | 'class-groups' | 'courses' | 'terms' | 'classrooms' | 'offerings' | 'schedules';

function message(error: unknown): string {
  return error instanceof ApiError && (error.code === 'INVALID_STATE' || error.code === 'INVALID_ARGUMENT') && typeof error.details?.reason === 'string'
    ? error.details.reason : describeError(error);
}

export function DeleteUnusedButton({ resource, id, name, disabled = false, onDeleted }: {
  resource: DeletableResource; id: string; name: string; disabled?: boolean; onDeleted(): void | Promise<void>;
}): ReactElement {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function remove(): Promise<void> {
    if (busy || disabled || !window.confirm(`确认删除“${name}”？\n仅能删除未被使用的资料；关联资料和授课历史不会一起删除。`)) return;
    setBusy(true); setError(null);
    try { await requestJson(`/${resource}/${encodeURIComponent(id)}`, { method: 'DELETE', csrf: true }); await onDeleted(); }
    catch (cause) { setError(message(cause)); }
    finally { setBusy(false); }
  }
  return <span><button type="button" className="btn btn-secondary btn-sm" disabled={disabled || busy} onClick={() => void remove()}>{busy ? '删除中…' : '删除'}</button>{error && <span role="alert" className="load-note load-note-error">{error}</span>}</span>;
}

/** Kept inside the existing account menu/editor; no navigation redesign. */
export function PasswordForm({ userId, onDone, onCancel }: { userId?: string; onDone(): void | Promise<void>; onCancel(): void }): ReactElement {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [again, setAgain] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function submit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault(); if (busy) return;
    const bytes = new TextEncoder().encode(next).length;
    if (bytes < 12 || bytes > 72) { setError('新密码须为12至72字节，建议使用字母、数字和符号。'); return; }
    if (next !== again) { setError('两次输入的新密码不一致。'); return; }
    setBusy(true); setError(null);
    try {
      await requestJson(userId ? `/users/${encodeURIComponent(userId)}/password` : '/auth/password', {
        method: 'POST', csrf: true, body: userId ? { new_password: next } : { current_password: current, new_password: next },
      });
      setCurrent(''); setNext(''); setAgain(''); await onDone();
    } catch (cause) { setError(message(cause)); }
    finally { setBusy(false); }
  }
  return <form className="accounts-editor-form" aria-label={userId ? '重置账号密码' : '修改自己的密码'} onSubmit={event => void submit(event)}>
    <h3>{userId ? '重置账号密码' : '修改自己的密码'}</h3>
    <p className="muted small">保存后该账号所有已登录设备将退出，须使用新密码登录。</p>
    {!userId && <label className="account-field">当前密码<input type="password" autoComplete="current-password" value={current} onChange={e => setCurrent(e.target.value)} required disabled={busy}/></label>}
    <label className="account-field">新密码<input type="password" autoComplete="new-password" value={next} onChange={e => setNext(e.target.value)} required disabled={busy}/></label>
    <label className="account-field">再次输入新密码<input type="password" autoComplete="new-password" value={again} onChange={e => setAgain(e.target.value)} required disabled={busy}/></label>
    {error && <p role="alert" className="load-note load-note-error">{error}</p>}
    <div className="form-actions"><button type="button" className="btn btn-secondary" disabled={busy} onClick={onCancel}>取消修改密码</button><button className="btn btn-primary" disabled={busy}>{busy ? '保存中…' : '保存新密码'}</button></div>
  </form>;
}
