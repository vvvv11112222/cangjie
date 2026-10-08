# 视频模块交接

对应项目 FR-04、FR-05、FR-06 的媒体处理部分，提供 FR-08 所需画面证据；已有 FR-13 行为能力作为本地 P1 模块复用。Git 基线为 main 的 `8827ed2`（已包含前端 PR #7），任务分支 `feat/vision-worker`。提交清单与验收边界见 [commits 报告](COMMITS.md)。

## 给队友的接入位置

| 成员 | 需要接入的内容 | 代码位置 |
| --- | --- | --- |
| 后端 | Claim、心跳、输入、产物上传、完成、失败六个内部接口；当前许可、租约、资产归属、幂等均需服务端校验 | [transport.py](common/transport.py)、既有 [开发协议](../docs/开发协议.md) |
| 音频 | 共用通信、校验、租约控制与阶段分发；增加 ASR handler 和对应能力配置，固定全组处理器版本 | [runner.py](common/runner.py)、[lifecycle.py](common/lifecycle.py)、[config.py](common/config.py) |
| 前端 | 经 Go 播放返回的资产、用 `timestamp_ms` 跳转、显示阶段和限制 | [handlers.py](video/handlers.py) 返回严格 1.1 DTO |
| 后端、前端、音视频 | P1 逐人观察/动作/预览接口扩展时共同修改 Schema、协议、样例与消费者 | [local.py](video/local.py) 与 [vision_core](vision_core/) 提供已有算法 |

本仓库原本没有 Worker 通信代码，故此次增加公共层供音频成员复用。程序没有自己的业务数据库、任务队列或媒体存储服务。音频接入时增加 handler 与 capabilities 校验，不再建立第二套任务通信。音频模型配置与固定 execution 的实际匹配由音频 handler 验证，视频阶段回显整个固定 execution。

multipart 的 `timestamp_ms`：关键帧使用十进制毫秒；没有时间点的播放代理使用空字符串表达 null。`origin_offset_ms` 为字符串 `0`。后端解析表单时需采用这一约定。key 为当前租约 UUID/文件名，不是文件系统路径。

关键帧处理达到上限立即停止；覆盖只延伸到最后实际解码时间之后 1 毫秒，保守记录尾帧，不据文件声明时长补足未解码范围。需要完整长录像证据时由 Go 固定合适间隔与上限。

媒体阶段异常发送 `CONFIG_MISMATCH`、`TIMELINE_INVALID`、`UNSUPPORTED_MEDIA`、`MEDIA_TOO_LARGE`、`INPUT_DIGEST_MISMATCH`、`PROCESSING_TIMEOUT`、`PROCESSING_FAILED` 或 `WORKER_API_ERROR`；retryable 明确给出，由 Go 决定是否重新排队。没有主动重试 ASR 或报告模型。403/409/410/凭据失效会停工；完成与取消竞态的最终状态由 Go 确认。

2026-10-08 修复后版本为 `cangjie-video-worker-1.0.1`。Go 分配 execution 时应固定此版本并核对实际 FFmpeg 摘要。限流或暂时故障按 `Retry-After` 在有效租约内有限重试；媒体本地超时与外部撤租分别处理，超时失败回传只检查仍有效的租约和 Go 硬截止。领取响应丢失或返回 5xx 时没有可用的协议恢复端点，Worker 等待配置的初次租约上限后再开始新的轮询；`JOB_LEASE_SECONDS` 必须与 Go 一致。

## 人工标注格式与质量测量

本地 JSON，不提交真实标签到仓库。以下是结构说明，不是合格样本：

```json
{
  "schema_version": "visual-eval-v1",
  "split": "held_out",
  "synthetic": false,
  "event_types": ["hand_raise", "head_down"],
  "samples": [{
    "report_path": "case-01/report.json",
    "source_sha256": "填写该原录像的实际SHA256",
    "frames": [{
      "timestamp_ms": 1000,
      "complete_annotation": true,
      "people": [{
        "bbox": [100, 80, 150, 140],
        "behaviors": {"hand_raise": true, "head_down": false}
      }]
    }]
  }]
}
```

每张画面须完整标注可见人员，框的是头部，坐标为原画面像素，不是缩放截图坐标；时间必须是报告实际检查过的时间。`true` 是有动作，`false` 是看清了且没有，`null` 是人工也看不清。按位置一对一匹配，不要求真人身份编号；未检出的举手目标计入漏检，未知不当作正确的阴性。测量输出每类 TP/FP/FN、precision/recall 和可判断比例，不泄露视频或学生信息。

这是逐画面测量，持续动作起止准确度和跨遮挡编号稳定性仍需单独验收。真实授权、独立划分、正负样本与远近视角的充分性须人工核对；程序不自动把自称 held_out 的数据当作可信验收集。

## 验收记录

本机 Windows、Python 3.12，固定 FFmpeg 摘要由 `runtime-info` 输出。结果与真实录像留在本地 `var/`，仓库只保留方法与结论。详见 [VALIDATION.md](VALIDATION.md)。

## 尚需团队集成

1. Go main 当前只提供 M0 基础能力，需实现协议中的内部任务/媒体接口，再执行真实 Go→Worker→证据→报告联调。
2. 音频成员接入公共入口及 ASR；协调共同 processor_version 与各阶段执行快照。
3. Linux/Windows 自动测试与 CPU 容器构建、运行及测试已通过 GitHub 检查；还需在共同部署环境验收真实 Go 接入和同一课堂样本的速度、质量。
4. 共同定义 P1 逐人动作、分析进度预览和人工复核接口，届时同步协议/Schema/样例/消费者。
5. 独立标注集验收、稳定编号和远处遮挡效果测量。当前可见人数不能直接当出勤人数，教师、镜面及遮挡需要区域配置和复核。

以上是团队整条链路的待接入项；本次视频代码不绕过这些接口向前端、数据库或存储写入。
