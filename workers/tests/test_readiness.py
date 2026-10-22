from dataclasses import replace
import json

import httpx

from workers.common.transport import Transport


def test_missing_models_do_not_announce_visual_readiness(settings, tmp_path, monkeypatch):
    monkeypatch.setenv('VISION_MODEL_ROOT', str(tmp_path / 'missing'))
    bodies = []
    def handle(request):
        bodies.append(json.loads(request.content))
        return httpx.Response(204)
    transport = Transport(replace(settings, vision_enabled=True), httpx.Client(transport=httpx.MockTransport(handle)))
    assert transport.claim() is None
    assert bodies[0]['capabilities'] == ['probe', 'video_analysis']
    assert 'visual_enabled' not in bodies[0]
    transport.close()


def test_verified_visual_models_are_checked_once_per_polling_process(settings, monkeypatch):
    from workers.video import visual
    checks = []
    monkeypatch.setattr(visual, 'verify_local_models', lambda: checks.append(True))
    bodies = []
    def handle(request):
        bodies.append(json.loads(request.content))
        return httpx.Response(204)
    transport = Transport(replace(settings, vision_enabled=True), httpx.Client(transport=httpx.MockTransport(handle)))
    transport.claim()
    transport.claim()
    assert checks == [True]
    assert all(body['visual_enabled'] for body in bodies)
    transport.close()
