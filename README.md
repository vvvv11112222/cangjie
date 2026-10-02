# 教学质量管理系统

文档 v0.5，2026-10-02。当前仓库包含开发文档、接口样例和数据库脚本，业务服务待实现。成员直接在本仓库维护文件。

## 从这里开始

全员先读[项目文档](项目文档.md)与[系统架构](docs/系统架构设计.md)，按[开发与验收计划](docs/开发与验收计划.md)推进 M0～M4。

[系统草图](系统草图.html)供前端参考布局，正式实现以当前需求和协议为准，页面可自行设计。

| 成员 | 重点资料 | 首个联调目标 |
| --- | --- | --- |
| 前端 | [开发协议](docs/开发协议.md)、[接口样例](contracts/examples/manifest.json) | 用固定样例显示任务、转写和报告 |
| 后端 | 开发协议、[数据库设计](docs/数据库设计.md) | Go API、任务流转和权限校验 |
| 数据库 | 数据库设计、[001](database/001_initial_schema.sql)与[002](database/002_review_baseline.sql) | 初始化、迁移及约束检查 |
| 音频 | 开发协议第3节、audio-result 样例 | Worker 公共入口和带时间戳转写 |
| 视频 | 开发协议第3节、probe-result/video-result 样例 | 媒体检查、播放代理和关键帧 |

## 文件怎么用

- `docs/`：架构、协议、数据库、验收计划。
- `contracts/v1.schema.json` 与 `contracts/examples/`：直接维护的接口定义与联调样例，变更时同步修改并检查。
- `database/`：迁移和约束验证；新库依次执行001、002。
- `tools/`：文档/协议检查、隔离数据库检查。
- [.env.example](.env.example)：配置模板，复制为本地 `.env` 后填写。

在仓库根目录运行（需安装 Python jsonschema）：

```powershell
python tools/check_docs.py
```

数据库验证命令见[数据库设计第6节](docs/数据库设计.md#6-执行与验证)。
