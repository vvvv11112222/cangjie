import numpy as np
from workers.vision_core.behavior import EventAccumulator, Tracker, Track, raised_hand, door_crossing, head_signal
from workers.vision_core.config import AnalysisConfig, Rect


def test_temporal_events_need_observed_duration_and_break_on_unknown():
    a = EventAccumulator(500, {"hand_raise": 1000})
    for t in [0, 500, 1000]:
        a.update('hand_raise','T1',t,True,.9,{'timestamp_ms':t})
    a.update('hand_raise','T1',1500,None,None,{})
    assert len(a.events)==1 and a.events[0]['end_ms']==1500
    a.update('hand_raise','T1',2000,True,.8,{})
    a.finish_all(2100)
    assert len(a.events)==1  # isolated frame does not establish persistence


def test_gap_does_not_bridge_unknown_period():
    a=EventAccumulator(500,{'leave_seat':2000})
    for t in [0,500,1000,5000,5500,6000]:
        a.update('leave_seat','T1',t,True,.9,{})
    a.finish_all(6500)
    assert not a.events


def test_tracker_short_gap_and_expiration():
    tracker=Tracker(1200)
    d=[{'bbox':[10,10,100,200]}]
    first=tracker.update(d,0)[0].id
    assert tracker.update([{'bbox':[12,10,102,200]}],500)[0].id==first
    assert tracker.update(d,2500)[0].id!=first


def test_raise_hand_and_weak_keypoints():
    p=np.zeros((17,2));s=np.ones(17)
    p[5]=[50,100];p[7]=[50,70];p[9]=[50,30]
    p[6]=[100,100];p[8]=[100,130];p[10]=[100,150]
    assert raised_hand(p,s)[0] is True
    s[:]=.1
    assert raised_hand(p,s)==(None,None)


def test_direction_and_schedule_gate():
    config=AnalysisConfig(door_roi=Rect(x1=.2,y1=.1,x2=.8,y2=.9),grace_s=1,class_end_s=100,schedule_enabled=True)
    t=Track('T1',[0,0,1,1],0,0,hits=3)
    assert door_crossing(t,(30,50),2000,config,100,100) is None
    assert door_crossing(t,(70,50),2500,config,100,100)=='late_entry'
    assert door_crossing(t,(30,50),3000,config,100,100) is None  # cooldown
    door_crossing(t,(70,50),6000,config,100,100)
    assert door_crossing(t,(30,50),6500,config,100,100)=='early_exit'
    assert door_crossing(t,(90,50),7000,config,100,100) is None  # outside entry ROI
    assert t.door_side is None


def test_head_requires_reference_and_large_face():
    t=Track('T1',[0,0,100,200],0,0)
    p=np.zeros((17,2));p[3]=[40,30];p[4]=[60,30];p[0]=[50,30];s=np.ones(17)
    assert head_signal(t,p,s,4000)==(None,None)
    for ms in [0,1000,2000]: assert head_signal(t,p,s,ms)==(None,None)
    p[0,1]=40
    assert head_signal(t,p,s,4500)[0] is True
    s[0]=.1
    assert head_signal(t,p,s,5000)==(None,None)
