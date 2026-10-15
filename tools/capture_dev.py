"""Windows, single-machine B0 environment. All runtime/private data stays in var/capture-dev.

Uses only the Python standard library; the configured Python has workers' locked dependencies.
Never accepts an external database URL, resets data, or kills a process by executable name.
"""
import argparse
import contextlib
import ctypes
from ctypes import wintypes
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import ssl
import subprocess
import sys
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
STATE = ROOT / "var/capture-dev"
PORTS = dict(database=15436, api=8080, media=8889, control=9997, udp=8189, https=8443, ca=8081)
NO_WINDOW = getattr(subprocess, "CREATE_NO_WINDOW", 0)


def read_json(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def save_json(path, value):
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, indent=2, ensure_ascii=False), encoding="utf-8")
    temporary.replace(path)


def checked_state():
    # Refuse redirected runtime directories (including junctions/symlinks).
    if STATE.resolve() != ROOT / "var/capture-dev":
        raise RuntimeError("var/capture-dev must not be redirected outside this checkout")
    STATE.mkdir(parents=True, exist_ok=True)
    for child in STATE.iterdir():
        if child.resolve() != child.absolute():
            raise RuntimeError("Runtime child path is redirected: " + child.name)


@contextlib.contextmanager
def exclusive():
    import msvcrt
    checked_state()
    with (STATE / "operation.lock").open("a+b") as stream:
        stream.seek(0)
        if not stream.read(1):
            stream.write(b"0")
            stream.flush()
        stream.seek(0)
        try:
            msvcrt.locking(stream.fileno(), msvcrt.LK_NBLCK, 1)
        except OSError as error:
            raise RuntimeError("Another capture-dev operation is running") from error
        try:
            yield
        finally:
            stream.seek(0)
            msvcrt.locking(stream.fileno(), msvcrt.LK_UNLCK, 1)


def run(args, env=None, cwd=ROOT, log="setup", input_text=None):
    # A Windows postgres child can inherit a pipe even after pg_ctl exits. Use regular
    # files and wait only for the command, so daemon descendants cannot block communicate().
    with (STATE / f"{log}.log").open("ab") as stream:
        offset = stream.tell()
        result = subprocess.run([str(arg) for arg in args], env=env, cwd=cwd, input=input_text,
                                stdout=stream, stderr=stream, text=True, encoding="utf-8", errors="replace",
                                creationflags=NO_WINDOW)
    if result.returncode:
        # Do not print command arguments/environment: they may carry bearer credentials.
        raise RuntimeError(f"{Path(args[0]).name} exited {result.returncode}; see {STATE / (log + '.log')}")
    with (STATE / f"{log}.log").open("rb") as stream:
        stream.seek(offset)
        return stream.read().decode("utf-8", errors="replace").strip()


def identity(pid, terminate=False, expected=None):
    """Inspect/terminate through the SAME process handle, preventing PID reuse races."""
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.OpenProcess.restype = wintypes.HANDLE
    kernel.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
    kernel.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel.GetProcessTimes.argtypes = [wintypes.HANDLE] + [ctypes.POINTER(wintypes.FILETIME)] * 4
    kernel.QueryFullProcessImageNameW.argtypes = [wintypes.HANDLE, wintypes.DWORD, wintypes.LPWSTR, ctypes.POINTER(wintypes.DWORD)]
    kernel.TerminateProcess.argtypes = [wintypes.HANDLE, wintypes.UINT]
    handle = kernel.OpenProcess(0x1000 | (1 if terminate else 0), False, int(pid))
    if not handle:
        if ctypes.get_last_error() == 87:  # PID no longer exists
            return None
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        created, exited, system, user = (wintypes.FILETIME() for _ in range(4))
        buffer, size = ctypes.create_unicode_buffer(32768), wintypes.DWORD(32768)
        if not kernel.GetProcessTimes(handle, ctypes.byref(created), ctypes.byref(exited), ctypes.byref(system), ctypes.byref(user)):
            raise ctypes.WinError(ctypes.get_last_error())
        if not kernel.QueryFullProcessImageNameW(handle, 0, buffer, ctypes.byref(size)):
            raise ctypes.WinError(ctypes.get_last_error())
        current = dict(pid=int(pid), executable=buffer.value.lower(), created=(created.dwHighDateTime << 32) | created.dwLowDateTime)
        if terminate:
            if current != expected:
                raise RuntimeError("Refusing to terminate a reused PID")
            if not kernel.TerminateProcess(handle, 0):
                raise ctypes.WinError(ctypes.get_last_error())
        return current
    finally:
        kernel.CloseHandle(handle)


