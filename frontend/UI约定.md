# 前端 UI 约定

这份文件只讲**展示层怎么写**，目的是新增页面时不用重新设计，直接沿用同一套颜色、间距和组件。
接口、字段、枚举、权限和错误码仍然以 [开发协议](../docs/开发协议.md) 与 [Schema](../contracts/v1.schema.json) 为准，本文件不作规定。

技术栈：React 19 + Vite + TypeScript，样式是唯一的全局文件 `src/styles.css`，没有 CSS 框架、没有 UI 组件库、没有图标库。

## 1. 颜色和形状只用 `:root` 变量

所有颜色、圆角、阴影都定义在 `src/styles.css` 的 `:root` 里。**页面和组件里不要写十六进制颜色**，一律写成 `var(--xxx)`。

常用变量：

| 用途 | 变量 |
| --- | --- |
| 侧边栏、主按钮 | `--brand`（悬停 `--brand-hover`） |
| 强调色：选中、运行中 | `--accent`、`--accent-soft`、`--accent-text` |
| 页面背景 / 卡片 | `--bg`、`--card` |
| 边框 | `--line`（更明显的一档 `--line-strong`） |
| 文字 | `--ink`（主）、`--muted`（次）、`--faint`（字段名、编号）、`--ink-body`（正文段落） |
| 悬浮面 / 下沉面 | `--surface-hover`、`--surface-sunken` |
| 圆角 | `--radius-card`（卡片）、`--radius-control`（按钮、输入框） |
| 卡片阴影 | `--shadow-card` |

需要新颜色时，先在 `:root` 里加一个带注释的变量，再引用它；不要在页面里就地写死。

## 2. 状态一律用 `Tone`

状态色只有 6 档，定义在 `src/format.ts` 的 `Tone`：

| 色调 | 含义 | 图标 |
| --- | --- | --- |
| `green` | 成功、已完成、就绪 | 圆勾 |
| `teal` | 运行中、分析中 | 时钟 |
| `amber` | 需注意：部分完成、证据不足、复核中 | 时钟 |
| `red` | 失败、已删除、已撤回 | 感叹号 |
| `gray` | 排队、跳过、取消、草稿、已归档 | 圆点 |
| `blue` | 中性信息：观察、ASR、示范数据 | 信息 |

渲染用 `BadgeTag`（带图标，用于状态）或 `Pill`（纯文字，用于分类和操作标签），不要在页面里手写 `color: green` 这类样式。

新增一种业务状态时：先在 `format.ts` 对应的映射表里加一条，再从上面 6 档里选一个色调。**不要新增颜色。**

**页面上的错误和限制怎么显示**：带错误码和失败阶段的结构化错误用 `RunErrorBox`；一行说明性提示（批次限制、结果限制、批次读取失败、轮询失败）用 `.notice-strip`。这两种就是全部，不要自己再拼第三种提示样式。

## 3. 新页面怎么加

1. 在 `src/App.tsx` 的 `NAV_ROUTES` 加一条路由，在 `NAV_ICONS` 配上图标，在 `PAGES` 注册页面组件——导航、路由、面包屑、侧栏和顶栏会自动带上；
2. 页面最外层用 `<div className="page">`；
3. 标题区用 `<header className="page-head">`，里面是 `eyebrow`（小字）+ `h1`（页面名）+ 一句说明；
4. 内容分区用 `Card`（可选 `icon`、`subtitle`、`actions`）；
5. 字段用 `Field`，编号用 `IdChip`，空态用 `EmptyState`，加载/失败用 `LoadStateNote`。

## 4. 技术字段放进 `TechDetails`

协议字段名（`session.latest_run_id` 这种）、内容哈希、锁版本，都放进 `<TechDetails>` 折叠区，正文只展示人能读懂的信息。

**不要删除任何字段**，只是把它的位置从主视线下沉到折叠区。页面上仍要保留真实错误码和失败阶段。

## 5. 按钮与输入控件

| 场景 | 写法 |
| --- | --- |
| 主操作（一个区域最多一个） | `.btn .btn-primary` |
| 次操作 | `.btn .btn-secondary` |
| 危险、不可逆操作 | `.btn .btn-danger`，并且放在最右，和主操作拉开距离 |

按钮必须对应真实功能。没有接口支撑的操作不要做成按钮，也不要为了好看加按钮。

输入框和下拉框都放进 `.account-field`，尺寸、边框、圆角和聚焦态由它统一提供——`input` 和 `select` 必须长得一样。新增别的控件类型（日期、多选等）时，记得一起补 `:focus` 态，否则会露出浏览器原生外观。

## 6. 窄屏

全站只有一个断点：`@media (max-width: 900px)`，规则是"侧栏变成顶部导航条"。

新页面只需要保证两件事：

- 表格套在 `.table-wrap` 里（内部横向滚动，不撑破页面）；
- 横向排列的卡片用 flex 并允许 `overflow-x: auto`（参考 `.stage-flow`）。

**整页不允许出现横向滚动条。** 改完在 1280 / 1024 / 820 三档宽度各看一眼。

## 7. 复用与依赖

- 公共组件都在 `src/components/ui.tsx`，图标在 `src/components/icons.tsx`；
- 两个页面用到同一块 UI 时，抽到 `ui.tsx`，不要在页面里复制一遍；
- 不新增依赖。需要图标就手写内联 SVG（参考 `icons.tsx` 的写法）。

## 8. 提交前

在 `frontend/` 目录运行：

```powershell
npm run check
```

它会依次做类型检查、单元测试和构建。

`src/pages/pages.test.tsx` 里断言的页面文案（例如"阶段完成 4/6"）不要为了视觉好看删改；确实要改文案时，测试一起改，并在提交说明里写清楚。
