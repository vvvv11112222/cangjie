/**
 * 顶栏的待办提醒（对应《系统草图.html》顶栏的 notification-button + 「工作提醒」弹窗）。
 *
 * 草图的提醒内容是写死的示例文案；这里改成**从已加载的课堂数据里算出来的真实条目**：
 *   待上传 = 还没有主媒体；分析中 = 最新批次排队或运行；
 *   待复核 = 批次已结束但还没有当前报告（和课堂档案页签同一套口径）；
 *   分析失败 = 最新批次失败。
 *
 * 数据来自 useSessionTasks：它和课堂档案页是同一个取数入口（接口模式下会各自请求一次
 * /sessions，等页面多了再抽成共享缓存，现在保持简单）。
 * 保留策略之类的运维提醒不在这里编造——那是「运行与设置」页接 /operations 之后的事。
 */

import { useEffect, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import { useSessionTasks } from '../data/hooks';
import { IconAlert, IconBell, IconInfo } from './icons';

interface Reminder {
  key: string;
  text: string;
  count: number;
}

export function NotificationBell(): ReactElement {
  const loaded = useSessionTasks();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);

  // 和账号面板一致：点外面或按 Esc 关掉，别让浮层一直挡着页面。
  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const onPointerDown = (event: MouseEvent): void => {
      if (wrapRef.current !== null && !wrapRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [open]);

  useEffect(() => {
    setError(loaded.status === 'error' ? (loaded.error ?? '读取课堂数据失败。') : null);
  }, [loaded.status, loaded.error]);

  const tasks = loaded.data.tasks;
  const reminders: Reminder[] = [
    {
      key: 'empty',
      text: '个课堂还没有主媒体，上传后才能提交分析',
      count: tasks.filter((task) => task.session.primary_media_asset_id === null).length,
    },
    {
      key: 'processing',
      text: '个批次正在分析中',
      count: tasks.filter(
        (task) => task.run !== null && (task.run.status === 'queued' || task.run.status === 'running'),
      ).length,
    },
    {
      key: 'review',
      text: '个课堂的分析已结束，还没有当前报告',
      count: tasks.filter(
        (task) =>
          task.session.primary_media_asset_id !== null &&
          task.session.current_report_id === null &&
          task.run !== null &&
          task.run.status !== 'queued' &&
          task.run.status !== 'running',
      ).length,
    },
    {
      key: 'failed',
      text: '个批次失败，需要查看失败阶段与错误码',
      count: tasks.filter((task) => task.run?.status === 'failed').length,
    },
  ].filter((item) => item.count > 0);
  const total = reminders.reduce((sum, item) => sum + item.count, 0);

  return (
    <div className="bell-wrap" ref={wrapRef}>
      <button
        type="button"
        className={`icon-button${total > 0 ? ' notification-button' : ''}`}
        aria-label={total > 0 ? `查看待办提醒（${total} 条）` : '查看待办提醒'}
        aria-expanded={open}
        aria-haspopup="true"
        aria-controls="notification-panel"
        onClick={() => setOpen((value) => !value)}
      >
        <IconBell size={17} />
      </button>

      {open ? (
        <div className="account-menu" id="notification-panel" role="dialog" aria-label="待办提醒">
          <p className="account-menu-title">待办提醒</p>
          <p className="account-menu-sub">
            按当前角色的可见范围统计，口径与课堂档案的状态页签一致。
          </p>
          {error === null ? null : (
            <p className="notice-strip">
              <IconAlert size={14} aria-hidden="true" />
              {error}
            </p>
          )}
          {reminders.length === 0 ? (
            <p className="notice-list-empty">
              <IconInfo size={14} aria-hidden="true" />
              {loaded.status === 'signed_out' ? '登录后显示你范围内的待办。' : '暂无待处理任务。'}
            </p>
          ) : (
            <div className="notice-list">
              {reminders.map((item) => (
                <div className="notice" key={item.key}>
                  <strong>{item.count} {item.text}</strong>
                </div>
              ))}
            </div>
          )}
        </div>
      ) : null}
    </div>
  );
}
