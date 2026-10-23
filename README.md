# 观课 · 教学质量管理系统

提供课堂管理、录像上传、手机采集、语音转写、举手与低头识别、教学报告、人工复核、发布导出和权限管理。后端使用 Go，前端使用 React，数据库使用 PostgreSQL，音视频处理由 Python Worker 执行。

视觉处理模块整合自本团队在项目开发早期编写的过程代码，现由正式 Worker 调用；旧的独立本地验收和结果导出入口已移除。该过程代码不是外部第三方项目。实际推理使用的第三方预训练模型见模型清单，不将预训练模型或其训练数据声明为团队原创。

## 环境

| 组件 | 版本或要求 |
| --- | --- |
| Git | 用于准备部署和配置采集时记录源码版本 |
| Go | 1.26 |
| Node.js | 24 或更高版本，含 npm |
| Python | 3.12 |
| PostgreSQL | 17，包含 initdb、pg_ctl、psql、createdb |
| 视觉模型 | 与 workers/config/models.manifest.json 中的文件及摘要一致 |
| 手机采集（可选） | Caddy、MediaMTX、FFmpeg、FFprobe；手机与电脑网络互通 |

模型权重、驱动、密钥、数据库及课堂录像均不随源码分发。使用已有且获准的模型文件；模型来源和摘要见 [模型清单](workers/config/models.manifest.json)。PDF 字体及其 [许可证](internal/governance/fonts/OFL.txt) 随源码保留。

## Windows 本机启动

在仓库根目录执行。示例路径替换为本机实际路径；建议数据目录使用英文路径。

推荐使用 Git 克隆仓库。使用无 `.git` 的源码 ZIP 时，先在解压后的源码根目录执行 `git init`、`git add --all`，再以本机身份创建导入快照提交；这会生成新的本机提交号，原始交付版本以源码包校验和及提交材料为准。准备命令需要这些 Git 元数据。

```powershell
py -3.12 -m venv .venv
.venv/Scripts/python.exe -m pip install -r workers/requirements.lock
Set-Location frontend
npm ci
Set-Location ..
.venv/Scripts/python.exe -m workers prepare-models --source D:/downloaded-models --output D:/cangjie-models
```

准备独立数据库并构建前后端，首次启动可明确选择 CPU：

```powershell
.venv/Scripts/python.exe tools/team_web.py --data-root D:/cangjie-data/team-web prepare --go D:/runtime/go/bin/go.exe --pg-bin D:/runtime/pgsql/bin --node 'C:/Program Files/nodejs/node.exe' --python .venv/Scripts/python.exe --models D:/cangjie-models --vision-runtime cpu
.venv/Scripts/python.exe tools/team_web.py --data-root D:/cangjie-data/team-web start-detached
```

