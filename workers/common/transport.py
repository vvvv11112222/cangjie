"""Go is the only remote media/queue/storage access point."""
import hashlib
from pathlib import Path
import time

import httpx

from .contracts import LeaseLost, WorkerError, json_bytes, validate


class Transport:
    def __init__(self, settings, client=None):
        self.settings = settings
        self.client = client or httpx.Client(
            timeout=httpx.Timeout(30, connect=5), follow_redirects=False, trust_env=False)

    def close(self):
        self.client.close()

    def headers(self, claim=None):
        headers = {"Authorization": "Bearer " + self.settings.token}
        if claim is not None:
            headers["X-Lease-Token"] = claim["lease_token"]
        return headers

    def job_url(self, claim, suffix):
        return f"{self.settings.api_base}/jobs/{claim['job_id']}/{suffix}"

    @staticmethod
    def check_response(response, statuses):
        if response.status_code in (401, 403, 409, 410):
            raise LeaseLost(f"Go 已拒绝任务访问（HTTP {response.status_code}）")
        if response.status_code not in statuses:
            raise WorkerError("WORKER_API_ERROR", f"Go 请求失败（HTTP {response.status_code}）",
                              response.status_code >= 500)

    def request(self, method, url, *, guard=None, **kwargs):
        # Only idempotent job operations call this retry wrapper. claim is never retried here.
        for attempt in range(3):
            if guard:
                guard.check()
            try:
                response = self.client.request(method, url, follow_redirects=False, **kwargs)
                if response.status_code < 500 or attempt == 2:
                    return response
            except httpx.TransportError as error:
                if attempt == 2:
                    raise WorkerError("WORKER_API_ERROR", "无法连接 Go 内部接口", True) from error
            if guard:
                guard.wait(0.25 * (attempt + 1))
            else:
                time.sleep(0.25 * (attempt + 1))

    def claim(self):
        response = self.client.post(self.settings.api_base + "/jobs/claim",
                                    headers=self.headers(), json={
            "worker_id": self.settings.worker_id, "capabilities": list(self.settings.capabilities)})
        self.check_response(response, (200, 204))
        if response.status_code == 204:
            return None
        claim = response.json()["data"]
        validate(claim, "Claim")
        return claim

    def heartbeat(self, claim, progress):
        response = self.client.post(self.job_url(claim, "heartbeat"), headers=self.headers(claim),
                                    json={"progress": min(99, max(0, int(progress)))})
        self.check_response(response, (200,))
        result = response.json()["data"]
        validate(result, "HeartbeatResult")
        if result["cancel_requested"]:
            raise LeaseLost("任务已取消")
        return result

    def download(self, claim, target, guard):
        expected = f"/internal/v1/jobs/{claim['job_id']}/input"
        if claim["input_url"] != expected:
            raise WorkerError("INVALID_RESULT", "拒绝访问领取任务以外的媒体地址")
        guard.check()
        digest, size = hashlib.sha256(), 0
        try:
            with self.client.stream("GET", self.job_url(claim, "input"),
                                    headers=self.headers(claim), follow_redirects=False) as response:
                self.check_response(response, (200,))  # No Range was requested: never accept a partial file.
                with Path(target).open("wb") as file:
                    for chunk in response.iter_bytes(1024 * 1024):
                        guard.check()
                        size += len(chunk)
                        if size > self.settings.max_input_bytes:
                            raise WorkerError("MEDIA_TOO_LARGE", "输入媒体超出文件大小上限")
                        digest.update(chunk)
                        file.write(chunk)
            if not size or digest.hexdigest() != claim["input_sha256"]:
                raise WorkerError("INPUT_DIGEST_MISMATCH", "输入媒体摘要与领取任务不一致")
        except httpx.TransportError as error:
            raise WorkerError("WORKER_API_ERROR", "读取指定媒体失败", True) from error

    def upload(self, claim, path, kind, timestamp_ms, guard):
        path = Path(path)
        size = path.stat().st_size
        if not 0 < size <= self.settings.max_artifact_bytes:
            raise WorkerError("MEDIA_TOO_LARGE", "产物超出文件大小上限")
        digest = hashlib.sha256(path.read_bytes()).hexdigest() if size < 1024 * 1024 else None
        if digest is None:
            from workers.vision_core.media import digest_file
            digest = digest_file(path)
        data = {"artifact_key": f"{claim['lease_token']}/{path.name}", "kind": kind,
                "timestamp_ms": "" if timestamp_ms is None else str(timestamp_ms), "origin_offset_ms": "0"}
        mime = "image/jpeg" if kind == "keyframe" else "video/mp4" if kind == "proxy" else "audio/wav"
        for attempt in range(3):
            guard.check()
            try:
                # Reopen on retry; multipart must start at byte zero, with the same immutable file/key.
                with path.open("rb") as file:
                    response = self.client.post(self.job_url(claim, "artifacts"),
                        headers=self.headers(claim), data=data, files={"file": (path.name, file, mime)})
                if response.status_code >= 500 and attempt < 2:
                    guard.wait(0.25 * (attempt + 1))
                    continue
                self.check_response(response, (200, 201))
                asset = response.json()["data"]
                validate(asset, "Artifact")
                if asset["sha256"] != digest or asset["byte_size"] != size:
                    raise WorkerError("INVALID_RESULT", "Go 返回的产物摘要或大小不一致")
                return asset["asset_id"]
            except httpx.TransportError as error:
                if attempt == 2:
                    raise WorkerError("WORKER_API_ERROR", "上传产物失败", True) from error
                guard.wait(0.25 * (attempt + 1))

    def complete(self, claim, payload, guard):
        # payload is serialized once by the runner. Never reconstruct JSON during retries.
        response = self.request("POST", self.job_url(claim, "complete"), guard=guard,
            headers={**self.headers(claim), "Content-Type": "application/json; charset=utf-8"}, content=payload)
        self.check_response(response, (200,))
        confirmation = response.json()["data"]
        validate(confirmation, "CompleteResult")
        if confirmation["job_id"] != claim["job_id"]:
            raise WorkerError("INVALID_RESULT", "完成确认属于其他任务")

    def fail(self, claim, error, guard):
        response = self.request("POST", self.job_url(claim, "fail"), guard=guard,
            headers={**self.headers(claim), "Content-Type": "application/json"}, content=json_bytes({
                "error_code": error.code, "message": str(error), "retryable": error.retryable}),
            )  # No exception payload/media content is sent to Go.
        self.check_response(response, (200,))
        result = response.json()["data"]
        validate(result, "FailResult")
        if result["job_id"] != claim["job_id"]:
            raise WorkerError("INVALID_RESULT", "失败确认属于其他任务")
