/**
 * 手写的线性图标集。
 *
 * 为什么不用图标库：仓库的依赖锁只允许在明确评估后新增包，而这里需要的图标数量
 * 很少（导航、卡片标题、阶段状态、错误提示）。手写内联 SVG 不增加依赖、不额外
 * 下载字体，颜色直接继承 currentColor，能和状态色（成功/处理中/失败）自动对齐。
 *
 * 所有图标统一 24 的视图框和 1.7 的描边，保证大小、线宽一致。
 */

import type { ReactElement, ReactNode } from 'react';

export interface IconProps {
  /** 渲染尺寸（正方形边长），默认 16。 */
  size?: number;
  className?: string;
}

function Glyph({ size = 16, className, children }: IconProps & { children: ReactNode }): ReactElement {
  return (
    <svg
      className={className}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {children}
    </svg>
  );
}

/** 教学分析 / 数据看板：折线图 */
export function IconInsights(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M4 20V4" />
      <path d="M4 20h16" />
      <path d="M8 16v-5" />
      <path d="M12.5 16V8" />
      <path d="M17 16v-3" />
    </Glyph>
  );
}

/** 课堂任务：带勾选的清单板 */
export function IconTask(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <rect x="5" y="4" width="14" height="16" rx="2.5" />
      <path d="M9 4V3h6v1" />
      <path d="M9 11.5l2 2 4-4" />
    </Glyph>
  );
}

/** 转写：声波 */
export function IconTranscript(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M5 10v4" />
      <path d="M8.5 7.5v9" />
      <path d="M12 5v14" />
      <path d="M15.5 7.5v9" />
      <path d="M19 10v4" />
    </Glyph>
  );
}

/** 报告：带文本行的文档 */
export function IconReport(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M6 3h7l5 5v13a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1z" />
      <path d="M13 3v5h5" />
      <path d="M8.5 13h7" />
      <path d="M8.5 16.5h4.5" />
    </Glyph>
  );
}

/** 面包屑起点：首页 */
export function IconHome(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M4 10.5 12 4l8 6.5" />
      <path d="M6 10v9h12v-9" />
      <path d="M10.5 19v-5h3v5" />
    </Glyph>
  );
}

/** 列表卡片标题 */
export function IconList(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M8 6h12" />
      <path d="M8 12h12" />
      <path d="M8 18h12" />
      <circle cx="4.5" cy="6" r="1" />
      <circle cx="4.5" cy="12" r="1" />
      <circle cx="4.5" cy="18" r="1" />
    </Glyph>
  );
}

/** 最近批次标题：时钟 + 回溯 */
export function IconHistory(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <circle cx="12" cy="12" r="8" />
      <path d="M12 8v4.3l3 1.7" />
    </Glyph>
  );
}

/** 成功 / 已完成 */
export function IconCheck(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M8.5 12.2l2.4 2.4 4.6-5" />
    </Glyph>
  );
}

/** 处理中 */
export function IconClock(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5V12l3 1.8" />
    </Glyph>
  );
}

/** 失败 / 异常 */
export function IconAlert(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.6v5" />
      <path d="M12 16.2h.01" />
    </Glyph>
  );
}

/** 等待 / 跳过 */
export function IconPause(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M10.5 9.5v5" />
      <path d="M13.5 9.5v5" />
    </Glyph>
  );
}

/** 中性状态：实心圆点。用于草稿、已归档、排队这类"既不是成功也不是失败"的状态。 */
export function IconDot(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <circle cx="12" cy="12" r="4" fill="currentColor" stroke="none" />
    </Glyph>
  );
}

/** 说明 / 接口数据 */
export function IconInfo(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 11v5" />
      <path d="M12 8.2h.01" />
    </Glyph>
  );
}

/** 课堂行首播放标记 */
export function IconPlay(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M9 7.5l7 4.5-7 4.5z" />
    </Glyph>
  );
}

/** 阶段之间的顺序连接 */
export function IconChevronRight(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M9.5 6.5 15 12l-5.5 5.5" />
    </Glyph>
  );
}

/** 展开 / 收起 */
export function IconChevronDown(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M6.5 9.5 12 15l5.5-5.5" />
    </Glyph>
  );
}

/** 允许操作 / 授权 */
export function IconShield(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M12 3.5 5.5 6v5.5c0 4 2.7 7 6.5 8.5 3.8-1.5 6.5-4.5 6.5-8.5V6z" />
      <path d="M9.2 12.2l2 2 3.6-4" />
    </Glyph>
  );
}
