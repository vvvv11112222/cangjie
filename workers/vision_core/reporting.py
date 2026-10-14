from collections import Counter
import csv
import html
import io
import base64
from pathlib import Path

DISPLAY_NAMES={'hand_raise':'举手','head_down':'低头','leave_seat':'离座','possible_phone':'疑似持手机',
               'entry':'进入教室','exit':'离开教室','late_entry':'可能迟到','early_exit':'可能早退'}
DISPLAY_STATUS={'candidate_found':'发现动作，待核对','no_candidate':'可判断的画面中未发现',
                'insufficient':'画面或条件不足','not_enabled':'未开启'}


def clock(ms):
    seconds = ms / 1000
    return f"{int(seconds // 60):02d}:{seconds % 60:06.3f}"


def build_report(job_id, title, metadata, config, points, events, snapshots, stats, provenance, coverage, elapsed, limitations):
    known = [p["visible_count"] for p in points if p["visible_count"] is not None]
    counters = Counter(e["event_type"] for e in events)
    methods = {
        "hand_raise": (stats["hand_evaluable"] > 0, "检查手臂是否抬起；每张画面与持续举手时间段分别统计，托腮、摸脸仍可能混淆"),
        "leave_seat": (stats["seat_evaluable"] > 0, "检查是否离开原来的座位位置；自动画出的座位需要核对，站起来不一定是离座"),
        "head_down": (config.head_down_enabled and stats["head_evaluable"] > 0, "检查可见脸部是否朝下；侧脸、遮挡和远处的小脸可能无法判断，低头不代表走神"),
        "possible_phone": (stats["phone_evaluable"] > 0, "查找手边是否出现手机；手机太小容易漏检，出现手机不等于正在使用"),
        "late_entry": (config.schedule_enabled and bool(config.door_roi) and stats["valid_frames"] > 0, "入口区中线方向穿越 + 录像内课堂时间；不关联个人身份"),
        "early_exit": (config.schedule_enabled and bool(config.door_roi) and config.class_end_s is not None and stats["valid_frames"] > 0, "需入口区、室内方向和课堂结束位置；不关联个人身份"),
    }
    capabilities = []
    hand_positive_frames = sum((p.get('hand_raise_count') or 0)>0 for p in points)
    hand_coverage = stats['hand_evaluable'] / max(1,stats.get('person_instances',0))
    for kind, (available, method) in methods.items():
        count = counters[kind]
        reason = None
        if not available:
            if kind == "leave_seat": reason = "尚未建立稳定座位区域或缺少可靠上身位置"
            elif kind == "head_down": reason = "低头分析未开启" if not config.head_down_enabled else "脸部过小、不可见或头姿超出可靠观察范围"
            elif kind == "late_entry": reason = "课堂时间判定未开启；目前只检测入口候选及进出" if not config.schedule_enabled else "未配置入口区域"
            elif kind == "early_exit": reason = "课堂时间判定未开启；目前只检测入口候选及进出" if not config.schedule_enabled else "未配置入口区域或课堂结束时间"
            elif kind == "possible_phone": reason = "缺少尺寸足够的可见人体，无法观察人与手机位置关系"
            else: reason = "手臂被遮挡或看不清，暂时不能判断是否举手"
        positive_frames = hand_positive_frames if kind=='hand_raise' else sum((p.get('head_down_count') or 0)>0 for p in points) if kind=='head_down' else 0
        partial_hand = kind=='hand_raise' and hand_coverage < .8
        if partial_hand and not count and not positive_frames:
            reason = f"能判断是否举手的部分占 {hand_coverage:.1%}，其他部分看不清，不能说全班都没有举手。"
        disabled=(kind=='head_down' and not config.head_down_enabled) or (kind in ('late_entry','early_exit') and not config.schedule_enabled)
        capabilities.append({"event_type": kind, "status": "not_enabled" if disabled else "candidate_found" if count or positive_frames else "insufficient" if partial_hand else "no_candidate" if available else "insufficient",
                             "candidate_count": count if available else None, "method": method, "reason": reason,
                             "positive_sample_frames": positive_frames if kind in ('hand_raise','head_down') else None,
                             "evaluable_person_observations": stats.get({"hand_raise":"hand_evaluable", "leave_seat":"seat_evaluable", "head_down":"head_evaluable", "possible_phone":"phone_evaluable"}.get(kind)),
                             "coverage_note": "能力可用仅表示部分画面可观察，不代表覆盖所有人和所有时间。"})
    dimensions = []
    unsupported = {
        "content": ("内容", "本工具未转写语音或核对授课主题，不能评价内容正确性和目标达成。"),
        "pace": ("节奏", "视觉事件曲线仅反映画面观察，不能替代讲授、停顿与互动时间分析。"),
        "thinking": ("思维", "缺少具体提问、追问与学生回应文本。"),
        "expression": ("表达", "未进行语音清晰度、语速、课件或板书内容分析。"),
        "technology": ("技术", "录像编码和画面质量属于采集条件，不能据此评价教师的教学技术运用。"),
    }
    for code in ("content", "pace", "thinking", "expression", "management", "technology"):
        if code == "management":
            dimensions.append({"dimension_code": code, "name": "管理", "status": "visual_observations_only",
                               "summary": f"实际采样 {len(points)} 帧，{len(known)} 帧可进行人数观察；发现 {len(events)} 条行为/进出候选，复核状态见事件列表。",
                               "limitation": "不能确认个人出勤、违规或管理质量，不自动打分。"})
        else:
            name, reason = unsupported[code]
            dimensions.append({"dimension_code": code, "name": name, "status": "insufficient", "summary": "证据不足", "limitation": reason})
    return {
        "schema_version": "vision-lab-0.4", "job_id": job_id, "title": title,
        "report_type": "local_visual_observation", "is_team_contract_result": False,
        "metadata": metadata, "configuration": config.model_dump(), "provenance": provenance,
        "coverage": coverage, "statistics": {**stats, "sample_count": len(points), "valid_count_samples": len(known),
            "average_visible_count": round(sum(known) / len(known), 2) if known else None,
            "min_visible_count": min(known) if known else None, "max_visible_count": max(known) if known else None,
            "elapsed_seconds": round(elapsed, 2), "candidate_counts": dict(counters),
            "max_simultaneous_hand_raise": max((p['hand_raise_count'] for p in points if p.get('hand_raise_count') is not None),default=None),
            "hand_positive_sample_frames": hand_positive_frames, "hand_evaluable_ratio": round(hand_coverage,4),
            "head_positive_sample_frames":sum((p.get('head_down_count') or 0)>0 for p in points),
            "max_simultaneous_head_down":max((p['head_down_count'] for p in points if p.get('head_down_count') is not None),default=None)},
        "capabilities": capabilities, "counts": points, "events": events, "snapshots": snapshots,
        "dimensions": dimensions, "limitations": limitations,
        "quality_validation": {"held_out_validation_completed": False,
                               "precision": None, "recall": None, "count_mae": None,
                               "reason": "本次是实际模型推理，尚未对独立人工标注集验收；模型分数不是正确率。"},
        "recommendations": [
            "先回放候选事件的证据时刻，逐条确认、修正或驳回，再用于课堂复盘。",
            "检查学生区、座位区、入口区是否正确，排除教师、走廊与镜头切换影响。",
            "在不同课堂的独立人工标注集上测量人数误差及逐类精确率/召回率。",
            "需要完整六维教学报告时，再接入语音转写、课程信息和人工复核后的视觉证据。",
        ],
    }


