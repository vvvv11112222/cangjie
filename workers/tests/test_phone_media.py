from dataclasses import replace
import json

import av
import pytest

from workers.tools.check_real_go import prepare
from workers.tests.fake_go import make_claim
from workers.tests.test_worker import execute
from workers.vision_core.media import inspect_media


@pytest.mark.parametrize('codec,portrait', [('opus', False), ('aac', True)])
def test_phone_style_audio_and_portrait_media_preserve_proxy_and_samples(settings, tmp_path, codec, portrait):
    meta = prepare(tmp_path / 'generated', False, codec, portrait)
    source = tmp_path / 'generated/source.mp4'
    assert meta['has_audio'] is True
    assert (meta['width'], meta['height']) == ((240, 320) if portrait else (320, 240))
    with av.open(str(source)) as container:
        assert container.streams.audio[0].codec_context.name == codec
    outcome, fake = execute(source, settings, make_claim(settings, source, 'probe'))
    assert outcome == 'succeeded' and not fake.failures
    result = json.loads(fake.complete_bytes[0])
    assert result['has_audio'] is True and result['origin_offset_ms'] == 0
    assert result['duration_ms'] == 4000
    proxy = tmp_path / 'proxy.mp4'
    proxy.write_bytes(next(iter(fake.assets.values()))['bytes'])
    with av.open(str(proxy)) as container:
        assert container.streams.video[0].codec_context.name == 'h264'
        assert container.streams.audio[0].codec_context.name == 'aac'
        assert (container.streams.video[0].width, container.streams.video[0].height) == (meta['width'], meta['height'])
    outcome, frames = execute(source, settings, make_claim(settings, source, duration=4000))
    assert outcome == 'succeeded'
    video = json.loads(frames.complete_bytes[0])
    assert [f['timestamp_ms'] for f in video['frames']] == list(range(0, 4000, 500))
    assert video['events'] == [] and video['model'] is None


def test_portrait_does_not_silently_bypass_configured_height_limit(settings, tmp_path):
    prepare(tmp_path / 'generated', False, 'aac', True)
    source = tmp_path / 'generated/source.mp4'
    limited = replace(settings, max_height=300)
    outcome, fake = execute(source, limited, make_claim(limited, source, 'probe'))
    assert outcome == 'failed' and fake.failures[0]['error_code'] == 'MEDIA_TOO_LARGE'
    assert not fake.assets
