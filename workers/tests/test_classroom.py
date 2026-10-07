import numpy as np
from workers.vision_core.behavior import raised_hand
from workers.vision_core.config import AnalysisConfig
from workers.vision_core.reporting import build_report,csv_counts


def test_small_classroom_arm_and_pose_ownership():
    p=np.zeros((17,2));s=np.ones(17)
    p[0]=[50,50];p[5]=[48,65];p[7]=[48,72];p[9]=[48,25]
    p[6]=[56,65];p[8]=[56,72];p[10]=[56,78]
    # 7px arm was excluded by the old absolute 12px rule.
    assert raised_hand(p,s)[0] is None
    assert raised_hand(p,s,[42,40,62,60])[0] is True
    p[0]=[90,50]  # neighbouring person's nose: reject
    assert raised_hand(p,s,[42,40,62,60])==(None,None)


def test_frame_raise_is_not_hidden_when_no_duration_event():
    stats={'hand_evaluable':5,'person_instances':10,'seat_evaluable':0,'head_evaluable':0,'phone_evaluable':0,'valid_frames':2}
    points=[{'timestamp_ms':0,'visible_count':5,'hand_raise_count':2,'hand_evaluable_count':3},
            {'timestamp_ms':500,'visible_count':5,'hand_raise_count':0,'hand_evaluable_count':2}]
    r=build_report('x','test',{'duration_ms':1000},AnalysisConfig(),points,[],[],stats,{'weights':{}},
                   {'start_ms':0,'end_ms':1000,'percent_of_video':100},1,[])
    cap=r['capabilities'][0]
    assert cap['status']=='candidate_found' and cap['candidate_count']==0 and cap['positive_sample_frames']==1
    assert r['statistics']['max_simultaneous_hand_raise']==2
    assert 'hand_raise_count' in csv_counts(r)


def test_low_coverage_zero_is_not_whole_class_no_raise():
    stats={'hand_evaluable':2,'person_instances':50,'seat_evaluable':0,'head_evaluable':0,'phone_evaluable':0,'valid_frames':1}
    r=build_report('x','test',{'duration_ms':1000},AnalysisConfig(),[{'timestamp_ms':0,'visible_count':50,'hand_raise_count':0}],[],[],stats,{'weights':{}},
                   {'start_ms':0,'end_ms':1000,'percent_of_video':100},1,[])
    assert r['capabilities'][0]['status']=='insufficient'


def test_classroom_defaults_cover_large_class():
    c=AnalysisConfig()
    assert c.detection_mode=='classroom' and c.max_pose_people==80 and c.hand_min_s==.5
