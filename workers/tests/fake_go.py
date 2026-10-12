"""Protocol harness only. This is NOT the team's real Go implementation."""
from datetime import datetime, timedelta, timezone
from email.parser import BytesParser
from email.policy import default
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import threading
from uuid import uuid4

import httpx

from workers.common.contracts import parameter_digest, validate
from workers.vision_core.media import digest_file


def make_claim(settings, source, stage="video_analysis", duration=2000, interval=500, limit=20):
    now = datetime.now(timezone.utc)
    ids = {name: str(uuid4()) for name in ("job_id", "run_id", "session_id", "media_asset_id", "lease_token")}
    parameters = {"config_profile": "p0-v1", "keyframe_interval_ms": interval,
                  "max_keyframes": limit, "asr_device": "cpu"}
    return {**ids, "schema_version": "1.1", "stage": stage,
            "lease_expires_at": (now + timedelta(seconds=120)).isoformat(),
            "execution_deadline_at": (now + timedelta(seconds=180)).isoformat(),
            "input_url": f"/internal/v1/jobs/{ids['job_id']}/input", "input_sha256": digest_file(source),
            "duration_ms": None if stage == "probe" else duration,
            "parameters": parameters, "execution": {"processor_version": settings.processor_version,
                "ffmpeg_build_sha256": settings.ffmpeg_sha256, "asr_model": None,
                "parameters_sha256": parameter_digest(parameters)},
            "rights_version": 1, "storage_generation": 0}


class FakeGo:
    def __init__(self, source, claim):
        self.source, self.claim = source, claim
        self.assets, self.complete_bytes, self.failures, self.requests = {}, [], [], []
        self.fail_complete_once = False
        self.cancel_heartbeat = False
        self.redirect_input = False
        self.claimed = False

    def handle(self, request):
        self.requests.append((request.method, request.url.path))
        assert request.headers["Authorization"] == "Bearer test-token"
        path = request.url.path
        if path.endswith("/claim"):
            validate(json.loads(request.content), "ClaimRequest")
            if self.claimed:
                return httpx.Response(204)
            self.claimed = True
            return self.reply(self.claim)
        assert request.headers["X-Lease-Token"] == self.claim["lease_token"]
        assert path.startswith(f"/internal/v1/jobs/{self.claim['job_id']}/")
        if path.endswith("/input"):
            if self.redirect_input:
                return httpx.Response(302, headers={"Location": "http://external.invalid/private"})
            return httpx.Response(200, content=self.source.read_bytes())
        if path.endswith("/heartbeat"):
            validate(json.loads(request.content), "Heartbeat")
            if self.cancel_heartbeat:
                return httpx.Response(409)
            return self.reply({"lease_expires_at": (datetime.now(timezone.utc) + timedelta(seconds=120)).isoformat(),
                               "cancel_requested": False})
        if path.endswith("/artifacts"):
            message = BytesParser(policy=default).parsebytes(
                b"Content-Type: " + request.headers["Content-Type"].encode() + b"\r\n\r\n" + request.content)
            parts = {p.get_param("name", header="content-disposition"): p.get_payload(decode=True)
                     for p in message.iter_parts()}
            key, content = parts["artifact_key"].decode(), parts["file"]
            assert key.startswith(self.claim["lease_token"] + "/")
            assert parts["kind"].decode() == ("proxy" if self.claim["stage"] == "probe" else "keyframe")
            assert parts["origin_offset_ms"] == b"0"
            asset = {"asset_id": str(uuid4()), "sha256": hashlib.sha256(content).hexdigest(), "byte_size": len(content)}
            self.assets[asset["asset_id"]] = {"dto": asset, "bytes": content, "key": key}
            return self.reply(asset, 201)
        if path.endswith("/complete"):
            self.complete_bytes.append(request.content)
            result = json.loads(request.content)
            validate(result, "StageResult")
            assert result["execution"] == self.claim["execution"]
            asset_ids = [f["asset_id"] for f in result.get("frames", [])]
            if "playback_asset_id" in result:
                asset_ids.append(result["playback_asset_id"])
            assert all(asset_id in self.assets for asset_id in asset_ids)
            if self.fail_complete_once and len(self.complete_bytes) == 1:
                return httpx.Response(503)
            return self.reply({"job_id": self.claim["job_id"], "status": "succeeded",
                               "duplicate": len(self.complete_bytes) > 1})
        if path.endswith("/fail"):
            self.failures.append(json.loads(request.content))
            return self.reply({"job_id": self.claim["job_id"], "status": "failed", "next_retry_at": None})
        return httpx.Response(404)

    @staticmethod
    def reply(data, status=200):
        return httpx.Response(status, json={"data": data, "request_id": "synthetic-harness"})

    def client(self):
        return httpx.Client(transport=httpx.MockTransport(self.handle), trust_env=False)

    def server(self):
        state = self
        class Handler(BaseHTTPRequestHandler):
            def handle_request(self):
                body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
                response = state.handle(httpx.Request(self.command, "http://localhost" + self.path,
                                                      headers=dict(self.headers), content=body))
                self.send_response(response.status_code)
                for name, value in response.headers.items():
                    self.send_header(name, value)
                self.send_header("Content-Length", str(len(response.content)))
                self.end_headers()
                self.wfile.write(response.content)
            do_GET = do_POST = handle_request
            def log_message(self, *args):
                pass
        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        return server
