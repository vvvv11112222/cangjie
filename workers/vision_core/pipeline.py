"""One bounded lookahead. Each detector session has a single owning worker."""
from concurrent.futures import ThreadPoolExecutor
import time
import cv2


def detection_pipeline(frames, detect, cancel, frame_index=None):
    iterator=iter(frames)
    def next_frame():
        cv2.ocl.setUseOpenCL(False)
        cancel()
        try:timestamp,image=next(iterator)
        except StopIteration:return None
        gray=cv2.cvtColor(image,cv2.COLOR_BGR2GRAY)
        valid=not (float(gray.mean())<6 and float(gray.std())<4)
        started=time.perf_counter()
        detections=detect(image) if valid else []
        return timestamp,image,detections,time.perf_counter()-started,frame_index() if frame_index else None
    with ThreadPoolExecutor(max_workers=1,thread_name_prefix='detection') as executor:
        future=executor.submit(next_frame)
        try:
            while True:
                item=future.result()
                if item is None:break
                cancel()
                future=executor.submit(next_frame)
                yield item
        finally:
            future.cancel()
    # Executor shutdown waits for the sole worker before a later job can use it.
