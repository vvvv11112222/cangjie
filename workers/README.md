# 视频工程师交付

本模块复用 vision-lab 0.4.3 的检测、姿态、头姿、追踪、座位和行为规则。核心来源及摘要见 [source-port.json](source-port.json)，模型来源、版本、许可证说明及摘要见 [models.manifest.json](config/models.manifest.json)。没有重新训练模型或改变识别门槛。

## 交付范围

| 工作 | 当前入口 | 输出与边界 |
| --- | --- | --- |
| 检查录像 | `probe` | 编码、尺寸、时长、音轨、原录像时间轴 |
| 网页播放视频 | `probe` | H.264/AAC MP4；与源录像逐帧核对时间，再上传 Go |
| 关键帧 | `video_analysis` | Go 返回的资产 ID、真实毫秒时间点、实际处理范围 |
| 人数与匿名编号 | `analyze-local` / `analyze_local()` | 可见人数、人物观察记录；编号只属于该录像，不等于学籍身份 |
| 举手、低头、离座、疑似持手机 | 同上 | 每次观察与持续事件、对应原图和标注图、无法判断的原因 |
| 座位与入口 | 同上 | 自动估计区域，可配置手动区域；未知课时默认不判迟到早退 |
| 人工修正与质量测量 | `vision_core.review` / `check_vision_quality` | 保留机器原结果、修订历史；测量人工标注样本 |

P0 正式接口严格使用 [团队协议](../docs/开发协议.md)及 [Schema 1.1](../contracts/v1.schema.json)：`events=[]`、`model=null`。本地行为报告包含更多字段，不能直接交给正式完成接口。正式六维报告、业务证据 ID、授权和数据库写入由 Go 负责。

当前 main 的 Go 尚未实现 `/internal/v1/jobs/*`，所以正式入口目前需要后端补齐接口才能接通。提供的模拟服务用于验证协议与真实媒体处理，不是可对外使用的后端。

## 安装与检查

[commits 报告](COMMITS.md)记录实际提交与验收范围。[自动检查](../.github/workflows/vision-worker.yml)在 PR 中分别检查 Windows、Linux 和 CPU 容器；具体运行结果以 GitHub Actions 为准，不将 GPU 吞吐与人工准确率视为自动检查通过的内容。

使用 Python 3.12，在仓库根目录执行。Windows：

```powershell
py -3.12 -m venv .venv
.\.venv\Scripts\Activate.ps1
python -m pip install -r workers/requirements.lock
python -m pytest -q
python tools/check_docs.py
python -m workers runtime-info
```

Linux：

```bash
python3.12 -m venv .venv
. .venv/bin/activate
python -m pip install -r workers/requirements.lock
python -m pytest -q
python tools/check_docs.py
```

安装包自带用于转码的 FFmpeg；媒体检查使用 PyAV，结果保留它的 FFmpeg 库版本。`runtime-info` 给出当前实际二进制的 SHA-256；各平台必须分别核对，不能使用另一台电脑的摘要。`VISION_FFMPEG_BINARY` 可指定团队固定的另一份二进制，再重新核对摘要。

## 连接团队 Go 后端

在后端实现内部接口、分配 Worker 凭据后执行：

```powershell
$runtime = python -m workers runtime-info | ConvertFrom-Json
$env:WORKER_API_BASE = 'http://127.0.0.1:8080/internal/v1'
$env:WORKER_ID = 'video-01'
$env:WORKER_TOKEN = '<后端分配的本地凭据>'
$env:WORKER_CAPABILITIES = 'probe,video_analysis'
$env:WORKER_PROCESSOR_VERSION = $runtime.processor_version
$env:FFMPEG_BUILD_SHA256 = $runtime.ffmpeg_build_sha256
python -m workers run
```

`--once` 只领取一次。环境变量与 [配置模板](../.env.example)一致；程序读取进程环境，不自行加载配置文件。后端固定同一 `processor_version`、FFmpeg 摘要和参数摘要到 Claim。参数间隔和上限使用 Claim，不能由本机默认值偷偷替换。

一次执行一个任务，心跳使用独立控制进程。失去租约、取消、硬截止或本地超时会停止媒体子进程并清理其临时目录。下载验证 SHA-256，上传验证回执摘要；重试完成请求保留同一份 JSON 字节。临时目录按任务/租约隔离，启动只清理带有效 UUID 和过期标记的自有目录。

Docker 入口（构建上下文为仓库根目录）：

```bash
docker build -f workers/Dockerfile -t cangjie-video-worker:1.0.0 .
docker run --rm cangjie-video-worker:1.0.0 python -m workers runtime-info
```

运行时显式传入前述环境变量及可写临时目录。容器中的 `127.0.0.1` 指容器自身；后端地址使用共同网络中的 Go 服务名。当前机器未安装 Docker，容器构建及 Linux 集成仍需共同环境验收。

## 使用已有行为分析

P0 不需要视觉模型。运行本地 P1 时，先从已有下载目录准备固定模型，原目录只读：

