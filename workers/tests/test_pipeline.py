from threading import Event
import numpy as np
import pytest
from workers.vision_core.pipeline import detection_pipeline


def test_pipeline_preserves_timestamps_results_and_worker_exceptions():
    frames=[(t,np.full((8,8,3),t,np.uint8)) for t in (40,80,120)]
    rows=list(detection_pipeline(iter(frames),lambda image:[int(image[0,0,0])],lambda:None))
    assert [(t,d) for t,_,d,_,_ in rows]==[(40,[40]),(80,[80]),(120,[120])]
    def fail(image):raise ValueError('detector failed')
    with pytest.raises(ValueError,match='detector failed'):
        list(detection_pipeline(iter(frames),fail,lambda:None))


def test_pipeline_lookahead_does_not_keep_running_after_close():
    calls=[]
    frames=((t,np.ones((8,8,3),np.uint8)*50) for t in range(100))
    pipeline=detection_pipeline(frames,lambda image:calls.append(1) or [],lambda:None)
    next(pipeline);pipeline.close()
    assert 1<=len(calls)<=2


def test_frame_indices_are_captured_before_lookahead_updates_audit():
    audit={}
    def frames():
        for index in (0,13,26):
            audit['index']=index
            yield index*40,np.ones((8,8,3),np.uint8)*50
    rows=list(detection_pipeline(frames(),lambda image:[],lambda:None,lambda:audit['index']))
    assert [(r[0],r[4]) for r in rows]==[(0,0),(520,13),(1040,26)]
