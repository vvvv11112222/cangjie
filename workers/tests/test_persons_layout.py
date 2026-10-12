import numpy as np
from workers.vision_core.behavior import Track,Tracker,raised_hand,head_down_from_angles,door_crossing
from workers.vision_core.layout import SeatMap
from workers.vision_core.config import AnalysisConfig,Rect
from workers.vision_core.reporting import csv_persons


def test_raised_forearm_beside_ear_is_not_chin_support():
    # Decoded geometry of the user's T0004 at 29 seconds; the former .6*h
    # face-distance rejection incorrectly removed this upright forearm.
    p=np.zeros((17,2));s=np.zeros(17)
    p[0]=[376.395,217.194];p[6]=[343.215,233.310]
    p[8]=[360.595,256.694];p[10]=[356.487,224.462]
    s[[0,6,8,10]]=[.817,.746,.800,.822]
    head=[350.022,188.622,383.808,225.959]
    assert raised_hand(p,s,head)[0] is True
    small=p.copy()/2
    assert raised_hand(small,s,[v/2 for v in head])[0] is False
    p[10]=[376,224] # wrist below and directly in front of face: chin/face contact
    assert raised_hand(p,s,head)[0] is False
    s[10]=.1
    assert raised_hand(p,s,head)[0] is None


def test_eye_touch_with_supported_elbow_stays_negative_even_at_high_resolution():
    # Representative face-touch geometry with the elbow below the shoulder.
    # Scaling a face contact must not activate the new beside-ear exception.
    p=np.zeros((17,2));s=np.zeros(17)
    p[0]=[438.356,175.887];p[6]=[411.241,185.705]
    p[8]=[418.254,203.236];p[10]=[423,181]
    s[[0,6,8,10]]=[.892,.789,.772,.785]
    head=[419.507,156.155,442.246,183.774]
    for scale in (1,2,4):
        assert raised_hand(p*scale,s,[v*scale for v in head])[0] is False


def test_face_contact_is_not_a_raise_but_upright_forearm_is():
    p=np.zeros((17,2));s=np.zeros(17)
    p[0]=[50,30];p[5]=[45,50];p[7]=[45,70];p[9]=[50,35]
    s[[0,5,7,9]]=1
    assert raised_hand(p,s,[40,20,60,40])[0] is False
    p[9]=[35,45]  # beside face; forearm raised, wrist near shoulder
    assert raised_hand(p,s,[40,20,60,40])[0] is True


def test_absolute_head_down_needs_no_neutral_history_and_rejects_profile():
    assert head_down_from_angles({'pitch':-30,'yaw':10,'roll':5}) is True
    assert head_down_from_angles({'pitch':30,'yaw':10,'roll':5}) is False
    assert head_down_from_angles({'pitch':-30,'yaw':85,'roll':5}) is None
    assert head_down_from_angles({'pitch':170,'yaw':10,'roll':170}) is None


def test_seat_calibration_and_lateral_departure_not_head_bowing():
    seats=SeatMap([],200,200)
    t=Track('T1',[40,40,60,60],0,0)
    for ms in (0,500,1000,1500):seats.observe(t,t.box,(50,65),ms)
    assert t.seat==0 and len(seats.seats)==1
    assert seats.departure(t,[40,58,60,78],(50,80),standing=False) is False
    assert seats.departure(t,[85,40,105,60],(95,65),standing=True) is True
    assert seats.departure(t,[85,40,105,60],(95,65)) is None


def test_motion_matching_recovers_partial_overlap_and_gates_far_jump():
    tracker=Tracker(3000,appearance_matching=True)
    old=tracker.update([{'bbox':[10,10,30,30]}],0)[0].id
    assert tracker.update([{'bbox':[25,10,45,30]}],500)[0].id==old
    assert tracker.update([{'bbox':[150,10,170,30]}],1000)[0].id!=old


def test_unknown_schedule_does_not_label_late_entry():
    config=AnalysisConfig(door_roi=Rect(x1=.2,y1=.1,x2=.8,y2=.9),grace_s=0)
    t=Track('T1',[0,0,1,1],0,0,hits=3)
    door_crossing(t,(30,50),2000,config,100,100)
    assert door_crossing(t,(70,50),2500,config,100,100)=='entry'


def test_person_export_preserves_unknown_instead_of_zero():
    report={'persons':[{'track_id':'T1','observations':[{'timestamp_ms':500,'frame_index':10,'seat_id':None,'behaviors':dict(hand_raise=True,head_down=None,leave_seat=False,possible_phone=None),'head_angles':None,'pose_available':True}]}]}
    output=csv_persons(report)
    assert 'T1,500,10,,1,unknown,0,unknown' in output