def processes():
    path = STATE / "processes.json"
    return read_json(path) if path.exists() else {}


def alive(record):
    return identity(record["pid"]) == record


def spawn(name, args, env):
    with (STATE / f"{name}.log").open("ab") as stream:
        process = subprocess.Popen([str(arg) for arg in args], cwd=ROOT, env=env,
                                   stdin=subprocess.DEVNULL, stdout=stream, stderr=stream,
                                   creationflags=NO_WINDOW, close_fds=True)
    record = identity(process.pid)
    if not record:
        raise RuntimeError(f"{name} exited on startup; see its log")
    current = processes()
    current[name] = record
    save_json(STATE / "processes.json", current)


def environment(config):
    env = dict(os.environ)
    # Prevent unrelated .env / inherited application settings from changing this sandbox.
    for line in (ROOT / ".env.example").read_text(encoding="utf-8-sig").splitlines():
        if line.strip() and not line.lstrip().startswith("#") and "=" in line:
            key, value = line.split("=", 1)
            env[key.strip()] = value.strip()
    p, secret, runtime = config["ports"], config["secrets"], config["runtime"]
    origin = f'https://{config["ip"]}:{p["https"]}'
    env.update(APP_ENV="development", API_LISTEN_ADDR=f'127.0.0.1:{p["api"]}', PUBLIC_ORIGIN=origin,
               DATABASE_URL=f'postgres://capture_dev:{secret["database"]}@127.0.0.1:{p["database"]}/capture_dev?sslmode=disable',
               DATABASE_MIGRATIONS_DIR=str(ROOT / "database"), PGPASSWORD=secret["database"], PGCLIENTENCODING="UTF8",
               PGOPTIONS="-c lc_messages=C", MEDIA_ROOT=str(STATE / "media"), WORKER_TEMP_ROOT=str(STATE / "worker-tmp"),
               DELETION_LEDGER_PATH=str(STATE / "ledger/deletions.jsonl"), WORKER_TOKEN=secret["worker"],
               WORKER_ID="capture-dev-worker", WORKER_PROCESSOR_VERSION=runtime["processor_version"],
               FFMPEG_BUILD_SHA256=runtime["ffmpeg_build_sha256"], VISION_FFMPEG_BINARY=runtime["ffmpeg_binary"],
               WORKER_CAPABILITIES="probe,audio_analysis,video_analysis", ASR_MODEL_NAME="not-installed",
               ASR_MODEL_REVISION="not-installed", MODEL_MONTHLY_BUDGET="0", BOOTSTRAP_TEST_PASSWORD=secret["login"],
               CAPTURE_ENABLED="true", CAPTURE_PUBLIC_URL=origin, CAPTURE_MEDIA_URL=origin + "/media",
               CAPTURE_CONTROL_URL=f'http://127.0.0.1:{p["control"]}', CAPTURE_CONTROL_TOKEN=secret["control"],
               CAPTURE_EVENT_TOKEN=secret["event"], CAPTURE_EVENT_URL=f'http://127.0.0.1:{p["api"]}/internal/v1/streams/events',
               CAPTURE_RECORD_ROOT=str(STATE / "recordings"), CAPTURE_FFMPEG=config["tools"]["ffmpeg"],
               CAPTURE_FFPROBE=config["tools"]["ffprobe"], VITE_DATA_SOURCE="api", GOTOOLCHAIN="local", GOTELEMETRY="off",
               PYTHONUTF8="1", PYTHONUNBUFFERED="1")
    env["PATH"] = str(Path(config["tools"]["node"]).parent) + os.pathsep + env.get("PATH", "")
    # MediaMTX environment overrides must not supersede the generated configuration.
    return {key: value for key, value in env.items() if not key.startswith("MTX_")}


