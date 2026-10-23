"""Assemble internal visual observations for the formal Worker adapter."""
from collections import Counter


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
        "schema_version": "cangjie-vision-internal-v1", "job_id": job_id, "title": title,
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
