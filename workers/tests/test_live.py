from dataclasses import replace
from unittest.mock import MagicMock
import json
import httpx
from workers.common.live import LiveProcessor
from workers.common.runner import Runner
from workers.common.transport import Transport
from .fake_go import make_claim


def test_live_child_timeout_returns_failure_and_releases_gpu(settings,monkeypatch):
    work=dict(request_id='a'*32,lease_token='b'*48,capture_id='synthetic',sequence=7,image_base64='synthetic',width=640,height=360)
    completed=[]
    def handle(request):
        if request.url.path.endswith('/claim'):return httpx.Response(200,json=dict(data=work))
        completed.append(json.loads(request.content))
        assert request.headers['X-Lease-Token']==work['lease_token']
        return httpx.Response(204)
    transport=Transport(settings,httpx.Client(transport=httpx.MockTransport(handle)))
    pipe,child,process=MagicMock(),MagicMock(),MagicMock()
    pipe.poll.return_value=False
    process.is_alive.return_value=True
    process.terminate.side_effect=lambda:setattr(process.is_alive,'return_value',False)
    context=MagicMock();context.Pipe.return_value=(pipe,child);context.Process.return_value=process
    monkeypatch.setattr('workers.common.live.mp.get_context',lambda _:context)
    live=LiveProcessor(transport)
    assert live.poll()
    assert completed==[dict(sequence=7,width=640,height=360,people=[],error_code='PROCESSING_TIMEOUT')]
    process.terminate.assert_called_once();process.join.assert_called_once();process.close.assert_called_once()
    assert live.process is None and live.pipe is None
    transport.close()


def test_offline_claim_releases_live_child_before_running(settings,source,monkeypatch):
    events=[]
    class Preview:
        def __init__(self,_):pass
        def close(self):events.append('close-live')
        def poll(self):raise AssertionError('offline work must have priority')
    monkeypatch.setenv('CAPTURE_LIVE_VISION_ENABLED','true')
    monkeypatch.setattr('workers.common.live.LiveProcessor',Preview)
    configured=replace(settings,vision_enabled=True,cleanup_enabled=False)
    runner=Runner(configured)
    monkeypatch.setattr(runner.transport,'claim',lambda:make_claim(configured,source))
    def run(_):
        assert events==['close-live']
        events.append('offline')
        return 'succeeded'
    monkeypatch.setattr(runner,'run_claim',run)
    runner._run(once=True)
    assert events==['close-live','offline','close-live']