def psql(config, statement=None, file=None, database="capture_dev"):
    args = [Path(config["tools"]["postgres_bin"]) / "psql.exe", "-X", "-q", "-At", "-h", "127.0.0.1",
            "-p", str(config["ports"]["database"]), "-U", "capture_dev", "-d", database, "-v", "ON_ERROR_STOP=1"]
    return run(args + (["-f", file] if file else ["-c", statement]), environment(config))


def free_ports(config, database=True):
    for name, port in config["ports"].items():
        if name == "database" and not database:
            continue
        kind = socket.SOCK_DGRAM if name == "udp" else socket.SOCK_STREAM
        with socket.socket(socket.AF_INET, kind) as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
            host = config["ip"] if name in ("https", "ca", "udp") else "127.0.0.1"
            try:
                sock.bind((host, port))
            except OSError as error:
                raise RuntimeError(f"{name} {host}:{port} unavailable; no existing service was stopped") from error


def database_start(config):
    pgdata = STATE / "pgdata"
    env = environment(config)
    pg = Path(config["tools"]["postgres_bin"])
    if not (pgdata / "PG_VERSION").exists():
        password_file = STATE / "initdb-password.private"
        password_file.write_text(config["secrets"]["database"] + "\n", encoding="utf-8")
        try:
            run([pg / "initdb.exe", "-D", pgdata, "-U", "capture_dev", "--auth=scram-sha-256",
                 "--encoding=UTF8", "--locale=C", "--pwfile", password_file], env)
        finally:
            password_file.unlink(missing_ok=True)
    run([pg / "pg_ctl.exe", "-D", pgdata, "-l", STATE / "postgres.log", "-w", "-t", "30", "-o",
         f'-h 127.0.0.1 -p {config["ports"]["database"]}', "start"], env)
    pid = int((pgdata / "postmaster.pid").read_text().splitlines()[0])
    current = processes()
    current["postgres"] = identity(pid)
    save_json(STATE / "processes.json", current)
    if psql(config, "SELECT 1 FROM pg_database WHERE datname='capture_dev'", database="postgres") != "1":
        psql(config, "CREATE DATABASE capture_dev", database="postgres")


