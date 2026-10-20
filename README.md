# 教学质量管理系统

文档 v0.7，2026-10-14。当前仓库包含开发文档、接口样例、数据库迁移、Go 后端、React 前端及 Python 视频 Worker。后端已提供课堂、媒体、任务、证据、报告、复核发布及治理接口；前端已提供课堂详情与回放，视频 Worker 已提供媒体处理和清理回执。音频 Worker、真实 ASR、报告模型配置及部分前端管理/复核页面仍待交付，M1～M4 的整体完成以验收计划和实际验证结果为准。

## 从这里开始

全员先读[项目文档](项目文档.md)与[系统架构](docs/系统架构设计.md)，按[开发与验收计划](docs/开发与验收计划.md)推进 M0～M4。

[系统草图](系统草图.html)供前端参考布局，正式实现以当前需求和协议为准，页面可自行设计。

手机采集联调从 [B0 开发环境](docs/B0开发环境.md)开始：提供 Windows 独立数据库、后台启动/停止、HTTPS 证书、测试课堂与账号及安卓设备检查步骤。

上传录像并分析举手、低头及逐人记录，从 [团队网页视觉部署](deploy/team-web/README.md)开始：沿用团队页面，在课堂详情增加视觉入口和结果标签；Windows 本机试用提供一键启动。该流程无需 ASR，不生成六维报告，独立人工质量与长录像速度仍待验收。

| 成员 | 重点资料 | 首个联调目标 |
| --- | --- | --- |
| 前端 | [开发协议](docs/开发协议.md)、[接口样例](contracts/examples/manifest.json) | 用固定样例显示任务、转写和报告 |
| 后端 | 开发协议、[数据库设计](docs/数据库设计.md) | Go API、任务流转和权限校验 |
| 数据库 | 数据库设计、[001](database/001_initial_schema.sql)～[009](database/009_live_captures.sql) | 初始化、迁移及约束检查 |
| 音频 | 开发协议第3节、audio-result 样例 | Worker 公共入口和带时间戳转写 |
| 视频 | 开发协议第3节、probe-result/video-result 样例 | 媒体检查、播放代理和关键帧 |

## 文件怎么用

- [AGENTS.md](AGENTS.md)：Codex 的仓库开发约定，包含按任务查阅文档、模块边界、验证及协作规则。
- `docs/`：架构、协议、数据库、验收计划。
- `contracts/v1.schema.json`、[端点映射](contracts/endpoints.json)与 `contracts/examples/`：契约1.1及联调样例；api-cases.json 中每个 value 是独立 DTO，按 definition 选择。前端和 Worker 须同步更新，不能混用1.0结果。
- `database/`：迁移和约束验证；新库按编号依次执行001～010，旧库只执行未应用迁移。007核验历史运行时约束，008新增Worker清理回执，009新增实时采集，010新增视觉任务约束；[数据库联合验收](docs/数据库联合验收.md)提供分权、升级及多连接验证入口。
- `tools/`：文档/协议检查、隔离数据库检查。
- [.env.example](.env.example)：配置模板，复制为本地 `.env` 后填写。
- [frontend/UI约定.md](frontend/UI约定.md)：前端展示层约定——颜色变量、状态色调、新页面骨架与窄屏规则。

## Python 视频模块

视频模块已提供媒体检查、播放代理、关键帧及严格 1.1 Worker 回传代码，复用已有 vision-lab 0.4.3 的行为分析能力。Go 的内部任务接口已实现；Worker 经这些接口领取任务、读取媒体和回传产物。安装、启动、测试与结果边界见 [视频模块说明](workers/README.md)，队友接入与验证记录见 [交接说明](workers/HANDOFF.md)，提交清单见 [commits 报告](workers/COMMITS.md)。清理功能默认关闭，启用前须确认后端完整协议及 Worker 身份和暂存目录的独占归属。

## Go 后端运行骨架

后端使用 Go 1.26 系列和 PostgreSQL 17。当前已提供：

