import copy
import hashlib
import json
from pathlib import Path
from uuid import uuid4

import cv2
import numpy as np
import pytest

from workers.video.handoff import export_handoff


@pytest.fixture
def report_file(tmp_path):
    directory = tmp_path / 'local'
    directory.mkdir()
    (directory / 'evidence').mkdir()
    _, image = cv2.imencode('.jpg', np.zeros((8, 8, 3), dtype=np.uint8))
    (directory / 'evidence' / 'frame.jpg').write_bytes(image.tobytes())
    observations = [dict(timestamp_ms=t, frame_index=i, bbox=[1, 2, 4, 6],
                         behaviors=dict(hand_raise=True, head_down=head, leave_seat=False, possible_phone=None),
                         behavior_scores={'hand_raise': .8}, pose_available=True,
                         original_file='frame.jpg' if i == 0 else None, annotated_file=None,
                         reviews={'hand_raise': {'value': False, 'reason': 'synthetic reviewer correction'}} if i == 1 else {})
                    for i, (t, head) in enumerate(((0, None), (500, False), (1000, True)))]
    report = dict(schema_version='vision-lab-0.4', is_team_contract_result=False,
                  metadata=dict(sha256='a'*64, duration_ms=1500, width=8, height=8),
                  configuration={'sampling_fps': 2}, coverage={'start_ms': 0, 'end_ms': 1001},
                  provenance=dict(processor_version='synthetic-v1', weights={}, execution_engine='cpu'),
                  counts=[{'timestamp_ms': t, 'visible_count': 1, 'hand_raise_count': 1, 'head_down_count': h}
                          for t,h in ((0,None),(500,0),(1000,1))],
                  persons=[dict(track_id='T0001', observations=observations)], events=[], limitations=['synthetic test only'])
    path = directory / 'report.json'
    path.write_text(json.dumps(report), encoding='utf-8')
    return path


def load_export(report_file, tmp_path, **kwargs):
    output = tmp_path / 'export'
    summary = export_handoff(report_file, output, **kwargs)
    return json.loads((output / 'visual-handoff.json').read_text(encoding='utf-8')), output, summary


def test_unknown_negative_positive_and_reviews_remain_distinct(report_file, tmp_path):
    original = report_file.read_bytes()
    result, output, summary = load_export(report_file, tmp_path)
    person = result['persons'][0]
    assert person['observation_counts']['head_down'] == {'positive': 1, 'negative': 1, 'unknown': 1}
    assert person['observation_counts']['hand_raise']['positive'] == 3
    assert summary['sustained_event_count'] == 0  # Three positive observations are not three events.
    assert [o['behaviors']['head_down'] for o in person['observations']] == [None, False, True]
    assert person['observations'][1]['behaviors']['hand_raise'] is True
    assert person['observations'][1]['reviews']['hand_raise']['value'] is False
    assert result['acceptance_passed'] is False and result['is_team_contract_result'] is False
    assert result['source_report_sha256'] == hashlib.sha256(original).hexdigest()
    assert report_file.read_bytes() == original
    image = result['images'][0]
    assert hashlib.sha256((output / image['path']).read_bytes()).hexdigest() == image['sha256']
    assert person['observations'][0]['evidence']['original'] == image['image_id']
    assert person['observations'][1]['evidence']['original'] is None


def test_binding_ids_are_explicit_not_fabricated(report_file, tmp_path):
    binding = {key: str(uuid4()) for key in ('session_id', 'run_id', 'media_asset_id')}
    result, _, _ = load_export(report_file, tmp_path, binding=binding)
    assert result['binding'] == binding
    assert not any('asset_id' in x for x in result['images'])


def test_no_images_is_explicitly_unavailable(report_file, tmp_path):
    (report_file.parent / 'evidence/frame.jpg').unlink()
    result, output, _ = load_export(report_file, tmp_path, include_images=False)
    assert result['images'][0]['availability'] == 'not_included'
    assert result['images'][0]['path'] is None and not (output / 'evidence').exists()


@pytest.mark.parametrize('mutation', ['unknown_as_zero', 'duplicate_time', 'out_of_range', 'nan_box', 'duplicate_track', 'event_outside', 'traversal', 'coverage_gap'])
def test_invalid_data_is_rejected_before_output_creation(report_file, tmp_path, mutation):
    r = json.loads(report_file.read_text())
    observation = r['persons'][0]['observations'][0]
    if mutation == 'unknown_as_zero': observation['behaviors']['head_down'] = 0
    if mutation == 'duplicate_time': r['counts'][1]['timestamp_ms'] = 0
    if mutation == 'out_of_range': observation['timestamp_ms'] = 1500
    if mutation == 'nan_box': observation['bbox'][0] = float('nan')
    if mutation == 'duplicate_track': r['persons'].append(copy.deepcopy(r['persons'][0]))
    if mutation == 'event_outside': r['events'] = [dict(track_id='T0001', start_ms=0, end_ms=2000)]
    if mutation == 'traversal': observation['original_file'] = '../outside.jpg'
    if mutation == 'coverage_gap': r['coverage']['end_ms'] = 500
    report_file.write_text(json.dumps(r), encoding='utf-8')
    output = tmp_path / 'export'
    with pytest.raises(ValueError):
        export_handoff(report_file, output)
    assert not output.exists()


def test_missing_evidence_is_not_reported_as_included(report_file, tmp_path):
    (report_file.parent / 'evidence/frame.jpg').unlink()
    with pytest.raises(FileNotFoundError):
        export_handoff(report_file, tmp_path / 'export')


def test_output_cannot_overwrite_or_pollute_source(report_file, tmp_path):
    with pytest.raises(ValueError):
        export_handoff(report_file, report_file.parent / 'export')
    output = tmp_path / 'existing'
    output.mkdir()
    (output / 'keep').write_text('keep')
    with pytest.raises(ValueError):
        export_handoff(report_file, output)
    assert (output / 'keep').read_text() == 'keep'


def test_sustained_event_preserves_times_and_links_same_evidence(report_file, tmp_path):
    report = json.loads(report_file.read_text())
    report['events'] = [dict(event_id='E00001', event_type='hand_raise', track_id='T0001',
                             start_ms=0, end_ms=1000, confidence=.8, review_status='unreviewed',
                             evidence=[dict(timestamp_ms=0, original_file='frame.jpg', annotated_file=None)])]
    report_file.write_text(json.dumps(report), encoding='utf-8')
    result, _, summary = load_export(report_file, tmp_path)
    event = result['events'][0]
    assert (event['start_ms'], event['end_ms']) == (0, 1000)
    assert summary['sustained_event_count'] == 1
    assert len(result['images']) == 1
    assert event['evidence'][0]['original'] == result['persons'][0]['observations'][0]['evidence']['original']


def test_images_budget_is_enforced(report_file, tmp_path):
    with pytest.raises(ValueError, match='大小超过'):
        export_handoff(report_file, tmp_path / 'export', max_image_bytes=10)