def generate_configs(config):
    p, ip = config["ports"], config["ip"]
    hook = '"' + (STATE / "bin/capture-hook.exe").as_posix() + '"'
    # JSON is a YAML subset accepted by pinned MediaMTX. No additional Python YAML dependency.
    save_json(STATE / "mediamtx.yml", dict(logLevel="warn", authMethod="http",
        authHTTPAddress=f'http://127.0.0.1:{p["api"]}/internal/v1/streams/auth', authHTTPExclude=[],
        api=True, apiAddress=f'127.0.0.1:{p["control"]}', rtsp=False, rtmp=False, hls=False, srt=False, playback=False,
        webrtc=True, webrtcAddress=f'127.0.0.1:{p["media"]}', webrtcLocalUDPAddress=f'{ip}:{p["udp"]}',
        webrtcAdditionalHosts=[ip], pathDefaults=dict(source="publisher", overridePublisher=False, record=True,
            recordPath=(STATE / "recordings/%path/%Y-%m-%d_%H-%M-%S-%f").as_posix(), recordFormat="fmp4",
            recordPartDuration="1s", recordSegmentDuration="30s", recordMaxPartSize="50M", recordDeleteAfter="0s",
            runOnReady=hook + " online", runOnNotReady=hook + " offline", runOnRecordSegmentCreate=hook + " segment_started",
            runOnRecordSegmentComplete=hook + " segment_complete"), paths={"~^captures/[0-9a-f-]{36}/[0-9]+$": {}}))
    caddy = '''{
    admin off
    auto_https disable_redirects
    skip_install_trust
    storage file_system {
        root "%s"
    }
}
https://%s:%s {
    bind %s
    tls internal
    handle /api/v1/* {
        reverse_proxy 127.0.0.1:%s
    }
    handle /health/* {
        reverse_proxy 127.0.0.1:%s
    }
    handle_path /media/* {
        reverse_proxy 127.0.0.1:%s {
            header_down Location ^/ /media/
        }
    }
    handle /internal/* {
        respond 404
    }
    handle {
        root * "%s"
        try_files {path} /index.html
        file_server
    }
}
http://%s:%s {
    bind %s
    handle /capture-ca.crt {
        root * "%s"
        header Content-Type application/x-x509-ca-cert
        file_server
    }
    handle {
        respond 404
    }
}
''' % ((STATE / "caddy-storage").as_posix(), ip, p["https"], ip, p["api"], p["api"], p["media"],
       (ROOT / "frontend/dist").as_posix(), ip, p["ca"], ip, (STATE / "public").as_posix())
    (STATE / "Caddyfile").write_text(caddy, encoding="utf-8")


def wait_http(url, context=None, timeout=30):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=context))
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with opener.open(url, timeout=2) as response:
                if response.status == 200:
                    return
        except (OSError, ValueError):
            time.sleep(0.25)
    raise RuntimeError("Readiness timeout: " + url)


def handoff(config):
    ca = STATE / "public/capture-ca.crt"
    fingerprint = hashlib.sha256(ssl.PEM_cert_to_DER_cert(ca.read_text())).hexdigest().upper()
    origin = environment(config)["PUBLIC_ORIGIN"]
    receipt = {"code_sha": config["code_sha"], "source_dirty": config.get("source_dirty", True),
               "origin": origin, "capture_page": origin + "/capture-demo.html",
               "ca_download": f'http://{config["ip"]}:{config["ports"]["ca"]}/capture-ca.crt',
               "ca_sha256": ":".join(fingerprint[i:i+2] for i in range(0, len(fingerprint), 2)),
               "fixture": config["fixture"], "versions": config["versions"],
               "android_trust": "pending_manual_verification", "real_phone_capture": "pending_B1"}
    save_json(STATE / "handoff.json", receipt)
    (STATE / "accounts.private.txt").write_text(
        "Local development accounts only; do not commit or publish.\n"
        "admin / academic_demo / supervisor_demo / teacher_demo\n"
        "Initial password (existing passwords are never reset): " + config["secrets"]["login"] + "\n", encoding="utf-8")


