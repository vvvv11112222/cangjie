# 给队长的模块交付与接入说明

更新日期：2026-10-20。交付定位：⑥号成员的独立课堂语言分析 demo，供代码审查、复现与接入；工程回归不等于真实课堂或整套比赛系统验收。

## 产出与职责

| 产出 | 对应文件 | 用途 |
| --- | --- | --- |
| ASR 与文本流程 | pipeline.go、segmentation.go、reading.go | 音轨、停顿切片、转写和保守阅读整理 |
| 六维分析与证据 | events.go、model.go、report_*.go、advice.go、semantic_contract.go、narrative_contract.go | 事件、逐字引用、结论及建议检查，全文疑点 |
| 提示词与标准 | prompt.txt、reading_prompt.txt、events_prompt.txt、audit_prompt.txt、rubric.md | 四个文本阶段与六维边界 |
| 任务与版本 | job.go、jobs.go、review.go、recovery.go | 本地队列、恢复、不可变版本和人工校对 |
| 教师页面 | web/ | 分区阅读、事实/分析/建议、时间脉络、回放和导出 |
| 测试与文档 | Go 测试、tests/、testdata/、scripts/、根目录文档 | 离线复现、历史失败回归、交付及演示说明 |

## 接入现有主项目

依据队长仓库的 [项目文档](https://github.com/vvvv11112222/cangjie/blob/main/项目文档.md)、[开发与验收计划](https://github.com/vvvv11112222/cangjie/blob/main/docs/开发与验收计划.md)、[正式报告 Schema](https://github.com/vvvv11112222/cangjie/blob/main/contracts/v1.schema.json)，变动后以主项目最新版本为准。

demo 是 package main、模块 cangjie-demo、本地 JSON 和专用 /api/jobs。主项目已有 internal/analysis/、PostgreSQL、媒体 Worker 与 React/TypeScript 页面。应扩展已有服务，不覆盖 cmd/api、主前端或共享协议。

| demo 能力 | 接入方向 | 必须补齐或保留 |
| --- | --- | --- |
| 音频与 ASR | 主项目媒体 Worker | 不绕过通信入口；原视频 T0、时间单位及偏移 |
| 转写与报告输入 | 转写修订、analysis run | 课堂归属、权限、固定版本、实际模型输入清单 |
| 本地任务队列 | 正式任务、租约与幂等处理 | 截止时间、失败分类、人工重试及计费未知状态 |
| 证据与回放 | 正式证据 DTO 和媒体代理 | 同 run 引用、访问控制，不输出永久本机路径 |
| 全文疑点与校对 | 正式审核发布流程 | 本地校对不等于已认证审核或正式发布 |
| 教师阅读交互 | React 报告页 | 迁移字段含义与交互，不直接复制原生入口 |

正式 Schema 限制额外字段。demo 的 Dimension 没有正式要求的 coverage；观察中的 title、analysis、verification、event_ids，报告中的 quality_audit 等不能原样提交。正式报告另有 run/session 身份、版本、哈希、来源及审核字段。

已在 demo 内提供最小候选适配包 integration/reportadapter、命令 cmd/adapt-report 及正式 Schema 测试，固定上游提交 8dc69286073090fffc5120a5d30946bfe883ee85。详见 integration/README.md。它使用可信服务端映射与最终模型输入，不生成业务 UUID，不写入主项目；原报告扩展保存在独立对象，需与队长确定存放位置，同步协议、样例、后端和前端。主项目内部调用、数据库与页面迁移仍需在主项目 PR 落地。

main.go 的旧 POST /api/run 与任务流程有重复媒体处理。正式接入时先明确迁移哪一条路径，再逐步收敛兼容入口；本次整理不改变运行流程。

## 待完成验收

- 已获准的清晰中文与噪声/远场课堂；独立测试集、人工文字及时间标注。
- 当前规则的事实支持、过度推断、建议价值和重要做法覆盖；模型提醒的误报、漏报。
- 当前块级定位最长 40 秒，非零音轨偏移、长录像首中尾漂移与句级定位仍待验证。
- Linux amd64 / Docker 实际运行；主项目数据库、权限、审核发布、预算及清理治理。
- 超过 10 分钟或 256MB 的完整课堂支持，当前不能宣传为已支持。

主项目 quality-gates.json 当前为 provisional_target：清晰 CER≤15%、噪声 CER≤30%、起止误差 P95≤1500ms，要求 held_out。它是队内暂定目标，不是比赛官方评分表。测量方法见 EVALUATION.md。

## 建议协作顺序

1. 队长审阅源码快照，确认接入目录、协议扩展位置与成员分工。
2. 审阅已实现的离线转换与契约测试，在主项目固定转写 → 六维候选 → validate 流程使用可信输入接入；保留现有检查与计费记录。
3. 对接媒体 Worker、数据库版本、来源清单、权限与审核发布，再接 React 页面。
4. 授权中文素材与学科人员验收，最后完成部署和整链测试，补齐参赛证据。

以上由相关成员协作，不要求语言分析成员重写整套主系统。

## 提交方式与范围

运行 node scripts/prepare-delivery.cjs --export，使用生成的 source/ 和外部 manifest.json 审查。包含全部根目录 Go 源码及测试、指定网页/脚本、提示词、当前文档、空密钥配置示例及必要公开测试资料，保留 MIT 署名与许可。

不提交 .env 及变体、本地凭据、data/、bin/、evaluation/、docs/history/、录像音频、缓存和完整任务快照。忽略规则不能清除其他仓库已经跟踪的秘密，实际 PR 仍需检查暂存区及历史。该脚本不操作 Git。

在队长仓库开任务分支和 PR，目录与队长协商；分支名可用 codex/ai-language-analysis。不要覆盖 main。公开素材的使用条件和仓库既有许可继续适用，本次不擅自给整个项目指定许可证。

## 建议 PR 描述

提供可追溯的课堂语言分析模块：固定转写提取教学事件，围绕六维生成事实、分析及具体建议，保留来源回放、输入版本和全文疑点。独立 demo 提供 ASR 与教师阅读页面，便于复现和主项目接入。

验证见 TEST-REPORT.md，普通回归不调用真实模型。独立服务尚未接入主项目，中文指标、精确时间定位和 Linux/Docker 待验收；不能将工程测试写成模型准确率。
