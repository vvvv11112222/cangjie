# 公开课程回归测试资料

本目录只保存普通 Go 测试实际依赖的七个固定文本文件，不包含视频、任务存储、请求日志、密钥或运行目录。它们来自此前已保存的 MIT 公开课测试，不是新增真实模型输出，也不是教师认可或当前版本的质量验收。

## 来源与使用条件

- 物理：[MIT OpenCourseWare，8.01SC Classical Mechanics，4.1 Newton’s First and Second Laws](https://ocw.mit.edu/courses/8-01sc-classical-mechanics-fall-2016/pages/week-2-newtons-laws/4-1-newtons-first-and-second-laws/)。
- 生物：[MIT OpenCourseWare，7.016 Introductory Biology，Lecture 4: Enzymes and Metabolism](https://ocw.mit.edu/courses/7-016-introductory-biology-fall-2018/resources/lecture-4-enzymes-and-metabolism/)。
- 来源标注为 [CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/)；保留 MIT OpenCourseWare 署名，遵守非商业和相同方式分享条件，第三方另有标注时依其条件。本说明不为 Go/JavaScript 源码另行指定许可证。
- 转写及其派生的历史报告保留错误、不完整说明和 AI 复核疑点，不能作为学科标准答案。字幕仅供固定来源比对，默认模型分析不注入字幕。

## 文件用途

| 文件 | 用途 |
| --- | --- |
| `AI辅助验收记录.json` | 四份来源的路径、原始 SHA256 和十五条来源定位；明确 `human_verified=false` |
| `results/retest-guided-review-v2-job-ead802c499b539da-v2.json` | 物理历史导出报告、转写及事件 |
| `results/retest-guided-review-v2-job-7b33d23c8ce0e4f1-v2.json` | 生物历史导出报告、转写及事件 |
| `sources/mit-physics.vtt`、`sources/mit-biology.vtt` | 已归档的参考字幕，不随附视频 |
| `replays/physics-review.json` | 旧全文检查的固定失败案例，SHA256 为 `3089282b74cfb36c200eea5990858f3cc5fca02a661338e6d2be25fc42db4b8a` |
| `replays/physics-events.json` | 将“注意”误判为学生活动的固定案例，SHA256 为 `5899415bbfbefe75fe4779e355652d50f30a2ecf7086e171c0f980cbb73f3875` |

七个文件均从原本地归档逐字节复制，原文件留在被忽略的 `evaluation/` 中。测试继续检查既有 SHA256，不通过替换错误样本来改善结果。此目录必须随源码交付，否则公开课程、叙述边界和事件校验回归会缺少输入。

运行 `scripts/test.ps1` 或 `sh scripts/test.sh` 不会调用真实模型。需要有费用的文本复测时，单独使用 `scripts/test-public-courses.ps1 -Live`，并先阅读其调用数量提示；输出写入被忽略的本地实验目录。