def prepare(args):
    if any(alive(record) for record in processes().values()):
        raise RuntimeError("Stop this environment before prepare; existing data will be preserved")
    ip = str(ipaddress.IPv4Address(args.ip))
    if not ipaddress.ip_address(ip).is_private or ip.startswith("127.") or ip == "0.0.0.0":
        raise RuntimeError("Supply the computer's actual private LAN IPv4 address")
    tool_paths = read_json(Path(args.tools))
    for key in ("go", "node", "npm_cli", "python", "mediamtx", "ffmpeg", "ffprobe", "caddy"):
        tool_paths[key] = str(Path(tool_paths[key]).resolve(strict=True))
    tool_paths["postgres_bin"] = str(Path(tool_paths["postgres_bin"]).resolve(strict=True))
    for exe in ("initdb", "pg_ctl", "psql", "postgres"):
        if not (Path(tool_paths["postgres_bin"]) / (exe + ".exe")).is_file():
            raise RuntimeError("Missing PostgreSQL executable: " + exe)
    for directory in ("bin", "public", "recordings", "media", "worker-tmp", "ledger"):
        (STATE / directory).mkdir(exist_ok=True)
    prior = read_json(STATE / "private.json") if (STATE / "private.json").exists() else {}
    secret = prior.get("secrets") or {key: secrets.token_hex(24) for key in ("database", "worker", "event", "control", "login")}
    config = dict(ip=ip, tools=tool_paths, ports=PORTS, secrets=secret)
    free_ports(config)
    runtime_env = dict(os.environ, PYTHONUTF8="1")
    runtime_env.pop("VISION_FFMPEG_BINARY", None)
    config["runtime"] = json.loads(run([tool_paths["python"], "-m", "workers", "runtime-info"], runtime_env))
    config["code_sha"] = run(["git", "rev-parse", "HEAD"])
    config["source_dirty"] = bool(run(["git", "status", "--porcelain"]))
    config["versions"] = {name: run([tool_paths[name], flag]).splitlines()[0] for name, flag in
                           (("go", "version"), ("node", "--version"), ("python", "--version"),
                            ("mediamtx", "--version"), ("ffmpeg", "-version"), ("ffprobe", "-version"), ("caddy", "version"))}
    config["versions"]["postgres"] = run([Path(tool_paths["postgres_bin"]) / "postgres.exe", "--version"])
    if config["versions"]["mediamtx"].strip() != "v1.15.3":
        raise RuntimeError("This configuration is validated with MediaMTX v1.15.3 only")
    if int(config["versions"]["node"].lstrip("v").split(".")[0]) < 24:
        raise RuntimeError("The frontend requires Node.js 24 or newer")
    save_json(STATE / "private.json", config)
    env = environment(config)
    for name in ("api", "migrate", "init-deletion-ledger", "seed-dev", "capture-hook"):
        print("Building " + name, flush=True)
        run([tool_paths["go"], "build", "-o", STATE / f"bin/{name}.exe", f"./cmd/{name}"], env)
    print("Installing locked frontend dependencies and building API mode", flush=True)
    run([tool_paths["node"], tool_paths["npm_cli"], "ci", "--no-audit", "--no-fund"], env, ROOT / "frontend")
    run([tool_paths["node"], tool_paths["npm_cli"], "run", "build"], env, ROOT / "frontend")
    try:
        database_start(config)
        run([STATE / "bin/migrate.exe"], env)
        run([STATE / "bin/init-deletion-ledger.exe"], env)
        run([STATE / "bin/seed-dev.exe"], env)
        seeded = psql(config, file=ROOT / "deploy/capture/development.sql")
        config["fixture"] = json.loads(seeded.splitlines()[-1])
        save_json(STATE / "private.json", config)
        generate_configs(config)
        run([tool_paths["caddy"], "validate", "--config", STATE / "Caddyfile", "--adapter", "caddyfile"], env)
    finally:
        stop(config, check_capture=False)
    print("Prepared. Next: python tools/capture_dev.py start", flush=True)


def start(config):
    current = processes()
    live = [name for name, record in current.items() if alive(record)]
    if live:
        raise RuntimeError("Environment already has running processes: " + ", ".join(live) + "; use status or stop")
    if "fixture" not in config:
        raise RuntimeError("Preparation did not finish; rerun prepare")
    free_ports(config)
    env = environment(config)
    generate_configs(config)
    try:
        database_start(config)
        spawn("api", [STATE / "bin/api.exe"], env)
        wait_http(f'http://127.0.0.1:{config["ports"]["api"]}/health/ready')
        spawn("mediamtx", [config["tools"]["mediamtx"], STATE / "mediamtx.yml"], env)
        worker_env = dict(env, WORKER_CAPABILITIES="probe,video_analysis", WORKER_CLEANUP_ENABLED="1",
                          WORKER_API_BASE=f'http://127.0.0.1:{config["ports"]["api"]}/internal/v1')
        worker_env.pop("WORKER_CLEANUP_CONTRACT_PATH", None)
        spawn("worker", [config["tools"]["python"], "-m", "workers", "run"], worker_env)
        spawn("caddy", [config["tools"]["caddy"], "run", "--config", STATE / "Caddyfile", "--adapter", "caddyfile"], env)
        root_ca = STATE / "caddy-storage/pki/authorities/local/root.crt"
        deadline = time.monotonic() + 30
        while not root_ca.exists() and time.monotonic() < deadline:
            time.sleep(0.25)
        shutil.copyfile(root_ca, STATE / "public/capture-ca.crt")
        context = ssl.create_default_context(cafile=str(STATE / "public/capture-ca.crt"))
        wait_http(env["PUBLIC_ORIGIN"] + "/health/ready", context)
        wait_http(env["PUBLIC_ORIGIN"] + "/capture-demo.html", context)
        for name, record in processes().items():
            if not alive(record):
                raise RuntimeError(name + " exited; see its log")
        handoff(config)
    except Exception:
        stop(config, check_capture=False)
        raise
    status()
    print("URLs / public CA fingerprint: " + str(STATE / "handoff.json"))
    print("Local accounts: " + str(STATE / "accounts.private.txt"))


