# 教学质量管理系统

文档 v0.6，2026-10-05。当前仓库包含开发文档、接口样例、数据库脚本，以及可运行的 Go 后端。M0 已具备 Go 后端环境、启动入口、身份权限、基础教务资料和角色测试账号等支撑能力；M1～M4 的课堂、媒体、分析、报告与交付能力仍按里程碑逐步实现。

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

## Go 后端运行骨架

后端使用 Go 1.26 系列和 PostgreSQL 17。当前已提供：

- `cmd/api`：API 进程、统一 JSON 响应、请求 ID、优雅停机及健康检查；
- `cmd/migrate`：按文件名顺序执行 `database/001`～`003`，记录并校验迁移摘要；
- `cmd/bootstrap-admin`：幂等创建首个学校、系统管理员及角色；
- `cmd/seed-dev`：仅在 development/test 环境幂等创建四种角色的联调账号；
- `internal/identity`：Cookie 会话、登录/登出、CSRF、固定角色、组织范围和账号授权；
- `internal/academic`：学院、学期、课程、班级、教室和开课实例的范围化读写；
- 本地媒体目录和独立删除日志的 readiness 检查；
- `Dockerfile` 与 `compose.yaml` 共同开发入口。

### 本机启动

1. 安装 Go 1.26 系列和 PostgreSQL 17，创建空数据库。
2. 复制配置模板，不要提交本地配置：

```powershell
Copy-Item .env.example .env
```

3. 在 `.env` 中至少填写 `DATABASE_URL`，例如：

```text
DATABASE_URL=postgres://teaching:本地密码@127.0.0.1:5432/teaching?sslmode=disable
```

4. 依次迁移、初始化管理员并启动 API：

```powershell
go run ./cmd/migrate
$env:BOOTSTRAP_ADMIN_PASSWORD = '<至少12位的本地密码>'
go run ./cmd/bootstrap-admin
Remove-Item Env:BOOTSTRAP_ADMIN_PASSWORD
go run ./cmd/api
```

管理员初始化命令可以重复执行；已有同名管理员时不会覆盖密码。启动后检查：

```powershell
Invoke-RestMethod http://127.0.0.1:8080/health/live
Invoke-RestMethod http://127.0.0.1:8080/health/ready
```

`live` 只检查进程，`ready` 会检查 PostgreSQL、媒体目录和独立删除日志。数据库不可用或删除日志损坏时返回 503。

浏览器业务接口使用 `/api/v1` 前缀。先请求 `GET /api/v1/auth/csrf`，再以返回的 token 作为 `X-CSRF-Token` 调用登录；所有写请求还须携带与 `PUBLIC_ORIGIN` 完全一致的 `Origin`。登录成功后服务端轮换为 HttpOnly、SameSite=Lax 的会话 Cookie，HTTPS 环境自动启用 Secure。会话有效期由 `SESSION_TTL_SECONDS` 控制，禁用账号会在同一事务撤销其活动会话。

为支持 M0 环境复现和后续 M1 固定样例联调，Go 后端已实现协议中的 `/auth/*`、`/users*`、`/org-units*`、`/terms*`、`/courses*`、`/class-groups*`、`/classrooms*` 和 `/offerings*`。系统管理员维护全校基础资料；学院教务只能维护本学院课程、班级、开课，以及安全范围内的教师和督导账号；教师的课程、班级和开课读取由本人开课关联决定。

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
docker compose up --build -d postgres api
$env:BOOTSTRAP_TEST_PASSWORD = '<至少12位的本地测试密码>'
docker compose --profile bootstrap run --rm seed-dev
Remove-Item Env:BOOTSTRAP_TEST_PASSWORD
```

Compose 使用固定 PostgreSQL 17.6 和 Go 1.26.0 镜像，仅含开发用数据库凭据；生产环境必须使用独立密钥和部署配置。
数据库和 API 默认只绑定本机回环地址。若本机已有 PostgreSQL 或其他服务占用端口，可在启动前设置 `$env:COMPOSE_POSTGRES_PORT` 或 `$env:COMPOSE_API_PORT`，无需停止本机服务。若只需创建系统管理员，可设置 `BOOTSTRAP_ADMIN_PASSWORD` 后运行 `docker compose --profile bootstrap run --rm bootstrap-admin`。

### 后端检查

```powershell
go test ./...
go vet ./...
go build ./cmd/...
```

身份与权限的 PostgreSQL 集成测试只允许指向可清空的独立测试库：

```powershell
$env:TEST_DATABASE_URL = 'postgres://teaching:测试密码@127.0.0.1:测试端口/teaching?sslmode=disable'
go test ./internal/httpapi -run TestPhaseOneAuthorizationFlow -v
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

真实 ASR 评测入口为 `python tools/check_quality.py <受控本地评测.json>`，格式及已确认的暂定门槛见[验收计划](docs/开发与验收计划.md#3-样本与质量验证)。本仓库没有真实授权样本，也尚无可启动的 React 或 Worker 服务；当前 Go 服务完成 M0 的后端环境与 M1 联调前置能力，尚未完成 M1 固定样例纵向联调。检查通过仅说明相应契约、SQL、草图行为、已实现接口或度量工具通过，不能代替后续音视频与模型验收。

## 前端（M0 骨架）

`frontend/` 为 React 19 + Vite + TypeScript 工程，M0 只读取 `contracts/examples` 的固定样例，不连后端。页面为课堂任务（`#/sessions`）、转写（`#/transcript`）、报告（`#/reports`）；样例引用不一致（例如 `run-partial` 与 `results` 的批次状态、报告溯源引用的修订）会在页面“样例数据核对”中显式列出，不拼成虚假链路。

```powershell
cd frontend
npm install        # Node.js 24 LTS；依赖锁在 frontend/package-lock.json
npm run dev        # 开发服务器，默认 http://localhost:5173
npm run check      # tsc --noEmit + vitest + vite build
```

`vite.config.ts` 已把 `/api` 代理到 `http://127.0.0.1:8080` 作为 M1 接入点，构建产物 `frontend/dist` 由 Go 同源提供。M0 尚未实现登录、鉴权、真实 API 调用与活动批次轮询。

## 使用 Codex

在 Codex 中打开克隆后的 Git 仓库根目录并新建任务，确认已读取 `AGENTS.md`；已有会话可直接要求重新读取。仓库级指令的加载方式见 [OpenAI 官方说明](https://learn.chatgpt.com/docs/agent-configuration/agents-md)。

成员可用下面的格式开始任务：

> 我负责【前端/后端/数据库/音频/视频】，本次实现【FR编号与具体功能】，使用【分支名】。请按 AGENTS.md 查阅相关文档，完成【交付项】并验证【验收条件】，说明需要其他模块配合的部分。
