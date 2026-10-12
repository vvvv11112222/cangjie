"""Compare actual sampled image tensors after source-time preservation was fixed."""
import argparse
from itertools import zip_longest
import json
from pathlib import Path
import threading

import numpy as np

from workers.vision_core.media import sampled_frames


def compare_inputs(before, after, duration_ms, fps=2):
    cancel = threading.Event()
    audits = [{}, {}]
    def frames(path, audit):
        for timestamp, image in sampled_frames(path, 0, duration_ms, fps, cancel, audit):
            yield timestamp, image, audit["decoded_frames"] - 1
    count = missing = changed_images = changed_indices = 0
    time_shifts = set()
    for a, b in zip_longest(frames(before, audits[0]), frames(after, audits[1])):
        count += 1
        if a is None or b is None:
            missing += 1
            continue
        changed_images += int(not np.array_equal(a[1], b[1]))
        changed_indices += int(a[2] != b[2])
        time_shifts.add(b[0] - a[0])
    return {"sample_pairs": count, "missing": missing, "changed_images": changed_images,
            "changed_frame_indices": changed_indices, "timestamp_shifts_ms": sorted(time_shifts),
            "same_inference_inputs": missing == changed_images == changed_indices == 0}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("before", type=Path)
    parser.add_argument("after", type=Path)
    parser.add_argument("--duration-ms", type=int, required=True)
    args = parser.parse_args()
    result = compare_inputs(args.before, args.after, args.duration_ms)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if not result["same_inference_inputs"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