- `cmd/api`：API 进程、统一 JSON 响应、请求 ID、优雅停机及健康检查；
- `cmd/migrate`：按文件名顺序执行 `database/001`～`010`，记录并校验迁移摘要及历史顺序；
- `cmd/init-deletion-ledger`：显式、幂等地执行一次独立删除账本初始化；API 启动不会自动补建丢失账本；
- `cmd/bootstrap-admin`：幂等创建首个学校、系统管理员及角色；
- `cmd/seed-dev`：仅在 development/test 环境幂等创建四种角色的联调账号；
- `internal/identity`：Cookie 会话、登录/登出、CSRF、固定角色、组织范围和账号授权；
- `internal/academic`：学院、学期、课程、班级、教室和开课实例的范围化读写；
- `internal/classroom`：课表关联的课堂目录、筛选、归档和主媒体选择；
- `internal/media` 与 `internal/storage`：来源核验、流式上传、本地对象存储适配、媒体目录及授权 Range 回放；
- `internal/analysis`：分析批次、Worker 领取/心跳/输入/产物/完成/失败接口，以及租约、取消、有限重试、过期回收和重复回调幂等；
- 本地媒体目录和独立删除日志的 readiness 检查；
- 报告输入按最终序列化请求大小缩减，单条长证据也会截断并记录限制；候选正文不合法时仍结算可核验账单，不自动重发收费请求。
- PDF 使用标准 STSong-Light CID 字体、Unicode CMap 和显式 FontDescriptor，包含固定来源、执行溯源、证据可用性及完整正文；不依赖私有字体文件。回归验证同时使用 MuPDF/PyMuPDF 和 Poppler。
- 导出确认回滚时清理本次租约文件，提交结果不确定时保留对象；删除日志重放先收口账目，数据库清理失败受最大重试次数约束。Worker 未确认清理时仍保持 blocked。
- `Dockerfile` 与 `compose.yaml` 共同开发入口。

### 本机启动

1. 安装 Go 1.26 系列和 PostgreSQL 17，创建空数据库。
2. 复制配置模板，不要提交本地配置：

```powershell
Copy-Item .env.example .env
```

3. 在 `.env` 中填写数据库与 Worker 执行版本。`WORKER_TOKEN` 使用至少 32 位的本地随机值；固定 JSON 联调也必须填写明确版本，不使用 `latest`：

```text
DATABASE_URL=postgres://teaching:本地密码@127.0.0.1:5432/teaching?sslmode=disable
WORKER_TOKEN=<至少32位本地随机值>
WORKER_PROCESSOR_VERSION=fixed-json-v1
FFMPEG_BUILD_SHA256=<64位小写十六进制摘要>
ASR_MODEL_NAME=fixed-json-asr
ASR_MODEL_REVISION=p0-v1
```

4. 依次迁移、初始化管理员并启动 API：

```powershell
go run ./cmd/migrate
go run ./cmd/init-deletion-ledger
$env:BOOTSTRAP_ADMIN_PASSWORD = '<至少12位的本地密码>'
go run ./cmd/bootstrap-admin
Remove-Item Env:BOOTSTRAP_ADMIN_PASSWORD
go run ./cmd/api
```

编译后的 API 可以从其他工作目录启动。将 `DATABASE_MIGRATIONS_DIR` 设置为部署时打包的迁移目录绝对路径，例如 `D:/services/cangjie/database`；启动检查与 `/health/ready` 共用这个目录。默认 `database` 保留仓库根目录开发用法，相对配置在读取配置时解析一次。迁移工具仍可用 `-dir` 指定相同目录；Docker Compose 固定为 `/app/database`。

管理员初始化命令可以重复执行；已有同名管理员时不会覆盖密码。启动后检查：

```powershell
Invoke-RestMethod http://127.0.0.1:8080/health/live
Invoke-RestMethod http://127.0.0.1:8080/health/ready
```

`live` 只检查进程，`ready` 会检查 PostgreSQL、媒体目录和独立删除日志。数据库不可用或删除日志损坏时返回 503。

浏览器业务接口使用 `/api/v1` 前缀。先请求 `GET /api/v1/auth/csrf`，再以返回的 token 作为 `X-CSRF-Token` 调用登录；所有写请求还须携带与 `PUBLIC_ORIGIN` 完全一致的 `Origin`。登录成功后服务端轮换为 HttpOnly、SameSite=Lax 的会话 Cookie，HTTPS 环境自动启用 Secure。会话有效期由 `SESSION_TTL_SECONDS` 控制，禁用账号会在同一事务撤销其活动会话。