def markdown_report(report):
    s, c = report["statistics"], report["coverage"]
    title = report["title"].replace("\n", " ")
    lines = [f"# {title}", "", "本报告记录系统从录像中发现的动作，需要结合原画面核对。看不清的部分不会算作没有动作。", "",
             f"- 录像时长：{clock(report['metadata']['duration_ms'])}",
             f"- 实际分析范围：{clock(c['start_ms'])}—{clock(c['end_ms'])}",
             f"- 时间范围覆盖：{c['percent_of_video']}%；每隔 {1/report['configuration']['sampling_fps']:g} 秒检查一次画面，不代表每个人的动作都看得清。",
             f"- 检查了 {s['sample_count']} 张画面，其中 {s['valid_count_samples']} 张能统计可见人数。",
             f"- 平均可见人数：{s['average_visible_count']}；范围：{s['min_visible_count']}—{s['max_visible_count']}",
             f"- 计数方法：{report['provenance'].get('counting_method','person')}；逐帧举手峰值：{s.get('max_simultaneous_hand_raise','未知')}；出现举手的采样帧：{s.get('hand_positive_sample_frames','未知')}",
             f"- 分析耗时：{s['elapsed_seconds']} 秒；执行器：{report['provenance'].get('provider','CPUExecutionProvider')}", "",
             "## 动作观察结果", "", "| 项目 | 观察结果 | 持续动作段数 | 方法或限制 |", "| --- | --- | --- | --- |"]
    for cap in report["capabilities"]:
        lines.append(f"| {DISPLAY_NAMES.get(cap['event_type'],cap['event_type'])} | {DISPLAY_STATUS.get(cap['status'],cap['status'])} | {cap['candidate_count'] if cap['candidate_count'] is not None else '暂时无法判断'} | {cap['reason'] or cap['method']} |")
    if 'hand_evaluable_ratio' in s:
        lines += ["", "## 发现举手的画面", "",
                  f"能判断是否举手的部分占 {s['hand_evaluable_ratio']:.1%}。这里只统计看得清的动作；遮挡、远处的人可能遗漏，摸脸也可能混淆。一个人一直举手会出现在多张画面里，所以画面张数不等于举手次数。", "",
                  "| 录像位置 | 发现举手人数 | 能判断是否举手的人数 | 可见人数 |", "| --- | --- | --- | --- |"]
        positive=[p for p in report['counts'] if (p.get('hand_raise_count') or 0)>0]
        for point in positive[:150]:
            lines.append(f"| {clock(point['timestamp_ms'])} | {point['hand_raise_count']} | {point.get('hand_evaluable_count','未知')} | {point['visible_count']} |")
        if not positive:
            lines.append("| 能判断的画面中未发现举手，仍可能有遗漏 | — | — | — |")
        if len(positive)>150:
            lines.append("报告展示前 150 个正例采样时刻，完整逐帧数据见 CSV / JSON。")
    lines += ["", "## 发现动作的时间段", ""]
    for event in report["events"]:
        review={'unreviewed':'待人工核对','accepted':'人工已确认','rejected':'人工已排除'}.get(event['review_status'],event['review_status'])
        lines.append(f"- {DISPLAY_NAMES.get(event['event_type'],event['label'])}：{clock(event['start_ms'])}—{clock(event['end_ms'])}，人物编号 {event['track_id']}，{review}。")
    if not report["events"]:
        lines.append("本次未产生可报告候选；需结合上方能力状态判断，不能据此认定没有行为。")
    lines += [""]
    if report.get('layout'):
        layout=report['layout']
        lines += ["## 自动区域观察", "", f"座位区域 {len(layout['seats'])} 个；入口状态：{layout['entrance_status']}。",layout['seat_note'],layout['entrance_note'],""]
    if report.get('persons'):
        lines += ["## 按人物查看", "", "编号用于在本次录像中查找人物，不代表姓名。遮挡或镜头切换后，同一个人的编号可能改变。画面张数不等于动作次数。", "",
                  "| 人物编号 | 出现时间 | 检查画面数 | 发现举手的画面数 | 发现低头的画面数 |", "| --- | --- | --- | --- | --- |"]
        for person in report['persons']:
            lines.append(f"| {person['track_id']} | {clock(person['first_ms'])}–{clock(person['last_ms'])} | {person['sample_count']} | {person['summary']['hand_raise']} | {person['summary']['head_down']} |")
    lines += ["", "## 逐帧复核记录", "", "模型原始判断与复核结果分别保留；单帧复核不代表确认整段持续事件，未复核内容仍为自动分析草稿。", "",
              "| 编号 | 时间 | 行为 | 模型原值 | 复核结论 | 依据 | 复核者 |", "| --- | --- | --- | --- | --- | --- | --- |"]
    for person in report.get('persons',[]):
        for o in person['observations']:
            for kind,record in o.get('reviews',{}).items():
                def verdict(value):return '检出' if value is True else '未见' if value is False else '无法判断'
                clean=lambda v:str(v).replace('|','／').replace('\n',' ')
                lines.append(f"| {person['track_id']} | {clock(o['timestamp_ms'])} | {kind} | {verdict(o['behaviors'][kind])} | {verdict(record['value'])} | {clean(record['reason'])} | {clean(record['reviewer'])} |")
    lines += ["", "## 六维证据完整性", ""]
    for dim in report["dimensions"]:
        lines.append(f"- {dim['name']}：{dim['summary']} {dim['limitation']}")
    lines += ["", "## 分析限制", ""] + [f"- {v}" for v in report["limitations"]]
    lines += ["", "## 下一步建议", ""] + [f"- {v}" for v in report["recommendations"]]
    lines += ["", "## 模型与验收状态", "", report["quality_validation"]["reason"], ""]
    for name, item in report["provenance"]["weights"].items():
        lines.append(f"- {name}：SHA-256 `{item['sha256']}`；来源 {item['url']}")
    return "\n".join(lines)


