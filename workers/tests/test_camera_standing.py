import numpy as np
from workers.vision_core.behavior import Track,standing_pose
from workers.vision_core.layout import SeatMap
from workers.vision_core.camera import transform_box,CameraMotion


def test_pan_moves_seat_and_head_together_without_departure():
    seats=SeatMap([],300,200);track=Track('T1',[40,40,60,60],0,0)
    for ms in [0,500,1000,1500]:seats.observe(track,track.box,(50,50),ms)
    pan=np.array([[1,0,30],[0,1,4]],dtype=float)
    seats.compensate_camera(pan);track.box=transform_box(track.box,pan)
    assert seats.departure(track,track.box,(80,54),standing=True) is False


def test_standing_requires_visible_straight_legs_not_only_raised_head():
    p=np.zeros((17,2));s=np.zeros(17)
    assert standing_pose(p,s) is None
    p[[11,13,15]]=[[20,40],[20,70],[20,100]];s[[11,13,15]]=1
    assert standing_pose(p,s) is True
    p[13]=[45,55]
    assert standing_pose(p,s) is False


def test_scene_change_is_not_a_stable_camera_pan():
    import cv2
    image=np.random.default_rng(1).integers(0,70,(360,640,3),dtype=np.uint8)
    camera=CameraMotion();assert camera.update(image)==(None,False)
    matrix,cut=camera.update(np.full_like(image,220))
    # A featureless next image cannot be aligned; this is not a track continuation.
    assert matrix is None and cut is True