为支持 M0 环境复现和 M1 固定样例联调，Go 后端已实现协议中的身份、基础教务、课表、课堂目录、来源、媒体与分析任务接口。上传完成的原媒体保持 `pending`；`media_prepare` 的 probe 成功后进入 `ready`。Worker 使用独立 Bearer 凭据，经 `/internal/v1/jobs/*` 领取任务、续租、读取输入、上传产物并提交固定 JSON 结果；所有任务输入和结果都绑定当前租约、课堂存储代次及固定执行版本。服务启动和周期扫描会回收过期租约，旧 token 不能写入，重复完成只接受完全相同的原始 JSON。

音视频任务终态后，Go 会保存不可变转写快照、建立同批次时间证据，并在外部处理许可和报告配置有效时调用兼容 Chat Completions 的报告模型。实际发送的证据清单和摘要先持久化；模型候选只有通过六维结构、时间范围和引用归属校验后才成为可读取草稿。模型未配置、无有效转写或证据不足时批次明确以受限状态收口，不补造结论。该能力提供 M1 固定样例联调所需后端入口；前端、Worker 和真实授权课堂样本的跨模块验收仍未完成，因此不代表 M1 或 M2 已完成。

后端还提供不可变的人工转写修订与 `report_only` 重跑，以及报告复制、编辑、逐条复核、整篇确认、发布和撤回接口。转写与报告写入均检查版本锁；竞争发布还会比较调用方预期的当前发布版。发布要求督导或系统管理员权限、六维结构完整、事实证据仍可用且所有保留观察已复核。旧转写、旧证据关系和旧报告不会被新修订覆盖。以上是 M3 的后端能力，前端复核页面、跨模块联调和导出仍待验收，因此不代表 M3 已完成。

需要四种角色的本地联调账号时，在 development/test 环境运行：

```powershell
$env:BOOTSTRAP_TEST_PASSWORD = '<至少12位的本地测试密码>'
go run ./cmd/seed-dev
Remove-Item Env:BOOTSTRAP_TEST_PASSWORD
```

默认创建 `admin`、`academic_demo`、`supervisor_demo` 和 `teacher_demo`；密码只从本地环境读取，不写入仓库。命令在 production 环境会拒绝执行。

### 容器启动

已安装 Docker Compose 时可运行：

```powershell
docker compose --profile bootstrap run --rm init-deletion-ledger
docker compose up --build -d postgres api
$env:BOOTSTRAP_TEST_PASSWORD = '<至少12位的本地测试密码>'
docker compose --profile bootstrap run --rm seed-dev
Remove-Item Env:BOOTSTRAP_TEST_PASSWORD
```

Compose 使用固定 PostgreSQL 17.6 和 Go 1.26.0 镜像，仅含开发用数据库凭据；生产环境必须使用独立密钥和部署配置。
数据库和 API 默认只绑定本机回环地址。若本机已有 PostgreSQL 或其他服务占用端口，可在启动前设置 `$env:COMPOSE_POSTGRES_PORT` 或 `$env:COMPOSE_API_PORT`，无需停止本机服务；Compose 默认会按 `COMPOSE_API_PORT` 生成浏览器允许来源，也可通过 `PUBLIC_ORIGIN` 显式覆盖。若只需创建系统管理员，可设置 `BOOTSTRAP_ADMIN_PASSWORD` 后运行 `docker compose --profile bootstrap run --rm bootstrap-admin`。

### 后端检查

```powershell
go test ./...
go vet ./...
go build ./cmd/...
```

身份、权限与媒体链路的 PostgreSQL 集成测试只允许指向可清空的独立测试库：

```powershell
$env:TEST_DATABASE_URL = 'postgres://teaching:测试密码@127.0.0.1:测试端口/teaching?sslmode=disable'
go test ./internal/httpapi -run TestPhaseOneAuthorizationFlow -v
go test ./internal/httpapi -run TestPhaseThreeSourceUploadPlaybackAndAuthorization -v
go test ./internal/httpapi -run TestPhaseFourAnalysisWorkerLeaseAndRecovery -v
Remove-Item Env:TEST_DATABASE_URL
```

