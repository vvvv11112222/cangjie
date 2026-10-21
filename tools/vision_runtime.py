"""Local Windows execution selection; no model, permission or sampling changes.

The probe runs in a separate process so provider import and compilation cannot
pollute the API/launcher process. A successful probe is not a speed acceptance.
"""
import argparse
import json
import os
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]


def discrete_adapter(adapters):
    # Dedicated memory is a conservative deployment heuristic, not a claim about
    # every vendor's architecture. Do not automatically select a small UMA GPU.
    candidates = [a for a in adapters if not a['software'] and a['dedicated_mb'] >= 1024]
    if not candidates:
        raise RuntimeError('没有发现具有至少 1 GiB 专用显存的独立显卡；使用 CPU')
    return max(candidates, key=lambda a: ('nvidia' in a['name'].casefold(), a['dedicated_mb']))


def execution_environment(selection, root=ROOT):
    gpu = selection['engine'] == 'directml'
    values = dict(VISION_RUNTIME=selection['engine'],
                  VISION_RUNTIME_CONFIG=str(root / 'workers/config' / ('runtime.directml.json' if gpu else 'runtime.cpu.json')),
                  VISION_CPU_THREADS='4', VISION_CV_THREADS='4', VISION_BATCH_SIZE=str(selection['batch_size']),
                  VISION_FIXED_BATCH='1' if gpu else '0', VISION_GPU_THREADS='1',
                  VISION_GPU_DETECTION='0', VISION_GPU_YOLOX='1' if gpu else '0',
                  VISION_PREFETCH='1', VISION_DECODED_POSES='0', VISION_TF32='0',
                  VISION_REQUIRE_GPU='1' if gpu else '0')
    if selection.get('accelerator_root'):
        values['VISION_ACCELERATOR_ROOT'] = selection['accelerator_root']
    if gpu:
        values['VISION_DML_DEVICE'] = str(selection['device']['device_id'])
    return values


def probe(accelerator_root, models, requested_batch):
    sys.path.insert(0, str(ROOT))
    from workers.vision_core.devices import dxgi_adapters
    device = discrete_adapter(dxgi_adapters())
    selection = {'engine': 'directml', 'device': device,
                 'batch_size': min(requested_batch, 16 if device['dedicated_mb'] >= 6144 else 8),
                 'accelerator_root': str(accelerator_root) if accelerator_root else None}
    for name in list(os.environ):
        if name.startswith('VISION_'):
            del os.environ[name]
    os.environ.update(execution_environment(selection), VISION_MODEL_ROOT=str(models))
    from workers.vision_core import models as adapters
    import numpy as np
    if 'DmlExecutionProvider' not in adapters.ort.get_available_providers():
        raise RuntimeError('没有安装 ONNX Runtime DirectML 加速包；请按部署说明准备加速环境')
    # Existing pinned pose export, float32, same batching path used by the Worker.
    batch = selection['batch_size']
    session = adapters.session(models / 'rtmpose_x.onnx', True, batch)
    if session.get_providers()[0] != 'DmlExecutionProvider':
        raise RuntimeError('姿态模型实际回退到了 CPU；独显检查未通过')
    shape = session.get_inputs()[0].shape
    outputs = session.run(None, {session.get_inputs()[0].name: np.zeros((batch, *shape[1:]), np.float32)})
    if not outputs or not all(np.isfinite(o).all() for o in outputs):
        raise RuntimeError('独显推理返回了无效数据')
    records = adapters.runtime_status()['sessions']
    if not records or records[-1]['device']['device_id'] != device['device_id']:
        raise RuntimeError('无法确认模型使用了所选独显')
    return {**selection, 'provider': 'DmlExecutionProvider', 'probe_passed': True,
            'onnxruntime_version': adapters.ort.__version__, 'reason': None}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--accelerator-root', type=Path)
    parser.add_argument('--models', type=Path, required=True)
    parser.add_argument('--batch-size', type=int, choices=(1, 8, 16), default=16)
    args = parser.parse_args()
    try:
        result = probe(args.accelerator_root, args.models, args.batch_size)
    except Exception as error:
        # Provider construction can print diagnostics before this last JSON line.
        print(json.dumps({'error': str(error)[:500]}, ensure_ascii=False), flush=True)
        return 1
    print(json.dumps(result, ensure_ascii=False), flush=True)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
