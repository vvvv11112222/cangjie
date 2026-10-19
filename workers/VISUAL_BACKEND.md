# 正式视觉后端接入与验收

本次补齐 FR-04/05/06 的视觉接入和 FR-13 课堂行为观察所需的 Go 后端和正式视频 Worker。基于 main `1af89a5`，沿用 Go 单体、现有上传与鉴权、租约、媒体资产和 PostgreSQL；视觉算法沿用已交付的 vision_core，没有更换模型、降低采样率或减少默认姿态人数。前端只补充新任务枚举的读取与中文名称，页面和布局未调整。

## 1. 现在能做什么

上传获准分析的录像后，创建 `visual_only` 批次，按 `probe → video_analysis` 执行。保存可见人数、匿名轨迹、每次举手/低头/离座/疑似持手机观察、持续动作及证据图片。结果由 Go 校验并保存在既有不可变 job_artifacts 中，Go 同事务创建 frame/behavior 业务证据。任务成功不要求 ASR 或大模型，也不生成六维教学报告。

`true` 是发现动作，`false` 是这次观察未发现，`null` 是看不清或条件不足。编号不是学籍身份；可见人数不能直接当出勤，低头不能直接当走神，不生成学生听课评分。座位和入口仍由同一算法估计；没有课时不判断迟到早退。持续事件与逐次观察分别提供，短动作可能只有逐次记录。

## 2. 部署

1. Go 与 Worker 同步更新本分支；先处理完或取消旧处理器版本的活动批次。执行正常迁移命令 `go run ./cmd/migrate -dir database`，新增文件为 [010_visual_analysis.sql](../database/010_visual_analysis.sql)。001—009 不变，不新增业务表。
2. 使用 Worker 已锁定的 Python 环境和已校验模型目录。模型准备方法见 [README](README.md)。运行 `python -m workers runtime-info`，取得 `processor_version`、`ffmpeg_build_sha256` 和 `visual_model.revision`。
3. 在 Go 配置中固定前两项，设置 `VISION_ANALYSIS_ENABLED=1`、`VISION_MODEL_REVISION=<visual_model.revision>`；Worker 同样启用 `VISION_ANALYSIS_ENABLED=1` 并配置 `VISION_MODEL_ROOT`。未显式启用时拒绝创建视觉批次，旧 P0 流程仍可使用。模型修订是模型清单与视觉核心代码的 SHA256，代码/清单变化后重新取值。
4. 沿用内部 Worker 凭据与 `WORKER_ID`，能力仍为 `probe,video_analysis`。Worker 领取时声明 `visual_enabled=true`；未声明的旧 Worker 不会领取视觉任务。Worker 不直连数据库或媒体目录。
5. 本机曾发生显示驱动故障，当前验收使用 `VISION_RUNTIME=cpu`、4 个 CPU/OpenCV 线程及 [runtime.cpu.json](config/runtime.cpu.json)。不更改显卡驱动、TDR 或系统设置。
6. Compose 已转发上述 Go 视觉配置及处理器/FFmpeg 固定值；模型 Worker 仍按其现有部署启动。B0 启动器保持原有默认设置，不会自动开启视觉任务。

## 3. 前端或联调脚本如何接入

先通过已有来源登记/核验、录像上传接口取得 `session_id` 与 `media_asset_id`。分析来源必须允许 `analysis`。随后调用原有创建接口，带 Cookie、CSRF、Origin 和唯一 `Idempotency-Key`：

```json
{
  "media_asset_id": "上传接口返回的 UUID",
  "mode": "visual_only",
  "input_transcript_revision_id": null,
  "config_profile": "vision-v1"
}
```

这是 `POST /api/v1/sessions/{id}/analysis-runs` 的请求结构说明；可直接通过 Schema 的合成请求见 [create-visual-run.json](../contracts/examples/create-visual-run.json)。每两秒查询原任务状态，完成后停止轮询。

