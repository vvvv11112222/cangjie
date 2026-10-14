from types import SimpleNamespace
import numpy as np
import pytest
from workers.vision_core.models import VisionModels


class Runtime:
    def __init__(self,fixed=False,fail=False):
        self.fixed,self.fail=fixed,fail
        self.calls=[]

    def get_inputs(self):return [SimpleNamespace(name='input',shape=[1 if self.fixed else 'batch',3,384,288])]

    def run(self,outputs,inputs):
        x=inputs['input'];self.calls.append(len(x))
        if self.fail and len(x)>1:raise RuntimeError('simulated GPU memory pressure')
        return x,x


def model(runtime):
    m=VisionModels.__new__(VisionModels)
    m.classroom_pose=m.pose=runtime;m.pose_batch_size=4;m.provenance={}
    m._pose_input=lambda image,box,session:(np.array([box[0]],np.float32),box[0])
    m._pose_output=lambda x,y,geometry:(int(x[0]),geometry)
    m.estimate_pose=lambda image,box,classroom=False:(box[0],box[0])
    return m


@pytest.mark.parametrize('fixed,fail',[(False,False),(True,False),(False,True)])
def test_batch_order_tail_and_fallback_do_not_drop_people(fixed,fail):
    runtime=Runtime(fixed,fail);m=model(runtime)
    result=m.estimate_poses(None,[[i] for i in range(11)],True)
    assert result==[(i,i) for i in range(11)]
    if fail:assert m.pose_batch_size==1 and 'batch_fallback' in m.provenance
    if not fixed and not fail:assert runtime.calls==[4,4,3]
    assert m.estimate_poses(None,[],True)==[]


def test_batch_cancellation_does_not_start_next_group():
    runtime=Runtime();m=model(runtime);checks=[]
    def cancel():
        checks.append(True)
        if len(checks)==3:raise InterruptedError('cancelled')
    with pytest.raises(InterruptedError):m.estimate_poses(None,[[i] for i in range(11)],True,cancel)
    assert runtime.calls==[4]


def test_exact_crops_reused_only_within_the_same_image():
    runtime=Runtime();m=model(runtime);image=np.zeros((2,2,3),np.uint8)
    assert m.estimate_poses(image,[[1],[1],[2]],True)==[(1,1),(1,1),(2,2)]
    assert runtime.calls==[2]
    assert m.estimate_poses(image,[[2],[1]],True)==[(2,2),(1,1)]
    assert runtime.calls==[2]
    m.estimate_poses(image.copy(),[[1]],True)
    assert runtime.calls==[2,1]


def test_fixed_bucket_padding_never_creates_extra_people(monkeypatch):
    import workers.vision_core.models as module
    runtime=Runtime();m=model(runtime);m.fixed_pose_batches=True;m._pose_sessions={}
    compiled=Runtime()
    monkeypatch.setattr(module,'session',lambda *args,**kwargs:compiled)
    result=m.estimate_poses(None,[[i] for i in range(11)],True)
    assert result==[(i,i) for i in range(11)]
    assert compiled.calls==[8,8,8]
    assert len(m._pose_sessions)==1


def test_float64_inplace_normalization_preserves_original_tensor_values():
    import cv2
    from workers.vision_core.models import POSE_MEAN,POSE_STD,POSE_LUT,HEAD_LUT
    pixels=np.arange(256,dtype=np.uint8).repeat(3).reshape(256,1,3)
    old=(pixels.astype(np.float32)-POSE_MEAN)/POSE_STD
    new=pixels.astype(np.float64);new-=POSE_MEAN;new/=POSE_STD
    assert np.array_equal(old.astype(np.float32),new.astype(np.float32))
    assert np.array_equal(old.astype(np.float32),cv2.LUT(pixels,POSE_LUT))
    old_head=(pixels.astype(np.float32)/255-[.485,.456,.406])/[.229,.224,.225]
    assert np.array_equal(old_head.astype(np.float32),cv2.LUT(pixels,HEAD_LUT))


def test_reused_tensor_buffer_preserves_every_input_value():
    runtime=Runtime();image=np.random.default_rng(7).integers(0,256,(100,140,3),dtype=np.uint8)
    box=[10.12,-20.4,65.8,88.1]
    old,geometry=VisionModels._pose_input(image,box,runtime)
    target=np.empty_like(old)
    new,new_geometry=VisionModels._pose_input(image,box,runtime,target)
    assert new is target and np.array_equal(old,new) and geometry==new_geometry