def stop(config, check_capture=True):
    current = processes()
    if check_capture and "postgres" in current and alive(current["postgres"]):
        active = psql(config, "SELECT count(*) FROM teaching.live_captures WHERE state IN ('waiting','recording','finalizing')")
        if active != "0":
            raise RuntimeError("Stop active captures in the page and wait for ready/failed before stopping services")
    for name in ("caddy", "mediamtx", "worker", "api", "postgres"):
        record = current.get(name)
        if record and alive(record):
            if name == "postgres":
                run([Path(config["tools"]["postgres_bin"]) / "pg_ctl.exe", "-D", STATE / "pgdata", "-m", "fast", "-w", "stop"], environment(config))
            else:
                identity(record["pid"], terminate=True, expected=record)
        current.pop(name, None)
        save_json(STATE / "processes.json", current)


def status():
    current = processes()
    print(json.dumps({name: "running" if name in current and alive(current[name]) else "stopped"
                      for name in ("postgres", "api", "mediamtx", "worker", "caddy")}, indent=2))


def start_detached():
    checked_state()
    # Job-managed terminals may terminate all children when their command/turn ends.
    # Start the launcher outside that job; this does not elevate the Windows user.
    with (STATE / "launcher.log").open("ab") as stream:
        process = subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "start"],
                                   cwd=ROOT, stdin=subprocess.DEVNULL, stdout=stream, stderr=stream,
                                   creationflags=NO_WINDOW | subprocess.CREATE_BREAKAWAY_FROM_JOB,
                                   close_fds=True)
    try:
        code = process.wait(timeout=60)
    except subprocess.TimeoutExpired as error:
        raise RuntimeError("Startup still running; check launcher.log and status before retrying") from error
    if code:
        raise RuntimeError("Detached startup failed; see var/capture-dev/launcher.log")
    status()
    print("Detached environment started. Handoff: " + str(STATE / "handoff.json"))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    setup = commands.add_parser("prepare")
    setup.add_argument("--ip", required=True)
    setup.add_argument("--tools", required=True)
    for name in ("start", "start-detached", "stop", "status", "trust"):
        commands.add_parser(name)
    args = parser.parse_args()
    if os.name != "nt":
        parser.error("This launcher targets Windows; use the deployment templates on other systems")
    if args.command == "start-detached":
        start_detached()
        return
    with exclusive():
        if args.command == "prepare":
            prepare(args)
        elif args.command == "status":
            status()
        elif args.command == "trust":
            cert = STATE / "public/capture-ca.crt"
            if not cert.exists():
                raise RuntimeError("Run prepare and start first")
            run(["certutil.exe", "-user", "-addstore", "Root", cert], log="trust")
            print("Installed this environment's public CA in the current user's Root store")
        else:
            config = read_json(STATE / "private.json")
            (start if args.command == "start" else stop)(config)


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, ValueError, KeyError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