该测试会在目标库执行现有迁移，并清空其中的 `teaching` 业务数据；不得连接共享开发库或生产库。

数据库结构仍可按[数据库设计第6节](docs/数据库设计.md#6-执行与验证)运行原生 SQL 检查。`cmd/migrate` 面向由本工具初始化的新库；若发现已有 `teaching` schema 却没有迁移历史，会拒绝猜测旧库版本，须先由数据库负责人确认基线。

在仓库根目录运行（需安装 Python jsonschema）：

```powershell
python tools/check_docs.py
python -m unittest discover -s tools -p test_quality.py -v
node tools/check_prototype.mjs
```

数据库验证命令见[数据库设计第6节](docs/数据库设计.md#6-执行与验证)。

真实 ASR 评测入口为 `python tools/check_quality.py <受控本地评测.json>`，格式及已确认的暂定门槛见[验收计划](docs/开发与验收计划.md#3-样本与质量验证)。本仓库不附真实课堂样本；React 与视频 Worker 已有独立运行入口。媒体准备与回放可通过 Go 联调，真实 ASR、六维报告、完整修订发布和故障恢复仍须按验收计划完成共同验证。检查通过仅说明相应契约、SQL、草图行为、已实现接口或度量工具通过，不能代替真实音视频效果与模型验收。

## 前端（数据访问层与 M2 回放）

`frontend/` 为 React 19 + Vite + TypeScript 工程。页面为课堂任务（`#/sessions`）、转写（`#/transcript`）、报告（`#/reports`），三个页面统一经 `frontend/src/data/` 的数据访问层取数，不再各自读样例文件：

- **默认读取 `contracts/examples` 固定样例**，不依赖后端，克隆仓库即可离线打开；
- 设 `VITE_DATA_SOURCE=api` 时改走 `/api/v1`：`getSessions`、`getSessionMedia`、`getMedia`、`getRun`、`getResults`、`getRevision`、`getReport` 分别对应 `GET /sessions`、`/sessions/{id}/media`、`/media/{id}`、`/analysis-runs/{id}`、`/analysis-runs/{id}/results`、`/transcript-revisions/{id}`、`/reports/{id}`；
- 统一按协议处理 `{data,request_id}`、`{error:{code,message,details}}` 与列表 `{items,next_cursor}`，错误码映射为页面中文提示；
- 账号区在侧栏内（宽屏在左栏、≤900px 随侧栏变成顶部栏），登录链路为 `GET /auth/csrf` → 带 `X-CSRF-Token` 的 `POST /auth/login` → `GET /auth/me`；页面展示的操作以各资源返回的 `allowed_actions` 为准（服务端逐次授权），账号级能力只在账号区展示。
- 接口模式下未登录不发业务数据请求（页面提示先登录）；确认退出或换账号后清空上一个账号的数据及分页进度，迟到的旧响应会被丢弃。初始化、登录和退出完整流程串行执行；请求中同步拦截重复操作。退出失败保留账号并清除旧 CSRF，重试时重新获取；确认退出成功或会话失效后立即显示匿名，不等待令牌预取。
- 课堂列表通过“加载更多”继续读取游标，空页仍可继续，失败保留已有课堂并提供重试。转写与报告每轮最多查找 10 页；草稿每轮最多检查 20 个批次，达到预算后显示“继续查找”，保留页内位置和后续游标。报告在当前页优先选择发布版，再查询最新批次的草稿；只有全部查完才提示无结果，循环游标与接口读取失败各自保留真实错误。

样例或接口数据之间的引用不一致（例如 `run-partial` 与 `results` 的批次状态、报告溯源引用的修订）仍在页面“样例数据核对”中显式列出，不拼成虚假链路。`/sessions`、`/analysis-runs`、`/results` 和通过校验的 `/reports` 已有真实后端读取入口；人工转写修订、报告复核和发布已有后端接口，但尚未进入当前前端联调范围。

```powershell
cd frontend
npm install        # Node.js 24 LTS；依赖锁在 frontend/package-lock.json
npm run dev        # 开发服务器，默认 http://localhost:5173
npm run check      # tsc --noEmit + vitest + vite build
```

`vite.config.ts` 已把 `/api` 代理到 `http://127.0.0.1:8080`。当前 Go API 未托管 `frontend/dist`；本机使用 Vite 开发或预览服务，正式部署需配置静态服务及同源 `/api` 反向代理。

预览入口只有一个：**跑开发服务器或静态服务器**。直接双击 `frontend/dist/index.html` 会白屏——构建产物用的是 ES module，浏览器在 `file://` 下按 CORS 规则拒绝加载，这与页面代码无关。可选方式：

```powershell
cd frontend
npm run dev                              # 开发模式，改代码即时生效
# 或看构建产物
npm run build
npx vite preview --port 18768            # 静态服务，支持 Range
```

按 UI 约定第 8 节，改了控件或焦点样式后跑一次键盘/焦点探针（零依赖，用本机 Edge）：

```powershell
# 第一个终端：在 frontend/ 下构建并起静态服务（保持运行，支持 Range）
cd frontend
npm run build
npx vite preview --port 18768

# 第二个终端：在仓库根目录跑探针
node frontend/tools/a11y-probe.mjs http://127.0.0.1:18768
```

### 接口模式联调

写请求要求 `Origin` 与 `PUBLIC_ORIGIN` 完全一致，本地用 Vite 开发服务器联调时必须在 `.env` 里把 `PUBLIC_ORIGIN` 改成前端的访问地址，否则登录会被判为 403：

```text
# .env
PUBLIC_ORIGIN=http://localhost:5173
DATABASE_URL=postgres://teaching:本地密码@127.0.0.1:5432/teaching?sslmode=disable
```

```powershell
$env:BOOTSTRAP_TEST_PASSWORD = '<至少12位的本地测试密码>'
go run ./cmd/seed-dev     # 建 admin / academic_demo / supervisor_demo / teacher_demo
go run ./cmd/api          # 新开一个终端保持运行

cd frontend
$env:VITE_DATA_SOURCE = 'api'
npm run dev
```

已实现：课堂列表可进入指定课堂详情（`#/sessions/{id}`，内含批次与操作 / 转写 / 报告 / 回放四个标签），并按资源 `allowed_actions` 显示上传、媒体准备、提交分析、取消与重试等动作按钮；活动批次每 2 秒轮询、进入终态停止。回放标签（M2，FR-05）按课堂主媒体取 `GET /media/{id}` 的 `playback_url` 播放播放代理，支持 0.5×～2× 倍速、前后 5 秒（播放器聚焦时可用 ← / →）、当前片段字幕（可开关，走浏览器原生字幕轨，排在播放控件上方，画中画字幕取决于浏览器支持）和画中画，并列出该批次的转写片段与可用证据，点击即跳到对应的原录像毫秒且高亮该行；媒体未就绪、没有 `playback_url`（未核验 / 无 playback 用途 / 已过期）或时间点超出媒体时长时，页面给出原因而不是拿别的地址顶替。

回放只在**固定样例模式**用仓库内的合成样例视频 `frontend/public/samples/m2-playback-demo.mp4`（480×270、30 秒、无音轨、带时间码，无真实课堂内容）；样例的 `sha256` 与字节数写在前端样例里并由单元测试锁定，只有重新生成视频时才需要同步。拖动进度条与时间跳转依赖服务端支持 Range：`npm run dev`、`npx vite preview` 和 Go 同源服务都支持，`python -m http.server` 不支持（只能从头连续播放）。接口模式一律使用服务端返回的 `playback_url`。

接口模式下拿到 `playback_url` 后，前端发一次 `Range: bytes=0-0` 预检；成功后取消不需要的响应体，即使服务端返回整份媒体的 200，也不继续下载。换地址、换账号、重查或离开页面会取消旧预检；响应头和错误正文读取都有期限。`VITE_PLAYBACK_PROBE_TIMEOUT_MS` 默认 10000 毫秒，可在启动/构建环境或 `frontend/.env.local` 设置正整数（非法值使用默认），修改后重启开发服务或重新构建。配置模板见 `.env.example`。预检保留服务端真实错误码（如 416、403、410），失败可重新检查；实际视频加载失败还提供“重试播放”，重取当前获准地址并重新加载，撤权后不沿用旧地址。

回放只叠加与当前原媒体、课堂及批次归属一致的字幕和证据；主媒体与最新分析使用不同录像时明确提示，保持当前录像播放并停止叠加不匹配结果。播放器失败或不存在时定位不可用，恢复后才开放有效定位。没有主媒体时，回放标签保留媒体列表的加载、错误、重试及加载更多入口；空页仍有游标时继续查找，查完无可回放目标才显示空态。实际字幕及画中画支持以浏览器为准，不承诺所有画中画窗口显示字幕。

尚未实现：课堂筛选控件、来源登记界面（上传需先有 `source_record_id`）、与后端的真实联调。

### PR #7 浏览器回归

[回归脚本](frontend/tools/browser-regression.mjs)用真实 React 页面和受控接口响应检查认证竞态、分页、续查、错误与窄屏导航；不向真实后端写入数据，不替代真实 Cookie、多标签页及 Go 服务联调。使用已安装的 Playwright 包和 Microsoft Edge，无需修改项目依赖锁文件。以下命令均在仓库根目录执行：

```powershell
$env:VITE_DATA_SOURCE = 'api'
Push-Location frontend
try {
    npm run build -- --outDir ../var/frontend-api
} finally {
    Pop-Location
    Remove-Item Env:VITE_DATA_SOURCE
}
python -m http.server 18767 --bind 127.0.0.1 --directory var/frontend-api
```

保持静态服务运行，在另一个终端执行（将参数替换为本机现有 Playwright 包的入口文件）：

```powershell
node frontend/tools/browser-regression.mjs '<已安装的playwright包目录>/index.mjs'
```

脚本失败时返回非零退出码；成功时在 `output/playwright/` 保存 JSON 结果和窄屏截图。静态服务仅用于受控回归，真实接口模式仍按上面的同源服务或 Vite 代理运行。

## 使用 Codex

在 Codex 中打开克隆后的 Git 仓库根目录并新建任务，确认已读取 `AGENTS.md`；已有会话可直接要求重新读取。仓库级指令的加载方式见 [OpenAI 官方说明](https://learn.chatgpt.com/docs/agent-configuration/agents-md)。

成员可用下面的格式开始任务：

> 我负责【前端/后端/数据库/音频/视频】，本次实现【FR编号与具体功能】，使用【分支名】。请按 AGENTS.md 查阅相关文档，完成【交付项】并验证【验收条件】，说明需要其他模块配合的部分。

## 安卓手机采集与实时预览（P1）

新增七个业务接口（含按课堂恢复活动采集）、两个媒体服务内部接口和009迁移；采用 MediaMTX v1.15.3，默认关闭。配置、可信HTTPS、安卓接入、调用顺序、状态与错误见[实时采集接口](docs/实时采集接口.md)。最小联调页为[手机/电脑测试页](frontend/public/capture-demo.html)。录制结束后复用媒体入库、media_prepare和原回放接口；真实ASR、OCR、行为及报告仍按原分析链单独验收。

手机配对访问现转入独立的 `/capture-phone.html`，提供静音预览、真实输入电平、实际参数与编码、服务端录像状态、停止重试和刷新恢复提示；电脑入口及后端配对格式保持兼容。构建时同时产出主站与手机页。操作与逐项验收见[安卓手机采集操作与验收](docs/安卓手机采集操作与验收.md)；本轮模拟浏览器已验证，**安卓真机待验收**。

## P1 正式视觉分析后端（2026-10-19）

新增默认关闭的 `visual_only` 批次，复用已有视频 Worker 与视觉模型，按媒体检查、视觉分析两阶段保存人数、举手、低头、逐人三态观察与画面证据，提供鉴权概况和分页查询。执行后续010迁移、固定Worker1.0.7和模型修订并显式启用后可通过API使用；不依赖ASR或收费报告。原前端布局与P0流程保留，视觉按钮和人物展示仍需前端接入。部署、请求、验收与限制见[视觉后端交付](workers/VISUAL_BACKEND.md)。