def csv_text(value):
    # CSV quoting does not stop spreadsheet formula interpretation. Preserve JSON originals.
    if isinstance(value, str) and (
        value.startswith(('\t', '\r', '\n')) or value.lstrip().startswith(('=', '+', '-', '@'))
    ):
        return "'" + value
    return value


def csv_counts(report):
    buffer = io.StringIO(newline="")
    writer = csv.writer(buffer)
    keys = ["timestamp_ms", "frame_index", "visible_count", "brightness", "sharpness", "valid", "pose_evaluated", "hand_raise_count", "hand_evaluable_count", "head_down_count", "head_evaluable_count"]
    writer.writerow(keys)
    for row in report["counts"]:
        writer.writerow([csv_text(row.get(k)) for k in keys])
    return "\ufeff" + buffer.getvalue()


def csv_persons(report):
    buffer=io.StringIO(newline='');writer=csv.writer(buffer)
    keys=['track_id','timestamp_ms','frame_index','seat_id','hand_raise','head_down','leave_seat','possible_phone','pitch','yaw','roll','pose_available']
    review_keys=[f'{kind}_{field}' for kind in keys[4:8] for field in ('review_value','review_reason','reviewer','reviewed_at')]
    writer.writerow(keys+review_keys+['original_file','annotated_file','detection_confidence'])
    for person in report.get('persons',[]):
        for o in person['observations']:
            row = [person['track_id'],o['timestamp_ms'],o['frame_index'],o['seat_id'],
                             *['unknown' if o['behaviors'][k] is None else int(o['behaviors'][k]) for k in keys[4:8]],
                             *[(o.get('head_angles') or {}).get(k) for k in ('pitch','yaw','roll')],o['pose_available'],
                             *[('' if k not in o.get('reviews',{}) else ('unknown' if o['reviews'][k]['value'] is None else int(o['reviews'][k]['value']))) if field=='value' else o.get('reviews',{}).get(k,{}).get(field,'')
                               for k in keys[4:8] for field in ('value','reason','reviewer','reviewed_at')],
                             *[o.get(k,'') for k in ('original_file','annotated_file','detection_confidence')]]
            writer.writerow([csv_text(value) for value in row])
    return '\ufeff'+buffer.getvalue()


