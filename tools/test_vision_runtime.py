"""Hardware-selection boundaries; synthetic adapters, no GPU load."""
import os
from unittest.mock import patch
import pytest
from vision_runtime import discrete_adapter, execution_environment
from workers.video.visual import require_acceleration
from workers.common.contracts import WorkerError


def gpu(index, name, memory, software=False):
    return {'device_id':index,'name':name,'dedicated_mb':memory,'software':software}


def test_discrete_selection_uses_current_identity_not_adapter_zero():
    adapters=[gpu(0,'AMD integrated',128),gpu(2,'NVIDIA discrete',8192),gpu(1,'Software',16384,True)]
    assert discrete_adapter(adapters)['device_id']==2
    assert discrete_adapter([gpu(3,'AMD discrete',4096)])['device_id']==3


@pytest.mark.parametrize('adapters',[[],[gpu(0,'AMD integrated',128)],[gpu(0,'Software',8192,True)]])
def test_no_discrete_gpu_does_not_silently_select_uma(adapters):
    with pytest.raises(RuntimeError,match='独立显卡'):
        discrete_adapter(adapters)


def test_cpu_environment_has_no_gpu_ordinal_or_dependency_path():
    env=execution_environment({'engine':'cpu','batch_size':1})
    assert env['VISION_REQUIRE_GPU']=='0'
    assert 'VISION_DML_DEVICE' not in env
    assert 'VISION_ACCELERATOR_ROOT' not in env


def test_worker_rejects_actual_cpu_fallback_and_wrong_gpu():
    with patch.dict(os.environ,{'VISION_REQUIRE_GPU':'1','VISION_DML_DEVICE':'2'}):
        for session in ([],[{'gpu_requested':True,'provider':'CPUExecutionProvider','device':None}],
                        [{'gpu_requested':True,'provider':'DmlExecutionProvider','device':{'device_id':0}}]):
            with pytest.raises(WorkerError): require_acceleration({'sessions':session})
        require_acceleration({'sessions':[{'gpu_requested':True,'provider':'DmlExecutionProvider','device':{'device_id':2}},
                                         {'gpu_requested':False,'provider':'CPUExecutionProvider','device':None}]})