打开 [系统首页](http://127.0.0.1:8088/#/sessions)。账号为 `admin`、`academic_demo`、`supervisor_demo`、`teacher_demo`；随机密码保存在本机数据目录的 `使用说明.txt` 中。现有账号密码不会被重复准备操作覆盖。

| 服务 | 默认地址 |
| --- | --- |
| 网页 | 127.0.0.1:8088 |
| Go API | 127.0.0.1:18088 |
| PostgreSQL | 127.0.0.1:55437 |

本机入口仅绑定回环地址。数据、日志、密钥和构建产物保存在指定数据目录，源码更新不会自动替换已运行的程序。更新前等待分析结束，再依次停止、重新准备和启动，始终使用同一数据目录。

```powershell
.venv/Scripts/python.exe tools/team_web.py --data-root D:/cangjie-data/team-web status
.venv/Scripts/python.exe tools/team_web.py --data-root D:/cangjie-data/team-web stop
```

使用默认数据目录 `var/team-web` 时，可双击根目录 `启动团队网页.bat`；首次使用仍须完成 prepare。自定义数据目录使用上述命令。

如需 Windows 独显加速，先安装与设备兼容的 DirectML ONNX Runtime；将 prepare 的 CPU 选项替换为 `--vision-runtime directml --accelerator-root D:/vision-accelerators`。启动器会核对设备和模型执行；CPU 模式速度取决于录像长度及人数。

## 使用流程

1. 管理员准备课程、班级、教室、开课及账号权限。
2. 教师创建课堂，登记并核验素材来源，上传录像或配对手机采集。
3. 在课堂详情选择“举手与低头识别”或“语音转写与教学报告”。
4. 查看任务、回放、转写及分析结果；有权限的督导或管理员复核报告后发布。

系统右上角“使用帮助”提供角色及操作引导。语音转写和报告需由管理员在“运行与设置 → 语音与报告模型配置”填写百炼兼容接口、模型、密钥、共享预算和单价；保存后在系统空闲时应用。录像来源还须明确允许相应的云端音频和文本处理。配置及分析可能产生供应商费用。

已有本机环境且尚未保存网页模型配置时，也可使用根目录 `配置语音与报告.bat` 完成首次导入；已保存网页配置的部署继续通过网页管理。

按需导入 50 节明确标记的合成课堂：

```powershell
.venv/Scripts/python.exe tools/team_web.py --data-root D:/cangjie-data/team-web seed-demo
```

该命令只为上述本机开发数据库添加演示目录，不创建真实录像、处理授权、转写或报告，也不自动调用模型。

## 手机摄像头与麦克风

先停止上述本机部署，再使用电脑当前的局域网 IPv4 和实际程序路径配置：

```powershell
.venv/Scripts/python.exe tools/team_web.py --data-root D:/cangjie-data/team-web enable-capture --ip 192.168.1.10 --caddy D:/runtime/caddy/caddy.exe --mediamtx D:/runtime/mediamtx/mediamtx.exe --ffmpeg D:/runtime/ffmpeg/ffmpeg.exe --ffprobe D:/runtime/ffmpeg/ffprobe.exe
.venv/Scripts/python.exe tools/team_web.py --data-root D:/cangjie-data/team-web start-detached
```

在电脑防火墙允许当前局域网访问 `8443/TCP`、`8081/TCP`、`8189/UDP`，保留数据库及内部控制端口仅本机可访问。手机打开 `http://192.168.1.10:8081/`，按页面步骤核对并安装证书，然后检查摄像头和麦克风。电脑使用 `https://192.168.1.10:8443/`，在课堂详情的“实时课堂”生成手机配对链接。手机授权后保持网页前台，结束时点击停止并等待录像就绪。

校园网或公共 Wi-Fi 可能隔离设备，可使用手机热点。电脑 IP 改变后需停止服务，用新 IP 重新 enable-capture，再启动。安卓和 iPhone 的权限、证书信任及实际网络需在对应设备上验证。

## 独立部署与构建

[.env.example](.env.example) 提供后端配置模板。独立部署需配置 PostgreSQL、媒体目录、删除账本、固定 Worker 执行版本及同源网页入口；密钥使用本地配置或环境变量。首次部署按顺序执行：

```powershell
Copy-Item .env.example .env
# 编辑本地 .env，再执行以下命令
go run ./cmd/migrate
go run ./cmd/init-deletion-ledger
$env:BOOTSTRAP_ADMIN_PASSWORD = '<自定的至少12位密码>'
go run ./cmd/bootstrap-admin
Remove-Item Env:BOOTSTRAP_ADMIN_PASSWORD
go run ./cmd/api
```

数据库迁移保存在 `database/001` 至 `013`，启动时校验迁移摘要。API 不自动创建前端页面或补建丢失的删除账本。`/health/live` 检查进程，`/health/ready` 检查数据库、迁移、存储和删除账本。

```powershell
go build ./...
go build -o audio-plan.exe ./cmd/audio-plan
Set-Location frontend
npm run check
Set-Location ..
```

Python Worker 通过 `python -m workers runtime-info` 查询 FFmpeg 摘要与处理器版本，通过 `python -m workers run` 启动。独立运行前按 [.env.example](.env.example) 显式设置 Worker API 地址、令牌、暂存目录、执行版本和模型配置；Python Worker 不自动读取 `.env`。音频 Worker 的 `ASR_PLANNER` 指向编译后的 audio-plan，运行能力须包含 `audio_analysis`。Windows 本机启动器自动完成这些设置。

根目录 `Dockerfile` 和 `compose.yaml` 提供后端及数据库容器入口；`workers/Dockerfile` 构建 CPU Worker。容器须自行接入同一网络、持久卷、前端反向代理及所需模型；容器内 API 地址使用服务名，不能使用宿主机的回环地址。Compose 中的默认凭据仅供本机开发。

## 数据与排障

数据目录中的 `private.json`、模型私有配置、数据库、媒体、删除账本和日志均须保留在受控本地环境。迁移、换机或清理前备份数据库及这些持久化文件，不将它们提交到 Git。

启动失败查看 `prepare.log`、`launcher.log`、`api.log`、`web.log`、`worker.log` 或 `postgres.log`。任务未完成时先检查模型配置、来源授权、预算及 Worker 状态。手机访问超时先检查 IP、网络互通和防火墙，证书错误需修复信任，不能跳过验证。