def html_report(report, directory):
    esc = html.escape
    body = []
    table = False
    for line in markdown_report(report).splitlines():
        if line.startswith('|'):
            if line.startswith('| ---'): continue
            cells = [esc(c.strip()) for c in line.strip('|').split('|')]
            if not table:
                body.append('<table><thead><tr>'+''.join(f'<th>{c}</th>' for c in cells)+'</tr></thead><tbody>')
                table = True
            else:
                body.append('<tr>'+''.join(f'<td>{c}</td>' for c in cells)+'</tr>')
            continue
        if table:
            body.append('</tbody></table>')
            table = False
        if line.startswith("# "): body.append(f"<h1>{esc(line[2:])}</h1>")
        elif line.startswith("## "): body.append(f"<h2>{esc(line[3:])}</h2>")
        elif line: body.append(f"<p>{esc(line)}</p>")
    if table: body.append('</tbody></table>')
    reviewed_frames={o['timestamp_ms']:o for p in report.get('persons',[]) for o in p['observations'] if o.get('reviews')}
    if reviewed_frames:
        body.append('<h2>逐帧复核原画面（最多 30 张）</h2>')
        for timestamp,o in list(sorted(reviewed_frames.items()))[:30]:
            filename=o.get('original_file')
            if not filename:continue
            path=directory/'evidence'/filename
            if path.is_file():
                encoded=base64.b64encode(path.read_bytes()).decode('ascii')
                body.append(f"<figure><img src='data:image/jpeg;base64,{encoded}'/><figcaption>复核对应原画面 {clock(timestamp)}</figcaption></figure>")
    body.append("<h2>证据画面（最多 30 张，标注图供辅助复核）</h2>")
    for snapshot in report["snapshots"][:30]:
        path = directory / "evidence" / snapshot["annotated_file"]
        if path.is_file():
            encoded = base64.b64encode(path.read_bytes()).decode("ascii")
            body.append(f"<figure><img src='data:image/jpeg;base64,{encoded}'/><figcaption>原录像时间 {clock(snapshot['timestamp_ms'])}</figcaption></figure>")
    return "<!doctype html><html lang='zh-CN'><meta charset='utf-8'><title>课堂视觉报告</title><style>body{max-width:960px;margin:40px auto;padding:0 24px;font:16px/1.8 system-ui;color:#173044}h1,h2{color:#0f766e}p,td{overflow-wrap:anywhere}table{width:100%;border-collapse:collapse;font-size:13px}th,td{border:1px solid #dfe8e9;padding:8px;text-align:left}th{background:#e6f4f0}figure{display:inline-block;width:45%;vertical-align:top;margin:12px}img{width:100%;border-radius:8px}@media print{figure{break-inside:avoid}}</style>" + "".join(body) + "</html>"