| 请求 | 用途 |
| --- | --- |
| `GET /api/v1/analysis-runs/{id}/visual` | 模型、采样、实际覆盖、人数/举手/低头时间线、持续事件、图片 ID 和限制；不含整班逐人明细 |
| `GET /api/v1/analysis-runs/{id}/visual/persons?limit=100` | 人物编号与观察次数；返回 next_cursor |
| `GET /api/v1/analysis-runs/{id}/visual/persons/T0001?limit=100` | 该人物的毫秒时间点、像素框、四类行为状态和可空图片 ID；返回 next_cursor |
| `GET /api/v1/analysis-runs/{id}/results` | 兼容原有画面、事件和业务证据读取 |
| `GET /api/v1/media/{asset_id}/content` | 使用现有授权接口读取图片；原录像按已有播放代理定位 |

逐人接口默认100、范围1—200；next_cursor是不可修改的偏移令牌，下一页原样回传到 `cursor`。它只用于该不可变批次，不套用其他目录接口的 UUID 游标。无下一页时为 null。到达已有个人记录末尾返回空数组，未知编号404。未完成或非视觉批次409，权限/来源失败403或404，过期410，未登录401。失败和看不清不能显示为“全班正常”。

前端现有创建按钮仍按原流程发送 P0 请求。本次没有替队友新增页面或更换按钮；需要让其新增视觉模式调用并消费上述接口后，才能在团队页面上点击使用。现有服务器不会因代码提交自动切换版本或执行迁移。

## 4. 大小、时间轴与速度

- 默认每0.5秒检查一次、最多80人姿态、开启低头；沿用原模型和规则。最大15000次采样，达到时报告真实覆盖，不补全未检查时段。
- 单结果32 MiB、最多10万条人物观察、1万条持续事件、1万个轨迹；超限明确失败并要求分段，不暗中降采样或丢人。
- 图片数量沿用任务 `MAX_KEYFRAMES`，默认240；优先保留事件证据，剩余按时间顺序选取。没有图片的观察保留时间点与行为，仍可在原录像核对。核心自身1000张截图上限不变。
- probe 已生成播放代理后，视觉阶段对本租约下载的源文件建立硬链接，直接解码，不重复转码、不复制整段录像、不重新执行检测生成报告。视频使用原始毫秒坐标。
- 带显示旋转标记的未转正视频会明确拒绝视觉分析；避免框坐标与浏览器自动转正画面不一致。P0 媒体播放仍按原流程处理，手机采集完成后的标准化视频可继续走此入口。
- 本轮没有证明2分钟录像2分钟内完成。CPU 短片结果仅证明接入有效；远处遮挡、编号碎片化与独立人工准确率仍受原算法限制。

## 5. 验证

已运行：143项 Python 测试、267项前端回归测试和前端构建；真实 PostgreSQL 17.6 上 `go test ./... -count=1`；协议样例/文档检查；前端类型检查。Go 新增测试覆盖两阶段成功/失败/取消、重复完成、旧 Worker 领取隔离、图片时间与任务归属、模型/源摘要、轨迹与事件关联、三态保留、查询分页、权限和来源撤销。真实模型测试须单独提供获准的本地短录像，不下载模型或把私人录像放进 CI。

真实 HTTP 冒烟使用独立测试库，实际经历登录、来源与上传、任务创建、Python Worker probe/视觉推理、Go持久化、概况/人物分页/观察/证据图片读取、匿名拒绝。1秒密集片段：2次采样、2张证据图，probe约0.96秒，视觉约15.97秒，总测试19.02秒，持续事件0；此片段不能代替举手/低头精确率验收。另一次2秒密集片段：4次采样、49条匿名轨迹、184次人物观察、5次观察检出举手、持续事件0、4张图片；probe1.079秒、视觉29.101秒、总测试32.47秒。持续举手事件正结果及未知低头保留另由受控集成测试验证。

```powershell
python -m pytest -q
python tools/check_docs.py
go test ./... -count=1
go vet ./...
go build ./cmd/...
```

真实模型测试入口 `TestRealVisualUploadIntegration` 只接受测试库名 `cangjie_worker_integration` 且 `WORKER_INTEGRATION_ALLOW_DATABASE_RESET=1`。需配置 `WORKER_VISUAL_INPUT`、`WORKER_INTEGRATION_PYTHON`、`WORKER_INTEGRATION_ROOT`、`VISION_MODEL_ROOT`。测试会清空这个专用库，不可指向业务库。真实录像、输出、模型、凭据和测试数据库全部留在忽略目录。
