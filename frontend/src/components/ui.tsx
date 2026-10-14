import { useState } from 'react';
import type { ReactElement, ReactNode } from 'react';
import type { JobStatus, Run } from '../types';
import type { Badge, Tone } from '../format';
import { jobStageLabel, jobStatusBadge, runActionLabel, shortId } from '../format';
import type { Disclosure } from '../data/viewModel';
import type { LoadStatus } from '../data/loadState';
import {
  IconAlert,
  IconCheck,
  IconChevronDown,
  IconChevronRight,
  IconClock,
  IconDot,
  IconInfo,
  IconPause,
} from './icons';
import type { IconProps } from './icons';

/**
 * 状态标签的图标按色调固定。成功、处理中、失败三档在颜色、图标、文字上都能区分，
 * 等待/取消/跳过统一用中性灰加暂停图标，不会和"失败"混淆。
 */
const BADGE_ICON: Record<Tone, (props: IconProps) => ReactElement> = {
  green: IconCheck,
  teal: IconClock,
  amber: IconClock,
  red: IconAlert,
  gray: IconDot,
  blue: IconInfo,
};

export function BadgeTag({ badge }: { badge: Badge }): ReactElement {
  const Icon = BADGE_ICON[badge.tone];
  return (
    <span className={`badge badge-${badge.tone}`}>
      <Icon size={13} />
      {badge.label}
    </span>
  );
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
  icon,
  actions,
  children,
}: {
  title: string;
  subtitle?: string;
  /** 卡片标题左侧的小图标；不传则只显示文字标题。 */
  icon?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}): ReactElement {
  return (
    <section className="card">
      <header className="card-head">
        <div className="card-heading">
          {icon ? <span className="card-icon">{icon}</span> : null}
          <div>
            <h2>{title}</h2>
            {subtitle ? <p className="muted small">{subtitle}</p> : null}
          </div>
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

/**
 * 数据来源提示条。默认只显示一行来源；"展开"才显示口径说明，
 * 展开状态只是本地显隐，不请求任何接口。
 */
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
  const [open, setOpen] = useState(false);
  return (
    <div className="source-note">
      <IconCheck size={16} className="source-icon" />
      <p className="source-line">
        <span className="source-tag">{label}</span>
        数据来源：
        {sources.map((source) => (
          <code key={source}>{source}</code>
        ))}
        {note}
      </p>
      <button
        type="button"
        className="source-toggle"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        {open ? '收起' : '展开'}
        <IconChevronDown size={14} className={open ? 'is-open' : undefined} />
      </button>
      {open ? (
        <p className="source-detail">
          页面只呈现读取到的字段：课堂、最新批次、当前报告编号都按接口原值展示，
          数据形状以 contracts/v1.schema.json 与 docs/开发协议.md 为准。
          按钮按各资源返回的 allowed_actions 展示，能否执行仍由服务端逐次授权。
        </p>
      ) : null}
    </div>
  );
}

/** 加载中/加载失败的一行说明，三个页面共用，避免各自写一套文案。 */
export function LoadStateNote({
  status,
  error,
}: {
  status: LoadStatus;
  error: string | null;
}): ReactElement | null {
  if (status === 'ready') {
    return null;
  }
  if (status === 'loading') {
    return <p className="load-note">正在读取数据……</p>;
  }
  if (status === 'signed_out') {
    return <p className="load-note">当前未登录，登录成功后会自动读取数据。</p>;
  }
  if (status === 'paused') return <p className="load-note">{error}</p>;
  return <p className="load-note load-note-error">{error ?? '读取数据失败。'}</p>;
}

export function LoadControls({ actionLabel, loadMore }: { actionLabel: string | null; loadMore(): void }): ReactElement | null {
  return actionLabel === null ? null : <button type="button" className="btn btn-secondary btn-sm" onClick={loadMore}>{actionLabel}</button>;
}

/* ==================== 分析批次相关的公共组件 ==================== */

/**
 * 技术详情折叠区。本项目按协议原样展示字段名，但字段名不该占着业务视线：
 * 统一收进这里，默认折叠、点开可查，信息一个都不少。
 */
export function TechDetails({
  summary = '技术详情',
  children,
}: {
  summary?: string;
  children: ReactNode;
}): ReactElement {
  return (
    <details className="tech-details">
      <summary>{summary}</summary>
      <div className="tech-body">{children}</div>
    </details>
  );
}

/**
 * 阶段卡片的色调：成功绿、运行中青绿、失败红，排队/跳过/取消一律中性灰。
 * "跳过"是中性状态，不能按失败着色——它和失败在语义上完全不同。
 */
const STAGE_TONE: Record<JobStatus, string> = {
  succeeded: 'ok',
  running: 'doing',
  failed: 'bad',
  queued: 'idle',
  skipped: 'idle',
  cancelled: 'idle',
};

function stageIcon(status: JobStatus | null): ReactElement {
  if (status === 'succeeded') {
    return <IconCheck size={16} />;
  }
  if (status === 'running') {
    return <IconClock size={16} />;
  }
  if (status === 'failed') {
    return <IconAlert size={16} />;
  }
  return <IconPause size={16} />;
}

/** 六个阶段按 stage_plan 的顺序横向排列，课堂任务页和课堂详情页共用一套。 */
export function StageFlow({ run }: { run: Run }): ReactElement {
  return (
    <ol className="stage-flow">
      {run.stage_plan.map((stage, index) => {
        const job = run.jobs.find((item) => item.stage === stage) ?? null;
        const tone = job === null ? 'idle' : STAGE_TONE[job.status];
        return (
          <li key={stage} className="stage-item">
            {index > 0 ? (
              <span className="stage-connector" aria-hidden="true">
                <IconChevronRight size={14} />
              </span>
            ) : null}
            <div className={`stage stage-${tone}`}>
              <span className="stage-icon" aria-hidden="true">{stageIcon(job?.status ?? null)}</span>
              <p className="stage-name">
                {index + 1}. {jobStageLabel(stage)}
              </p>
              {job === null ? (
                <span className="muted small">阶段计划中未返回任务</span>
              ) : (
                <>
                  <BadgeTag badge={jobStatusBadge(job.status)} />
                  <span className="stage-meta mono">
                    尝试 {job.attempts}/{job.max_attempts}
                    {job.status === 'running' ? ` · ${job.progress}%` : ''}
                  </span>
                  {job.error_code === null ? null : (
                    <span className="stage-error-code mono">{job.error_code}</span>
                  )}
                </>
              )}
            </div>
          </li>
        );
      })}
    </ol>
  );
}

/** 批次的三项协议字段：没有错误时直接显示，有错误时收进错误区的技术详情。 */
export function RunFields({ run }: { run: Run }): ReactElement {
  return (
    <div className="field-grid">
      <Field label="run.error_code">
        <span className={run.error_code === null ? 'muted' : 'danger'}>{run.error_code ?? 'null'}</span>
      </Field>
      <Field label="run.report_id">
        <span className="muted">{run.report_id ?? 'null'}</span>
      </Field>
      <Field label="run.allowed_actions">
        {run.allowed_actions.length === 0 ? '无' : run.allowed_actions.map(runActionLabel).join('、')}
      </Field>
    </div>
  );
}

/**
 * 批次错误区，课堂任务页和课堂详情页共用。
 * 只展示后端真实返回的错误码，不推断根因：失败阶段从 jobs 里 status=failed 的
 * 条目推出，推不出来就明说"后端未给出失败阶段"，而不是编一个。
 */
export function RunErrorBox({ run }: { run: Run }): ReactElement {
  const failed = run.jobs.filter((job) => job.status === 'failed');
  return (
    <div className="error-box">
      <div className="error-head">
        <span className="error-icon" aria-hidden="true"><IconAlert size={16} /></span>
        <span className="error-title">任务错误信息</span>
        <span className="error-code mono">{run.error_code}</span>
        {failed.length === 0 ? (
          <span className="error-stage muted">后端未给出失败阶段</span>
        ) : (
          <span className="error-stage">
            失败阶段：{failed.map((job) => jobStageLabel(job.stage)).join('、')}
          </span>
        )}
      </div>
      <TechDetails>
        <RunFields run={run} />
        <ul className="tech-list">
          {run.jobs
            .filter((job) => job.error_code !== null)
            .map((job) => (
              <li key={job.id} className="mono">
                {job.stage} · {job.status} · {job.error_code} · 尝试 {job.attempts}/{job.max_attempts}
              </li>
            ))}
        </ul>
      </TechDetails>
    </div>
  );
}
