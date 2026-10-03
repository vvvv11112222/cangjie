# 教学质量管理系统

文档 v0.6，2026-10-03。当前仓库包含开发文档、接口样例和数据库脚本，业务服务待实现。成员直接在本仓库维护文件。

## 从这里开始

全员先读[项目文档](项目文档.md)与[系统架构](docs/系统架构设计.md)，按[开发与验收计划](docs/开发与验收计划.md)推进 M0～M4。

[系统草图](系统草图.html)供前端参考布局，正式实现以当前需求和协议为准，页面可自行设计。

| 成员 | 重点资料 | 首个联调目标 |
| --- | --- | --- |
| 前端 | [开发协议](docs/开发协议.md)、[接口样例](contracts/examples/manifest.json) | 用固定样例显示任务、转写和报告 |
| 后端 | 开发协议、[数据库设计](docs/数据库设计.md) | Go API、任务流转和权限校验 |
| 数据库 | 数据库设计、[001](database/001_initial_schema.sql)、[002](database/002_review_baseline.sql)、[003](database/003_review_fixes.sql) | 初始化、迁移及约束检查 |
| 音频 | 开发协议第3节、audio-result 样例 | Worker 公共入口和带时间戳转写 |
| 视频 | 开发协议第3节、probe-result/video-result 样例 | 媒体检查、播放代理和关键帧 |

## 文件怎么用

- [AGENTS.md](AGENTS.md)：Codex 的仓库开发约定，包含按任务查阅文档、模块边界、验证及协作规则。
- `docs/`：架构、协议、数据库、验收计划。
- `contracts/v1.schema.json`、[端点映射](contracts/endpoints.json)与 `contracts/examples/`：契约1.1及联调样例；api-cases.json 中每个 value 是独立 DTO，按 definition 选择。前端和 Worker 须同步更新，不能混用1.0结果。
- `database/`：迁移和约束验证；新库依次执行001、002、003，旧库只执行未应用迁移。
- `tools/`：文档/协议检查、隔离数据库检查。
- [.env.example](.env.example)：配置模板，复制为本地 `.env` 后填写。

在仓库根目录运行（需安装 Python jsonschema）：

```powershell
python tools/check_docs.py
python -m unittest discover -s tools -p test_quality.py -v
node tools/check_prototype.mjs
```

数据库验证命令见[数据库设计第6节](docs/数据库设计.md#6-执行与验证)。

真实 ASR 评测入口为 `python tools/check_quality.py <受控本地评测.json>`，格式及已确认的暂定门槛见[验收计划](docs/开发与验收计划.md#3-样本与质量验证)。本仓库没有真实授权样本或可启动的 Go/React/Worker 服务；检查通过仅说明相应契约、SQL、草图行为或度量工具通过，不能代替业务及模型验收。

## 使用 Codex

在 Codex 中打开克隆后的 Git 仓库根目录并新建任务，确认已读取 `AGENTS.md`；已有会话可直接要求重新读取。仓库级指令的加载方式见 [OpenAI 官方说明](https://learn.chatgpt.com/docs/agent-configuration/agents-md)。

成员可用下面的格式开始任务：

> 我负责【前端/后端/数据库/音频/视频】，本次实现【FR编号与具体功能】，使用【分支名】。请按 AGENTS.md 查阅相关文档，完成【交付项】并验证【验收条件】，说明需要其他模块配合的部分。
