from dataclasses import replace
from pathlib import Path

import av
import numpy as np
import pytest

from workers import PROCESSOR_VERSION
from workers.common.config import Settings
from workers.vision_core.media import digest_file, ffmpeg_executable


def pytest_configure(config):
    if config.option.basetemp:
        Path(config.option.basetemp).resolve().parent.mkdir(parents=True, exist_ok=True)


@pytest.fixture
def source(tmp_path):
    file = tmp_path / "synthetic.mp4"
    with av.open(str(file), "w") as container:
        stream = container.add_stream("libx264", rate=10)
        stream.width, stream.height, stream.pix_fmt = 160, 120, "yuv420p"
        for index in range(20):
            image = np.full((120, 160, 3), 20 + index * 8, np.uint8)
            for packet in stream.encode(av.VideoFrame.from_ndarray(image, format="rgb24")):
                container.mux(packet)
        for packet in stream.encode():
            container.mux(packet)
    return file


@pytest.fixture
def settings(tmp_path):
    return Settings("http://127.0.0.1:8080/internal/v1", "test-worker", "test-token", tmp_path / "scratch",
                    PROCESSOR_VERSION, digest_file(ffmpeg_executable()), heartbeat_seconds=0.2)
