"""Consume ephemeral Go-owned frames only while the existing Worker is idle."""
import multiprocessing as mp
import time
import httpx


def _serve(pipe):
    from workers.vision_core.live import LiveDetector
    detector=LiveDetector()
    while True:
        work=pipe.recv()
        if work is None: return
        try: pipe.send(detector.infer(work))
        except Exception: pipe.send(None)


class LiveProcessor:
    def __init__(self,transport):
        self.transport=transport
        self.process=None
        self.pipe=None
        self.last_work=0

    def close(self):
        if self.process is not None:
            if self.process.is_alive(): self.process.terminate()
            self.process.join(timeout=5)
            if self.process.is_alive(): self.process.kill();self.process.join(timeout=5)
            self.process.close()
            self.process=None
        if self.pipe is not None: self.pipe.close();self.pipe=None

    def poll(self):
        t=self.transport
        try:
            response=t.client.post(t.settings.api_base+'/live-vision/claim',headers=t.headers(),json={})
            if response.status_code==204:
                if self.process is not None and time.monotonic()-self.last_work>15: self.close()
                return False
            t.check_response(response,(200,))
            work=response.json()['data']
            self.last_work=time.monotonic()
            if self.process is None or not self.process.is_alive():
                self.close()
                context=mp.get_context('spawn')
                self.pipe,child=context.Pipe()
                self.process=context.Process(target=_serve,args=(child,),daemon=True)
                self.process.start();child.close()
            self.pipe.send(work)
            result=None
            answered=self.pipe.poll(32)
            if answered:
                try: result=self.pipe.recv()
                except EOFError: pass
            if result is None:
                self.close()
                result=dict(sequence=work['sequence'],width=work['width'],height=work['height'],people=[],
                            error_code='PROCESSING_FAILED' if answered else 'PROCESSING_TIMEOUT')
            headers={**t.headers(),'X-Lease-Token':work['lease_token']}
            response=t.client.post(t.settings.api_base+'/live-vision/'+work['request_id']+'/complete',headers=headers,json=result)
            # An expired viewer frame is discarded, never retried or persisted.
            if response.status_code not in (204,409):t.check_response(response,(204,))
            return True
        except (httpx.TransportError,EOFError,BrokenPipeError,OSError):
            self.close()
            return False
