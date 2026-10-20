# 课堂语言分析 demo

⑥号成员“AI 语言分析工程师 + 测试与文档”的独立验证模块。Go 后端与原生 HTML/CSS/JavaScript 页面完成“视频 → 音频 → ASR → 阅读整理 → 教学事件 → 六维报告 → 全文检查”。

当前可供队长审查与接入，尚未完成真实中文课堂质量、主项目集成及 Linux/Docker 运行验收。交付边界见 [HANDOFF.md](HANDOFF.md)。

## 当前版本与功能

截至 2026-10-20：报告 `six-dim-coaching-v8`，正文 `bounded-narrative-v4`，建议 `grounded-advice-v7`，全文检查 `context-review-v9`。实际运行版本以 `GET /api/health` 为准。

- 原始 ASR、人工转写修订与报告快照分别保存；文本重分析复用转写，不重复 ASR。
- 六维分别为内容组织、教学推进、思维引导、解释表达、活动组织、工具支持；证据不足时明确说明，不给教师打分。
- 逐字引文、教学事件、报告结论与回放关联；失败候选保留诊断，不冒充正式报告。
- 左侧目录分区阅读，课堂事实、教学分析、修改方向与实施后观察方法直接展示。
- 教学推进附教师讲解思路，按已有事件时间排列，不额外推断教学意图，不把预告当作已完成。
- 全文检查保留完整正文并标明具体疑点；人工校对单独记录，模型检查不等于教师认可。
- 后台任务支持进度、取消、人工重试、历史版本、JSON 与易读 Markdown 导出。

维度说明见 [SIX-DIMENSIONS.md](SIX-DIMENSIONS.md)，分段与时间限制见 [SEGMENTATION.md](SEGMENTATION.md)。

## 首次配置

需要 Go 1.26+、FFmpeg、FFprobe；前端无需 npm 或单独开发服务器。测试需要 Node.js，本机建议使用 `D:\Program Files\nodejs\node.exe`。

仅首次且不存在 `.env` 时，复制 `.env.example` 为 `.env`，填写密钥；已有配置不要覆盖。进程环境变量优先于文件。

| 配置 | 用途 |
| --- | --- |
| `DASHSCOPE_API_KEY` | 百炼密钥，仅保存在本地 |
| `BAILIAN_BASE_URL` | 默认百炼兼容接口；专属地址以控制台为准 |
| `BAILIAN_ASR_MODEL` / `BAILIAN_TEXT_MODEL` | 默认 qwen3-asr-flash / qwen-plus |
| `BAILIAN_ASR_LANGUAGE_HINTS` | 默认 zh,en，按素材语言调整 |
| `BAILIAN_REPORT_AUDIT` | 默认 1，额外调用一次文本模型全文检查；0 表示未启用 |
| `FFMPEG_PATH` / `FFPROBE_PATH` | 程序名或绝对路径，含空格时用双引号包裹 |
| `DEMO_HOST` / `DEMO_PORT` | 默认 127.0.0.1:8090 |
| `DEMO_DATA_DIR` | 默认 data，保存任务、历史和媒体 |

## 启动与更新