```powershell
python -m workers prepare-models --source '<已有模型目录>' --output var/vision-models
$env:VISION_MODEL_ROOT = (Resolve-Path var/vision-models).Path
$env:VISION_RUNTIME = 'cpu'
$env:VISION_RUNTIME_CONFIG = (Resolve-Path workers/config/runtime.cpu.json).Path
python -m workers analyze-local '<获准使用的录像.mp4>' --output var/my-video-result
```

默认检查全录像，每秒检查 2 次画面，最多处理 15,000 张；沿用原版 80 人姿态上限与识别规则。自定义参数通过 `--config <AnalysisConfig.json>` 提供。输出目录须为空，避免覆盖已有证据。

Windows DirectML 使用 [原版加速配置](config/runtime.directml.json)。需要已有对应 ONNX Runtime DirectML 安装目录：

```powershell
$env:VISION_RUNTIME = 'directml'
$env:VISION_RUNTIME_CONFIG = (Resolve-Path workers/config/runtime.directml.json).Path
$env:VISION_ACCELERATOR_ROOT = '<包含 directml/onnxruntime 的加速运行库根目录>'
```

程序根据实际 DXGI 清单选择 NVIDIA 显卡，失败会保留 CPU 回退说明；不依赖某台电脑固定的显卡编号。GPU 包不进入仓库。CUDA 沿用已有适配代码，本次未验证。

产物为 `report.json`、`report.html`、`report.md`、`counts.csv`、`persons.csv`、`playback.mp4` 和 `evidence/`。`persons.csv` 每行对应某编号在某个时间点的判断，`unknown` 表示看不清，不能按“没有动作”处理。报告中的帧号只是辅助，跨模块传时间用整数毫秒。

调用接口适合后续共同 Worker 扩展：

```python
from workers.video.local import analyze_local

report = analyze_local(source, empty_output_directory, config,
                       cancel=cancel_event, progress=on_progress, preview=on_preview)
```

`on_progress(**values)` 接收状态，`on_preview(frame, jpeg)` 沿用现有观看分析过程的数据。正式页面接入仍须由 Go 中转并完成权限检查。本地同一进程可连续调用并复用模型缓存；首个任务初始化 GPU 模型也计入耗时，报告另列初始化时间。不要每处理一张画面或一段录像就重新启动模型进程。

## 验证入口

真实媒体与模拟 Go 接口联调：

```powershell
python -m workers.tools.check_video '<获准使用的录像.mp4>' --output var/p0-check
```

迁移前后同录像、同参数结果对比（原实验目录只读）：

```powershell
python -m workers.tools.validate_port --lab-root '<原 vision-lab 目录>' --job '<已有任务编号>' --output var/port-check
```

独立人工标注的行为质量测量：

```powershell
python -m workers.tools.check_vision_quality '<受控本地人工标注.json>'
```

标注格式见 [交接说明](HANDOFF.md)。当前只有回归与迁移对照，没有独立留出集准确率结论；不能把模型分数或两版一致当作准确率达标。真实课堂视频、人工标签、权重和分析结果全部留在被忽略的 `var/` 中。

当前验证结果、队友接入位置及剩余集成项见 [HANDOFF.md](HANDOFF.md)。

## 速度交付门槛

完整行为分析须使用同一台电脑、同一显卡、同一录像、同一参数，并逐项对照原示例程序。完整人数、举手/低头/离座/手机、80 人上限、2 FPS 与证据输出都保持开启；不能通过少分析人、少检查画面、降低分辨率或关闭行为功能充当提速。模型初始化与热运行分别测量。

释放原演示服务的模型缓存后，分别以独立进程执行 `workers.tools.benchmark_analysis` 的 `baseline` 和 `team` 两种 implementation。每种先运行近景，再运行密集课堂；两进程不能同时占用 GPU。程序在 NVIDIA 空闲显存不足 3 GB 时拒绝测量，避免重现双模型争用。结果留在 `var/`，检查 `timing.json` 与对应 report 的人数、人物和行为记录。

```powershell
python -m workers.tools.benchmark_analysis --implementation baseline --lab-root '<原实验目录>' --job '<近景任务编号>' --job '<密集课堂任务编号>' --output var/speed-baseline
python -m workers.tools.benchmark_analysis --implementation team --lab-root '<原实验目录>' --job '<近景任务编号>' --job '<密集课堂任务编号>' --output var/speed-team
python -m workers.tools.check_speed_acceptance --baseline var/speed-baseline --team var/speed-team --output var/speed-comparison.json
```

先按前文配置实际执行器、模型目录和运行配置；原实验目录的 runtime.json 必须与团队配置一致。对照检查还会核对实际显卡、模型摘要、执行配置与完整结果，参数不同或结果变化时拒绝通过。输出中的“未变慢”只针对该轮测量，系统负载和模型编译缓存仍会影响时间。

这一步使用同一份已生成的播放视频，便于单独比较算法耗时，不包含上传与转码。2026-10-07 已完成三段录像的独占环境对照及 2 分钟录像的本地完整处理；本轮团队版达到原示例的最低速度和结果一致性标准，但完整处理约 185 秒，尚未达到 120 秒目标。具体数据和准确率限制见 [验证记录](VALIDATION.md)。真实 Go 全链路仍待队友接入。
