"""Portable local visual results for joint integration; never a StageResult DTO."""
import copy
import hashlib
import json
import math
from pathlib import Path
import re
from uuid import UUID

KINDS = ('hand_raise', 'head_down', 'leave_seat', 'possible_phone')


def integer(value, label, minimum=0):
    if type(value) is not int or value < minimum:
        raise ValueError(label + ' 必须是有效整数')
    return value


def number(value):
    return type(value) in (int, float) and math.isfinite(value)


def export_handoff(report_path, output, *, binding=None, include_images=True,
                   max_image_bytes=512 * 1024**2):
    """No model load, no network and no original video copy. Unknown stays null."""
    report_path, output = Path(report_path).resolve(), Path(output).resolve()
    if output.exists() or output.is_relative_to(report_path.parent):
        raise ValueError('请使用报告目录之外的新输出目录，避免覆盖原报告')
    raw = report_path.read_bytes()
    report = json.loads(raw)
    if report.get('schema_version') != 'vision-lab-0.4' or report.get('is_team_contract_result') is not False:
        raise ValueError('需要现有 analyze-local 生成的本地视觉报告')
    if binding is not None:
        if set(binding) != {'session_id', 'run_id', 'media_asset_id'}:
            raise ValueError('课堂、批次、媒体编号必须成套提供')
        binding = {key: str(UUID(value)) for key, value in binding.items()}
    metadata = report['metadata']
    duration = integer(metadata['duration_ms'], '录像时长', 1)
    width, height = (integer(metadata[k], k, 1) for k in ('width', 'height'))
    if not re.fullmatch('[0-9a-f]{64}', metadata['sha256']):
        raise ValueError('原录像摘要无效')
    def timestamp(value):
        value = integer(value, '原录像毫秒时间')
        if value >= duration:
            raise ValueError('观察时间超出原录像')
        return value
    counts = copy.deepcopy(report['counts'])
    times = [timestamp(point['timestamp_ms']) for point in counts]
    if not times or times != sorted(set(times)):
        raise ValueError('人数采样时间必须唯一且递增')
    coverage = report['coverage']
    start = integer(coverage['start_ms'], '实际分析起点')
    end = integer(coverage['end_ms'], '实际分析终点', 1)
    if not start < end <= duration or any(not start <= moment < end for moment in times):
        raise ValueError('采样时间超出实际分析范围')
    for point in counts:
        for key in ('visible_count', 'hand_raise_count', 'head_down_count'):
            if point.get(key) is not None:
                integer(point[key], key)
    valid_times = set(times)
    images, image_lookup, blobs = [], {}, {}
    total_bytes = 0
    def evidence(item):
        nonlocal total_bytes
        moment = timestamp(item['timestamp_ms'])
        if moment not in valid_times:
            raise ValueError('证据必须对应实际检查过的画面')
        references = {}
        for variant in ('original', 'annotated'):
            name = item.get(variant + '_file')
            if name is None:
                references[variant] = None
                continue
            if not isinstance(name, str) or not name or any(c in name for c in ('/', '\\', ':')) or name in ('.', '..'):
                raise ValueError('证据文件名不能包含目录或外部路径')
            key = (moment, variant, name)
            if key not in image_lookup:
                identifier = f'image-{len(images) + 1:06d}'
                entry = {'image_id': identifier, 'timestamp_ms': moment, 'variant': variant,
                         'availability': 'not_included', 'path': None, 'sha256': None, 'byte_size': None}
                if include_images:
                    source = report_path.parent / 'evidence' / name
                    from workers.common.deletion import plain_path
                    plain_path(source.parent)
                    plain_path(source)
                    root = (report_path.parent / 'evidence').resolve()
                    if source.is_symlink() or not source.resolve().is_relative_to(root):
                        raise ValueError('证据路径超出报告证据目录')
                    size = source.stat().st_size
                    if size > 10 * 1024**2 or total_bytes + size > max_image_bytes:
                        raise ValueError('证据大小超过导出上限')
                    with source.open('rb') as file:
                        data = file.read(10 * 1024**2 + 1)
                    if len(data) != size or len(data) > 10 * 1024**2:
                        raise ValueError('证据大小变化或超出单张上限')
                    if not data.startswith(b'\xff\xd8') or not data.endswith(b'\xff\xd9'):
                        raise ValueError('需要现有报告生成的完整 JPEG 证据')
                    digest = hashlib.sha256(data).hexdigest()
                    if digest not in blobs:
                        blobs[digest] = data
                        total_bytes += len(data)
                    entry.update(availability='included', path=f'evidence/{digest}.jpg',
                                 sha256=digest, byte_size=len(data))
                images.append(entry)
                image_lookup[key] = identifier
            references[variant] = image_lookup[key]
        return references
    persons, seen_ids, total_observations = [], set(), 0
    for person in report['persons']:
        track_id = person['track_id']
        if not isinstance(track_id, str) or not track_id or track_id in seen_ids:
            raise ValueError('人物匿名编号必须非空且唯一')
        seen_ids.add(track_id)
        observations = []
        previous = -1
        summary = {kind: {'positive': 0, 'negative': 0, 'unknown': 0} for kind in KINDS}
        for original in person['observations']:
            moment = timestamp(original['timestamp_ms'])
            if moment not in valid_times or moment <= previous:
                raise ValueError('人物观察必须对应实际采样时间且不能重复')
            previous = moment
            bbox = original['bbox']
            if len(bbox) != 4 or not all(number(x) for x in bbox) or not (bbox[0] < bbox[2] and bbox[1] < bbox[3]):
                raise ValueError('人物框坐标无效')
            behavior = original['behaviors']
            if set(behavior) != set(KINDS) or any(v is not None and type(v) is not bool for v in behavior.values()):
                raise ValueError('四类行为必须保留 true/false/null')
            for kind, value in behavior.items():
                summary[kind]['unknown' if value is None else 'positive' if value else 'negative'] += 1
            observation = copy.deepcopy(original)
            observation.pop('original_file', None)
            observation.pop('annotated_file', None)
            observation['evidence'] = evidence(original)
            observations.append(observation)
        if not observations:
            raise ValueError('人物必须有实际观察记录')
        total_observations += len(observations)
        persons.append({'track_id': track_id, 'first_ms': observations[0]['timestamp_ms'],
                        'last_ms': observations[-1]['timestamp_ms'], 'sample_count': len(observations),
                        'observation_counts': summary, 'observations': observations})
    events = []
    for original in report['events']:
        event = copy.deepcopy(original)
        start = timestamp(event['start_ms'])
        end = integer(event['end_ms'], '动作结束时间', 1)
        if not start < end <= duration or event['track_id'] not in seen_ids:
            raise ValueError('事件时间或人物关联无效')
        event['evidence'] = [dict(timestamp_ms=item['timestamp_ms'], **evidence(item))
                             for item in original.get('evidence', [])]
        events.append(event)
    provenance = report['provenance']
    result = {
        'schema_version': 'visual-handoff-v1', 'is_team_contract_result': False,
        'source_report_sha256': hashlib.sha256(raw).hexdigest(), 'binding': binding,
        'source': {'sha256': metadata['sha256'], 'duration_ms': duration, 'width': width, 'height': height},
        'time_origin': 'source_recording_start_ms', 'bbox_coordinates': 'source_image_pixels',
        'configuration': report['configuration'], 'coverage': report['coverage'],
        'execution': {key: provenance.get(key) for key in ('processor_version', 'rules_version', 'execution_engine')},
        'weights': {key: value['sha256'] for key, value in provenance['weights'].items()},
        'counts': counts, 'persons': persons, 'events': events, 'images': images,
        'summary': {'sample_count': len(counts), 'anonymous_track_count': len(persons),
                    'person_observations': total_observations, 'sustained_event_count': len(events),
                    'image_file_count': len(blobs), 'image_bytes': total_bytes},
        'acceptance_passed': False,
        'limitations': list(report['limitations']) + [
            '本包用于联合接入，不是正式 StageResult；现有 Go 完成接口不接受非空行为结果。',
            '编号只属于此段录像，不是学生身份；记录张数不等于举手次数，低头不等于走神。',
            '机器原值和本地复核记录分别保留；本地复核不能替代团队登录授权的正式复核。',
            '缺失证据保持为空；not_included 表示导出时未包含图片，不能当作可用画面证据。',
            '本包不是独立人工质量验收，也不生成学生综合分数。'],
    }
    # Validate JSON numerics before creating output; never emit NaN/Infinity.
    payload = json.dumps(result, ensure_ascii=False, indent=2, allow_nan=False).encode('utf-8')
    output.mkdir(parents=True, exist_ok=False)
    if blobs:
        (output / 'evidence').mkdir()
        for digest, data in blobs.items():
            (output / 'evidence' / (digest + '.jpg')).write_bytes(data)
    (output / 'visual-handoff.json').write_bytes(payload)
    return result['summary']
