# 2026-10-14 视频 Worker 团队交付

## 本轮完成

处理器版本 `cangjie-video-worker-1.0.6`。基于主分支 `133ea5a` 继续使用现有视频处理及视觉算法；不修改前端、Go 业务代码、数据库迁移或公共协议。联调后端固定为 `fe03d472fbfd2ec1e15fb868ff0b953795525953`（媒体阶段已整合后续分析、报告、复核和治理）。

- 正式上传→媒体准备→代理回放→关键帧→Go 画面证据重新验证。
- 实现后端已定义的 Worker 删除清理轮询及回执；默认关闭，兼容旧后端。
- 每个临时根目录使用操作系统独占锁，媒体子进程退出后才可清理；不能从另一个进程抢跑清理。
- 只清理 Go 指定 job_ids 对应的 UUID/UUID 租约目录；异常内容、符号链接和 Windows junction/reparse point 均拒绝。真实路径必须仍在配置的根目录内。
- 清理前刷盘保存课堂 storage_generation 屏障；重启后阻止已删除代次任务重新下载或生成文件。只保留匿名课堂 ID 与代次，不保留媒体、凭据或分析内容。
- 文件占用或清理失败不发送成功回执，持续入口退避重试；重复清理及确认重发使用同一代次。凭据失效或旧代次被拒绝时不盲重发。

## 后端成员如何接入

1. 部署包含上述后端提交的代码，按后端文档完成 001～008 迁移和独立删除账本初始化。该步骤由后端/数据库成员执行，本模块不直连数据库。
2. 运行 `python -m workers runtime-info`，将现有 Go 配置的处理器版本、FFmpeg 摘要固定为此平台实际值。不能重写旧批次版本。
3. 一个 Worker 身份必须只归属一个部署实例，所有该实例的正式任务暂存和缓存必须在其 `WORKER_TEMP_ROOT` 内。视频与音频不能用同一身份在不同机器或不同临时目录分别确认清理。多 Worker 身份的后端配置仍由后端成员协调；不满足此条件保持清理关闭，不提交不完整回执。
4. 当前 main 的公共 Schema 尚未含清理 DTO。启用时把 `WORKER_CLEANUP_CONTRACT_PATH` 指向此次后端部署的**完整** `contracts/v1.schema.json`，不手写字段、不使用测试夹具。后端协议进入共同主分支后可省略该参数。

配置和启动（使用现有 Worker 凭据及其他运行参数）：

```powershell
$env:WORKER_CLEANUP_ENABLED = '1'
$env:WORKER_CLEANUP_CONTRACT_PATH = '<后端部署根目录>/contracts/v1.schema.json'
python -m workers run
```

持续入口在每次领取媒体任务之前检查清理。任务运行中由既有心跳检查感知 Go 撤租，停止并等待媒体子进程退出，再进入下一轮清理；不会为清理另外启动模型或视频任务。空闲时约每 5 秒轮询。一次性检查入口：

```powershell
python -m workers cleanup-once
```

返回 `receipt: null` 表示无待清理项目；实际回执必须绑定当前 worker_id、deletion_request_id 和 storage_generation。这里的成功只覆盖已声明的正式 Worker 临时根目录，不代表备份、他机、原 vision-lab、独立 analyze-local 输出或外部存储已清理。

## 真实联调记录

Windows，本机 PostgreSQL 17.6 的专用 `cangjie_worker_integration` 测试库；Go 1.26.0、Python 3.12。限制 Go 编译并行度，未加载视觉模型或启用任何 GPU。

| 检查 | 结果 |
| --- | --- |
| 4 秒 320×240 合成录像 | 上传、probe、代理、Range、4 张关键帧及 4 条 Go 画面证据通过 |
| 4 秒 161×120 奇数宽度合成录像 | 同上通过，原录像时间点保持 |
| 无音轨 | 音频明确 skipped/NO_AUDIO，无转写、无六维报告，不伪造结果 |
| 真实清理 HTTP | Go 删除先 blocked/WORKER_CLEANUP_PENDING；Python 清理独立远端暂存并回报后，Go 删除 succeeded |
| 回执持久化 | 数据库有且只有当前 Worker 的一条回执，下一次清理轮询返回空 |

媒体联调 9.96 秒；每个 probe 约 0.86～0.94 秒，每个关键帧阶段约 0.58～0.61 秒。清理联调 2.16 秒，其中 Python 实际领取、清理、确认及再次空轮询约 0.94 秒。这些为合成 P0 流程，不能代替密集课堂 P1 的速度或准确率验收。

复现入口沿用 [README](README.md) 的独立测试库限制。只向固定最新后端的独立检出复制 `workers/integration/real_cleanup_test.go.fixture` 为 `internal/httpapi/real_cleanup_worker_test.go`，同时复制原 `real_go_test.go.fixture`；运行 `TestRealWorkerCleanupIntegration`。旧 phase4 后端不能编译该清理夹具。CI 仍保留旧 phase4 媒体兼容检查，并将最新后端媒体和清理检查固定到上述提交。

## 仍待共同验收

- 后端 PR #9/#10 与回放前端 PR #19 合入后，验证真实浏览器同源登录、上传、来源许可、回放及证据定位。本轮没有重做前端，也没有将样例界面当作真实联调通过。
- 音频 Worker 和真实中文 ASR，报告模型及六维报告完整链路由相关成员交付。
- P1 举手、低头、匿名人物查询及复核的正式存储/查询协议仍需前后端确认，当前只保留现有 `analyze-local` 入口与报告，不把本地报告塞入 P0 DTO。
- 密集课堂的独立人工质量验收、编号稳定性与 120 秒录像在 120 秒内处理的目标仍未通过。GPU 故障后的真实显卡推理恢复须另行验证，本轮不执行 GPU 压测。
- 跨主机、音视频多实例、备份恢复及损坏存储的治理验收须在共同部署环境完成；本机回执不能冒充所有机器的回执。
