from dataclasses import replace

import pytest

from workers.common.contracts import WorkerError, validate_result, parameter_digest, json_bytes
from workers.common.lifecycle import validate_claim, Guard
from workers.video.visual import model_identity, package_report, visual_analysis
from .fake_go import make_claim


def visual_claim(settings, source):
    claim=make_claim(settings,source)
    claim['parameters'].update(config_profile='vision-v1',keyframe_interval_ms=500,
        visual={'model':model_identity(),'max_samples':15000,'max_pose_people':80,'head_down_enabled':True})
    claim['execution']['parameters_sha256']=parameter_digest(claim['parameters'])
    return claim


def test_visual_disabled_or_mismatched_before_model_load(settings,source):
    claim=visual_claim(settings,source)
    with pytest.raises(WorkerError,match='视觉'):
        validate_claim(claim,settings)
    settings=replace(settings,vision_enabled=True)
    validate_claim(claim,settings)
    claim['parameters']['visual']['model']['revision']='0'*64
    claim['execution']['parameters_sha256']=parameter_digest(claim['parameters'])
    with pytest.raises(WorkerError,match='视觉'):
        validate_claim(claim,settings)


def test_adapter_preserves_every_person_and_unknown_decision(settings,source):
    claim=visual_claim(settings,source)
    # Production images carry the schema, not repository example documents.
    observation={'timestamp_ms':0,'bbox':[10,10,40,50],
                 'behaviors':{'hand_raise':True,'head_down':None,'leave_seat':False,'possible_phone':None}}
    report={'metadata':{'width':160,'height':120},'persons':[{'track_id':'T0001','observations':[observation]}],
            'counts':[{'timestamp_ms':0,'visible_count':1,'hand_raise_count':1,'head_down_count':None}],'events':[], 'coverage':{'start_ms':0,'end_ms':2000},'limitations':[]}
    result=package_report(report,claim,{})
    validate_result(result,claim)
    assert result['visual']['persons'][0]['observations'][0]['behaviors']==observation['behaviors']
    assert result['visual']['persons'][0]['observations'][0]['frame_asset_id'] is None
    assert len(result['visual']['persons'])==1
    assert result['events']==[]


def test_formal_adapter_calls_existing_core_without_transcoding(settings,source,tmp_path,monkeypatch):
    settings=replace(settings,vision_enabled=True)
    claim=visual_claim(settings,source)
    work=tmp_path/'job';work.mkdir();(work/'input.bin').write_bytes(source.read_bytes())
    calls=[]
    def analyze(job,directory,metadata,config,guard,progress):
        assert (directory/'playback.mp4').stat().st_ino==(work/'input.bin').stat().st_ino
        assert config.duration_s==0 and config.sampling_fps==2 and config.max_pose_people==80 and config.head_down_enabled
        calls.append(job)
        return {'metadata':metadata,'persons':[],'counts':[{'timestamp_ms':0,'visible_count':0,'hand_raise_count':None,'head_down_count':None}],
            'events':[],'coverage':{'start_ms':0,'end_ms':2000},'limitations':[],'snapshots':[]}
    monkeypatch.setattr('workers.vision_core.analysis.analyze',analyze)
    monkeypatch.setattr('workers.vision_core.media.make_proxy',lambda *args:pytest.fail('duplicate transcode'))
    result=visual_analysis(claim,work,settings,None,Guard(claim))
    validate_result(result,claim)
    assert calls==[claim['job_id']]
