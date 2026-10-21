import base64
import io
import json
from pathlib import Path
import subprocess
import wave

from workers.common.contracts import WorkerError
from workers.video.handlers import base_result
from workers.vision_core.media import ffmpeg_executable


def run_local(command, directory, guard, name):
    output = directory / (name + '.out')
    errors = directory / (name + '.err')
    with output.open('wb') as stdout, errors.open('wb') as stderr:
        process = subprocess.Popen([str(p) for p in command], stdout=stdout, stderr=stderr,
                                   stdin=subprocess.DEVNULL, creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0))
        try:
            while process.poll() is None:
                guard.wait(.1)
            guard.check()
            if process.returncode:
                raise WorkerError('PROCESSING_FAILED', '音频预处理失败；请核对运行时和媒体时间轴')
        finally:
            if process.poll() is None:
                process.kill()
            process.wait()
    return output, errors


def audio_analysis(claim, directory, settings, transport, guard):
    expected = claim['execution']['asr_model']
    if expected != {'name': settings.asr_model_name, 'revision': settings.asr_model_revision}:
        raise WorkerError('CONFIG_MISMATCH', 'ASR 模型与固定任务配置不一致')
    duration = claim['duration_ms']
    if not isinstance(duration, int) or duration <= 0:
        raise WorkerError('CONFIG_MISMATCH', '音频阶段需要原录像时长')
    audio = directory / 'audio.wav'
    # Keep the common media origin. Resampling inserts leading/gap silence rather
    # than moving delayed audio to video T0; never relabel extracted relative time.
    run_local([ffmpeg_executable(), '-nostdin', '-v', 'error', '-y', '-copyts', '-start_at_zero',
               '-i', directory / 'input.bin', '-map', '0:a:0', '-vn', '-af', 'aresample=16000:async=1:first_pts=0',
               '-ac', '1', '-ar', '16000', '-c:a', 'pcm_s16le', '-t', f'{duration/1000:.3f}', audio], directory, guard, 'extract')
    with wave.open(str(audio), 'rb') as source:
        actual = min(duration, source.getnframes() // 16)
        if actual <= 0:
            raise WorkerError('PROCESSING_FAILED', '没有可处理的音频')
        _, log = run_local([ffmpeg_executable(), '-nostdin', '-hide_banner', '-nostats', '-v', 'info',
                            '-i', audio, '-af', 'silencedetect=noise=-35dB:d=0.5', '-f', 'null', '-'], directory, guard, 'silence')
        output, _ = run_local([settings.asr_planner, 'worker-plan', log, str(actual)], directory, guard, 'plan')
        chunks = json.loads(output.read_text(encoding='utf-8'))
        cursor = 0
        segments = []
        for chunk in chunks:
            start, end = chunk['start_ms'], chunk['end_ms']
            if type(start) is not int or type(end) is not int or start != cursor or not start < end <= min(start + 40000, actual):
                raise WorkerError('INVALID_RESULT', '音频切片时间轴不连续或越界')
            source.setpos(start * 16)
            samples = source.readframes((end - start) * 16)
            if len(samples) != (end - start) * 32:
                raise WorkerError('INVALID_RESULT', '音频切片长度不符合时间范围')
            data = io.BytesIO()
            with wave.open(data, 'wb') as target:
                target.setnchannels(1); target.setsampwidth(2); target.setframerate(16000); target.writeframes(samples)
            text = transport.transcribe(claim, {'start_ms': start, 'end_ms': end,
                'wav_base64': base64.b64encode(data.getvalue()).decode('ascii')}, guard)
            if text.strip():
                segments.append({'segment_no': len(segments), 'start_ms': start, 'end_ms': end,
                                 'text_content': text, 'speaker_label': 'unknown'})
            cursor = end
            guard.update(progress=end * 95 // actual)
        if cursor != actual:
            raise WorkerError('INVALID_RESULT', '音频切片未覆盖已提取的音轨')
    limitations = ['复用语言分析模块的停顿切片；每块最长40秒，不是句子或词级对齐。', '未区分说话人，不将全部声音归为教师。']
    if actual < duration:
        limitations.append('音轨短于录像，覆盖范围以实际处理的音频为准。')
    return {**base_result(claim), 'segments': segments, 'audio_asset_id': None, 'model': expected,
            'coverage': [{'start_ms': 0, 'end_ms': actual}], 'limitations': limitations}