在 demo 根目录执行：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\start.ps1
```

打开 [http://127.0.0.1:8090](http://127.0.0.1:8090)。脚本统一编译并启动 `bin/teacher-report-demo.exe`。只编译可用 `go build -o bin/teacher-report-demo.exe .`，开发可用 `go run .`。

端口被占用时，确认旧服务没有正在处理的任务，在原窗口按 Ctrl+C 停止后再启动。刷新页面不会切换后端；提示词、标准及网页嵌入程序，修改后需要重新编译启动。一个数据目录只运行一个服务。

Linux 配置环境后用 `sh scripts/start.sh`。容器可用 `docker compose up --build -d`，日志用 `docker compose logs -f demo`；国内构建可加 GOPROXY 构建参数。Compose 仅绑定宿主机本地端口，使用命名数据卷；停止用 `docker compose down`，不要添加删除卷的 `-v`。Linux/Docker 实际运行尚未验收。

## 教师怎样使用

1. 上传已获准使用、不超过 10 分钟且不超过 256MB 的视频，确认模型调用费用。
2. 先读课堂概览，再通过左侧目录查看六维反馈，区分事实、分析、修改方向及实施后观察方法。
3. 用合并的原文回放入口核对依据。时间是最长 40 秒的音频块范围，不是句子或词级对齐。
4. 遇到待确认标记，结合旁边原因与原文判断；没有提醒也不表示专业内容已验收。
5. 转写有错时填写修订与原因；保留旧版，只重跑阅读、事件、报告及启用时的全文检查。
6. “重新生成六维分析”需单独确认文本费用，生成新报告；刷新不会重新生成。
7. “分析校对”只记录事实、分析、建议的人工判断，不自动修改报告、重新生成或训练模型；当前身份未经认证。

失败或部分完成时仍可查看已有转写与有效事件，失败候选不是正式报告。旧转写有缺口时，文本重跑不会补齐音频；可用原视频创建独立转写任务。系统不自动重试付费请求。

## 数据与能力边界

data/state.json、data/media/ 包含课堂文本、录像和诊断，当前不自动清理。备份时先停止服务，再复制整个数据目录。启动时已有排队任务可能继续执行，之前运行中断的任务需人工确认重试。

没有接入主项目的账号权限、PostgreSQL、正式审核发布，不应直接公开部署。仅分析音频转写，视频用于回看；不判断板书、学生表情、真实参与率、设备使用效果或口音。非零音轨起始偏移、整堂长视频和句级定位误差尚待验证。

## 测试

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\test.ps1 -NodeExecutable 'D:\Program Files\nodejs\node.exe'
```

Linux 用 `sh scripts/test.sh`。脚本运行 Go 测试、静态检查、前端单元测试和语法检查，强制关闭真实模型测试。缺少 FFmpeg/FFprobe 时，合成媒体相关测试会明确跳过。

三组浏览器测试为 tests/analysis.browser.cjs、tests/jobs.browser.cjs、tests/recovery.browser.cjs，使用本地模拟接口。需要 Playwright 与 Chromium，可用 PLAYWRIGHT_MODULE、BROWSER_EXECUTABLE 指定安装路径；截图写入 bin/qa/。

普通回归依赖的固定公开样本随源码放在 testdata/public-courses/，不需要本机 evaluation/。真实模型测试须单独显式启用，运行前阅读调用次数及费用提示。结果见 [TEST-REPORT.md](TEST-REPORT.md)，后续真实质量测量见 [EVALUATION.md](EVALUATION.md)。

## 给队长的源码快照

```powershell
& 'D:\Program Files\nodejs\node.exe' scripts/prepare-delivery.cjs --check
& 'D:\Program Files\nodejs\node.exe' scripts/prepare-delivery.cjs --export
```

--check 检查明确清单、固定资料与常见密钥格式；--export 复制到 bin/delivery/<本次编号>/source/，同级保存大小及 SHA256 清单。它不初始化 Git、提交或推送。在生成的 source/ 内再执行普通测试和编译。

交付不包含本地配置、任务、视频、二进制、缓存、实验归档及历史工作笔记；必要公开测试资料保留来源与许可。检查不替代实际 PR 的人工审查。

evaluation/ 保留本机实验与中文验收页面，docs/history/ 保留整理前说明，均不进入源码快照。接入顺序与提交注意事项见 HANDOFF.md。

## 主项目适配与自动验收

新增 integration/reportadapter 离线 Go 包和 cmd/adapt-report：把 demo 证据转换为服务端登记的 UUID，补齐实际 coverage，保留完整建议及原报告扩展；不会直接写入主项目。固定协议、输入要求和运行方式见 [integration/README.md](integration/README.md)。

运行 powershell -ExecutionPolicy Bypass -File scripts/test-integration.ps1 可检查正式 Schema、证据绑定、冻结参考和队长的原版质量计算器；需 Python 与 jsonschema。真实中文素材尚缺，合成指标不作为质量验收。独立进程和 Docker 冒烟脚本也见该说明。
