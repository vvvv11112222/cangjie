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

/*
 * 以下六个图标对应《系统草图.html》侧栏用到的图形（grid / video / calendar /
 * folder / camera / settings）。草图是 HTML 原型、没有可复用的图标集，
 * 这里按本文件统一的 24 视图框、1.7 描边手写一份，不引入图标库。
 */

/** 管理工作台：宫格 */
export function IconGrid(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <rect x="4" y="4" width="7" height="7" rx="1.6" />
      <rect x="13" y="4" width="7" height="7" rx="1.6" />
      <rect x="4" y="13" width="7" height="7" rx="1.6" />
      <rect x="13" y="13" width="7" height="7" rx="1.6" />
    </Glyph>
  );
}

/** 课堂录像：摄像机机身 */
export function IconVideo(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <rect x="3.5" y="6.5" width="12" height="11" rx="2" />
      <path d="M15.5 11l5-3v8l-5-3z" />
    </Glyph>
  );
}

/** 教务与课表：日历 */
export function IconCalendar(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <rect x="4" y="5.5" width="16" height="14" rx="2.5" />
      <path d="M4 10h16" />
      <path d="M8.5 3.5v4M15.5 3.5v4" />
    </Glyph>
  );
}

/** 素材与来源：文件夹 */
export function IconFolder(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M4 7.5A2 2 0 0 1 6 5.5h3.2l1.8 2h7A2 2 0 0 1 20 9.5v7A2 2 0 0 1 18 18.5H6a2 2 0 0 1-2-2z" />
    </Glyph>
  );
}

/** 设备接入：摄像头 */
export function IconCamera(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M4 8.5A2 2 0 0 1 6 6.5h6a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z" />
      <path d="M14 11.5 20 8v8l-6-3.5z" />
      <circle cx="8.5" cy="11.5" r="1.6" />
    </Glyph>
  );
}

/** 运行与设置：齿轮 */
export function IconSettings(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 3.5v2.2M12 18.3v2.2M4.9 7.8l1.9 1.1M17.2 15.1l1.9 1.1M4.9 16.2l1.9-1.1M17.2 8.9l1.9-1.1" />
    </Glyph>
  );
}

/** 账号头像：未登录时的默认人形 */
export function IconUser(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <circle cx="12" cy="8.5" r="3.5" />
      <path d="M5.5 19.5c0-3.3 2.9-5.5 6.5-5.5s6.5 2.2 6.5 5.5" />
    </Glyph>
  );
}

/** 待办提醒：铃铛（对应《系统草图.html》顶栏的通知按钮） */
export function IconBell(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M12 4a5.5 5.5 0 0 0-5.5 5.5c0 4.2-1.2 5.6-1.7 6.1-.3.3-.1.9.4.9h13.6c.5 0 .7-.6.4-.9-.5-.5-1.7-1.9-1.7-6.1A5.5 5.5 0 0 0 12 4z" />
      <path d="M10 19.5a2 2 0 0 0 4 0" />
    </Glyph>
  );
}

/**
 * 品牌标志：摊开的书（与《系统草图.html》左上角 brand-mark 里的 book 图标同形）。
 * 草图是内联 SVG，这里按本文件统一的 24 视图框、1.7 描边写一份。
 */
export function IconBook(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <path d="M12 6c-3-3-7-3-9-2v15c3-1 6 0 9 2 3-2 6-3 9-2V4c-2-1-6-1-9 2v15" />
      <path d="M6 8l3 1M15 9l3-1" />
    </Glyph>
  );
}

/** 搜索：放大镜 */
export function IconSearch(props: IconProps): ReactElement {
  return (
    <Glyph {...props}>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m16 16 4 4" />
    </Glyph>
  );
}

export function IconPlus(props: IconProps): ReactElement {
  return <Glyph {...props}><path d="M12 5v14M5 12h14" /></Glyph>;
}

export function IconUpload(props: IconProps): ReactElement {
  return <Glyph {...props}><path d="M4 15v5h16v-5M12 16V3m-5 5 5-5 5 5" /></Glyph>;
}

export function IconRefresh(props: IconProps): ReactElement {
  return <Glyph {...props}><path d="M20 7V2l-4 4a8 8 0 1 0 3 11M20 7h-5" /></Glyph>;
}
