# 测试报告：工程回归与验收边界

更新日期：2026-10-20。当前报告 six-dim-coaching-v8、正文 bounded-narrative-v4、建议 grounded-advice-v7、全文检查 context-review-v9。工程测试、真实模型记录与质量验收分别说明。

## 可复现检查

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\test.ps1 -NodeExecutable 'D:\Program Files\nodejs\node.exe'
```

Linux 用 sh scripts/test.sh。普通脚本强制关闭真实模型测试，Go 使用本地模拟提供方；合成媒体测试需 FFmpeg/FFprobe，缺失时明确跳过。

| 检查 | 验证范围 |
| --- | --- |
| Go 全量测试 | 切片、恢复、任务、版本冲突、事件引文、生成与复核结构、建议来源、失败诊断、校对与兼容入口 |
| go vet | Go 静态检查 |
| 前端单元测试 | 转写、恢复、中文展示、待确认正文、依据去重、讲解脉络、版本绑定 |
| 交付测试 | 源码与固定输入齐全、排除本地数据、拒绝疑似密钥/越界/链接、独立快照及哈希 |
| 页面语法 | jobs.js、recovery.js、analysis.js、events.js |
| 浏览器回归 | analysis.browser.cjs、jobs.browser.cjs、recovery.browser.cjs，本地模拟接口 |

浏览器需 Playwright 与 Chromium，可用 PLAYWRIGHT_MODULE、BROWSER_EXECUTABLE 指定路径，截图写入 bin/qa/。

## 当前页面与本轮整理

上一轮 Go、静态检查、23 项前端测试和三组浏览器回归通过；覆盖 1200/768/390px、目录与键盘切换、阅读位置保留、正文及待确认建议完整可见、无重复副本、回放和校对跳转、CSS 资源。输入为合成报告，不代表真实内容已验收。

本轮增加 4 项交付测试，迁移普通回归依赖的固定公开输入，新增明确源码清单。原 demo 和独立导出快照中，Go 全量测试、go vet、27 项 Node 测试及页面语法检查均通过；独立快照的 Windows 编译也通过。没有调用真实模型或 ASR。FFmpeg/FFprobe 在本机可用，Docker 不可用，因此不声明容器运行通过。网页与生成流程本轮未改动，浏览器结果引用上述上一轮记录，没有重新运行。

此前交付快照为89文件；加入下面的接口和验收工具后，当前源码清单为108文件。最终快照在回归完成后重新导出，代码及固定输入与已测试快照逐文件比较；测试和编译产生的临时文件不放入最终快照。实际文件大小及 SHA256 以快照同级 manifest.json 为准。

## 主项目适配与自动验收工具（2026-10-20）

固定主项目提交 8dc69286073090fffc5120a5d30946bfe883ee85 的完整 Schema、暂定门槛和原版质量计算器。上游文件 Git blob SHA 经 Python 与交付脚本验证；没有修改主项目共享协议或数据库。

| 检查 | 本轮结果和证据范围 |
| --- | --- |
| Go 全量测试、go vet | 原 demo 与独立源码快照通过；适配包另含4项测试、24类异常子用例 |
| 前端与交付 | 原 demo 与独立快照27项 Node 测试及网页语法通过；交付测试补充新包/脚本齐全和固定协议校验 |
| 正式契约与冻结参考 | 9项 Python 测试通过：实际编译/运行转换命令、原版 Schema、UUID format、扩展隔离、来源摘要、冻结参考与错误输入 |
| 原版质量计算器 | 上游6项测试通过，CER/P95边界、缺场景和时间越界与队长口径一致 |
| 质量工具命令演练 | 合成冻结、包装计算器和原版计算器均运行；两者指标一致，acceptance_passed=false、退出1；不登记真实准确率 |
| Windows 独立启动 | 原 demo 与独立快照的健康、嵌入网页、空任务存储、404、FFmpeg生成与FFprobe探测1秒16kHz单声道音频通过 |
| Linux amd64 构建 | 独立快照的 demo 和 adapt-report 交叉编译通过；未实际运行Linux程序 |
| Docker / WSL | 本机无可用运行时；Docker脚本语法检查通过，执行预检明确退出2，不计为容器运行通过 |

已测试独立快照为 bin/delivery/2026-10-20T12-49-34-877Z-026898/source。Windows冒烟结果位于该目录的 bin/verification/deploy-smoke-nBBuVS/result.json；原 demo 结果为 bin/verification/deploy-smoke-gMzomd/result.json。它们是本机验收证据，不进入最终源码包。没有调用真实模型、使用现有课堂数据或重启当前教师服务。

适配器只转换受信任的候选输入，不能证明模型调用或发布权限。报告中的教学分析、检查方法和全文疑点保存在独立 extensions；正式存储/前端支持仍需队长协作。具体输入、限制与复现命令见 integration/README.md。

## PR34 审查修复回归（2026-10-20）

- 已知历史分析版本 v1–v7 可继续读取、保存和导出人工校对；测试覆盖持久化重载后原报告与哈希不变，未知版本和无效历史证据仍拒绝。
- 带“注意单位并计算”或“listen … and write”要求的真实任务保留；纯注意提醒、教师讲解及没有实际动作的礼貌用语仍不能充当活动依据。活动筛选只是词句材料检查，语义仍须模型及人工核对。
- 空观察数组通过离线转换和正式 Schema 校验；null 数组继续失败，无依据时不补造发现。
- Windows 启动脚本在独立导出目录实际执行，普通 `.env` 端口、引号和注释、进程环境变量优先级三组启动检查通过；使用空任务存储，未调用真实模型。

修复后 Go 全量测试（FFmpeg/FFprobe 可用）、go vet、27项 Node 测试、10项契约与冻结参考测试、6项原版质量计算器测试、三组模拟接口浏览器回归通过。同步最新 main 后的主项目 Go 单元测试、静态检查及文档检查通过（171个契约样例、95个接口映射、20份文档）；未重跑数据库实机集成。真实课堂质量、主项目正式接入与 Linux/Docker 实际运行验收边界保持不变。

## 固定公开样本

testdata/public-courses/ 包含七个固定文本输入，保留原始字节及 SHA256；四份来源在 AI辅助验收记录.json，两个历史重放的校验值保留在 Go 测试内。Git 属性禁止自动换行。缺少或变化会失败，不通过跳过或替换旧错误来改善结果。

样本验证历史失败定位、分类提醒隔离、被动听讲不能充当学生活动及复核不改历史。模拟回复不证明真实模型能发现问题。来源及许可见 testdata/public-courses/README.md。

## 真实模型及系统验收

已有英语物理/生物短片、文本复测和成对合成案例在较早规则下运行，原始记录留在本地 evaluation/；不作为当前中文或教师金标准。

| 项目 | 状态 |
| --- | --- |
| 当前中文 CER / 术语统计 | 未验收 |
| 原视频时间轴与定位误差 P95 | 未验收 |
| 学科事实支持与建议可用性 | 未验收 |
| Linux / Docker 实际运行 | 未验收；仅Linux交叉编译与Windows独立启动通过 |
| 主项目数据库、权限、Worker、审核发布整链 | 未接入验收 |
| 完整长课堂 | 当前限 10 分钟/256MB，不宣称已支持 |

真实模型测试需要显式 -Live 或相应 BAILIAN_LIVE_*_TEST=1，可能计费。工程测试数不等于准确率或生成成功率。测量见 EVALUATION.md。

## 交付复现

node scripts/prepare-delivery.cjs --check 检查固定资料、明确源码清单和常见密钥格式，不读取 .env、data、evaluation。--export 在 bin/delivery/ 新建快照并保存每个文件大小和 SHA256。

在快照 source/ 内重新执行普通测试及编译，证明不依赖开发机未提交资料。检查不等于全面安全审计，实际 PR 仍需检查暂存区、历史和素材许可。

整理前完整工作记录保留在本机 docs/history/TEST-REPORT-before-handoff-2026-10-20.md，旧程序名、旧页面与当时端口状态不作为当前启动说明。
