import type { ReactElement, ReactNode } from 'react';
import type { Badge, Tone } from '../format';
import { shortId } from '../format';
import type { Disclosure } from '../data/viewModel';

export function BadgeTag({ badge }: { badge: Badge }): ReactElement {
  return <span className={`badge badge-${badge.tone}`}>{badge.label}</span>;
}

export function Pill({
  label,
  tone = 'gray',
}: {
  label: string;
  tone?: Tone;
}): ReactElement {
  return <span className={`pill pill-${tone}`}>{label}</span>;
}

export function Card({
  title,
  subtitle,
  actions,
  children,
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  children: ReactNode;
}): ReactElement {
  return (
    <section className="card">
      <header className="card-head">
        <div>
          <h2>{title}</h2>
          {subtitle ? <p className="muted small">{subtitle}</p> : null}
        </div>
        {actions}
      </header>
      <div className="card-body">{children}</div>
    </section>
  );
}

export function DisclosureList({ items }: { items: Disclosure[] }): ReactElement | null {
  if (items.length === 0) {
    return null;
  }
  return (
    <ul className="disclosures">
      {items.map((item, index) => (
        <li key={`${item.tone}-${index}`} className={`disclosure disclosure-${item.tone}`}>
          <span className="disclosure-tag">{item.tone === 'warn' ? '需注意' : '说明'}</span>
          <span>{item.text}</span>
        </li>
      ))}
    </ul>
  );
}

export function IdChip({ id, label }: { id: string; label?: string }): ReactElement {
  return (
    <span className="id-chip mono" title={id}>
      {label ? `${label} ` : ''}
      {shortId(id)}
    </span>
  );
}

export function Field({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}): ReactElement {
  return (
    <div className="field">
      <span className="field-label">{label}</span>
      <span className="field-value">{children}</span>
    </div>
  );
}

export function EmptyState({ text }: { text: string }): ReactElement {
  return <p className="empty">{text}</p>;
}

export function DataSourceNote({
  sources,
  label = '固定样例',
  note = '，未接入后端。',
}: {
  sources: string[];
  /** 来源标签：固定样例 / 接口数据。 */
  label?: string;
  /** 结尾说明；接口模式传"。"。 */
  note?: string;
}): ReactElement {
  return (
    <p className="source-note">
      <span className="source-tag">{label}</span>
      数据来源：
      {sources.map((source) => (
        <code key={source}>{source}</code>
      ))}
      {note}
    </p>
  );
}

/** 加载中/加载失败的一行说明，三个页面共用，避免各自写一套文案。 */
export function LoadStateNote({
  status,
  error,
}: {
  status: 'ready' | 'loading' | 'error';
  error: string | null;
}): ReactElement | null {
  if (status === 'ready') {
    return null;
  }
  if (status === 'loading') {
    return <p className="load-note">正在读取数据……</p>;
  }
  return <p className="load-note load-note-error">{error ?? '读取数据失败。'}</p>;
}
