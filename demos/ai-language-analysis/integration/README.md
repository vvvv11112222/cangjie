# 主项目接入与自动验收

更新日期：2026-10-20。这里只实现离线候选适配和验收工具；没有修改队长仓库、业务数据库、模型请求、教师页面或发布流程。

## 固定协议

依据 [主项目提交 8dc6928](https://github.com/vvvv11112222/cangjie/tree/8dc69286073090fffc5120a5d30946bfe883ee85)，保留完整 v1.schema.json、quality-gates.json 和原版质量计算器及测试。来源和 Git blob SHA 在 upstream/provenance.json；测试与交付检查会验证字节未变。主项目更新后应审查差异，整体更新契约、消费者和测试，不能单独放宽本地规则。这些文件继承上游仓库适用的许可，本目录未另行重新许可。

主项目使用六维 ReportCandidate，明确要求 coverage、UUID 证据与严格字段。没有供客户端任意提交报告的公开入口；候选应接入既有 internal/analysis 的 report/validate 阶段。

## 候选转换

Go 包 integration/reportadapter 提供 Convert(reportJSON, modelInputJSON, context)，cmd/adapt-report 是离线命令。包只用标准库，不依赖 demo 的 package main，可供队长审阅迁移。

```powershell
go run ./cmd/adapt-report --report <纯Report文件> --model-input <最终ModelInput文件> --context <可信绑定文件>
```

三个输入均为本地 UTF-8 JSON。报告必须是通过 demo 原有事件、证据与报告校验的 Report 对象，不是整份任务导出或失败候选；转换器不替代语义校验。ModelInput 是主项目最终选取、截断后精确序列化的原始字节。context 的示意字段见 testdata/adapter-input.json；该文件是合成测试，COMPUTE_FROM_* 标记会由测试计算，不能直接作为真实上下文使用。

| 输入 | 提供方和约束 |
| --- | --- |
| run/session/media/revision UUID | 主项目服务端；不从模型或浏览器接受新身份 |
| input_sha256 | 该分析批次的源输入摘要，不是清单摘要 |
| manifest_sha256 | 最终 ModelInput 原始 UTF-8 字节 SHA256，不能在转换后重新计算替换预期值 |
| duration_ms | 主项目探测的原视频时长 |
| source_timeline_confirmed | 媒体成员验证共用源 T0 后明确确认；不能由转换器推定 |
| bindings | 服务端登记的 segment_id → evidence_id；带归属、可用性、原文和原时间范围 |

转换器拒绝跨批次/课堂/媒体、不可用或未发送的证据、重复映射、文本/时间变化、修订不符、错误摘要、越界/重叠覆盖以及无依据结论。当前桥接仅支持 transcript 证据；存在 manual/frame/behavior 时明确失败，等待专门接入。文本被主项目截断时，不能把完整段落的旧结论绑定到截断片段；应先用相同的最终证据重新生成。

输出分成 candidate 和 extensions。只有 candidate 符合正式候选 Schema，包含六维、原观察文字与完整 suggestion；coverage 原样采用实际输入窗口，保留空隙，不解释为行为持续区间。extensions 保存原报告全部内容，包括 title、analysis、verification、event_ids 和 quality_audit，并记录候选摘要用于防止错配。主项目尚不接受这个扩展对象；需与队长确定存储和 React 展示方式，避免迁移后丢失有用内容。扩展含课堂正文，同样受权限和清理约束，不能提交实际输出到 GitHub。

extensions 的 candidate_sha256 按 Go 候选结构 JSON 紧凑序列化计算，source_report_sha256 按保留对象原字段顺序经 Go json.Marshal(RawMessage) 紧凑序列化计算，使用默认 HTML 字符转义规则；重新排序键会改变摘要。它们不等同于主项目业务报告的 content_sha256。

哈希和绑定只检查结构与内容一致性，不能证明某次模型调用实际发生、来源获准或输入与旧报告的生成历史一致。调用方仍须用可信账目/请求记录关联报告及最终输入，沿用主项目当前许可、有效租约、实际请求体摘要、发布审核和过期检查。不要把历史 demo 报告补一组新 UUID 后当作已完成正式接入。

## 自动检查

需要 Go、Python 和 jsonschema；本机测试版本为 Python 3.13 / jsonschema 4.23.0，主项目 Python 基线仍为 3.12。必要时在团队开发环境执行 python -m pip install -r integration/requirements.txt，本次没有安装系统依赖。

```powershell
powershell -ExecutionPolicy Bypass -File scripts/test-integration.ps1
```

Linux 用 sh scripts/test-integration.sh。检查包括实际编译并运行转换命令、用原版 Schema 与 UUID format 校验输出、扩展隔离、上游文件字节校验、冻结参考测试及原版质量计算器测试。Go 普通测试还覆盖24类错误输入和输出保留规则。测试产物存 bin/verification/，不进入交付。

## 中文质量测量准备

quality.py 调用原版计算器，不改变 CER、P95 或门槛。额外冻结参考文字、人工起止点、场景、源摘要和授权记录引用，模型输出变化不会改变参考集合摘要。改动冻结参考、来源或 synthetic 标志会被拒绝；来源摘要和授权记录仍由负责素材的人核验。

目前没有真实中文录像或人工参考。可先用 testdata/quality-draft.json 演练；里面是人工编造的文字和时间，不是 ASR 运行结果。

```powershell
# evaluation 目录已被忽略；每次输出使用新文件名，不覆盖既有验收集。
python integration/quality.py freeze integration/testdata/quality-draft.json --output evaluation/quality-synthetic-frozen.json
python integration/quality.py check evaluation/quality-synthetic-frozen.json
```

freeze 退出0只表示参考已冻结；check 对该合成样例会输出 metrics_passed=true、acceptance_passed=false，并按主项目约定退出1。输入非法退出2。正式样本清晰与噪声两场景都须具备，时间均为原录像整数毫秒；没有参考文字、漏场景、越界时间不能充当通过。

正式使用时，将获准素材和人工标注放在忽略的 evaluation/，填写真实 source_records、source_id、reference、duration_ms 及 reference_start_ms/reference_end_ms。先冻结并归档独立 held_out 参考；hypothesis 与 actual_* 只作为待测结果填写或替换。首次冻结允许这些字段先填占位，不能把占位产生的指标记为实测。参考变动须另建并说明新数据集，不能调参后重冻同一验收集。synthetic=false 只允许负责人确认真实来源后填写，工具不会自动把合成样例转为真实。

官方计算器可直接读取冻结输出：python integration/upstream/tools/check_quality.py <冻结文件>。有参考的 ASR 指标、教师对建议的认可、非零音轨偏移和长录像漂移仍需分别验收；块级时间窗不能当作句级精度。

## 部署检查

本机无可用 Docker/WSL Linux 运行时。Linux 交叉编译只证明可构建，不能作为容器运行通过。

本机独立启动检查先编译新文件，再执行：

```powershell
go build -o bin/verification/demo-smoke.exe .
node scripts/deploy-smoke.cjs bin/verification/demo-smoke.exe
```

Linux 同样可用 go build -o bin/verification/demo-smoke . 和 node scripts/deploy-smoke.cjs bin/verification/demo-smoke。脚本在新临时目录启动、使用独立端口及空数据，屏蔽模型凭据，检查健康、嵌入网页、空任务列表和 FFmpeg 生成/探测的1秒单声道16kHz音频，然后停止它自己启动的进程。日志及结果留在 bin/verification/，不会重启当前教师服务。

有 Docker 后可执行：

```powershell
python scripts/docker-smoke.py --goproxy https://goproxy.cn,direct
```

该脚本不读 .env、不启动 Compose、不挂现有数据卷；构建临时镜像并启动临时容器，验证非 root、健康、网页、空存储、FFmpeg/FFprobe 和 SIGTERM。退出时只清理本次临时容器和镜像，保存结果与日志。网络下载失败也记为失败；只有真实执行成功的 result.json 才能登记容器冒烟通过。主项目 PostgreSQL、Worker、权限、审核发布和模型质量不在该脚本范围。
