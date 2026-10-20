"""Local team website: official Go API, React build and CPU vision Worker.

Private state stays in the chosen project data directory. Never resets a database,
changes migration history, installs drivers or terminates processes by name.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
HIDDEN = getattr(subprocess, 'CREATE_NO_WINDOW', 0)
HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def save(path, data):
    temp = path.with_suffix('.tmp')
    temp.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding='utf-8')
    temp.replace(path)


def load(path):
    return json.loads(path.read_text(encoding='utf-8'))


def check_port(port):
    with socket.socket() as sock:
        try: sock.bind(('127.0.0.1', port))
        except OSError as error: raise RuntimeError(f'Port {port} is occupied; no existing service was stopped') from error


def ascii_root(state):
    if os.name != 'nt' or str(state).isascii(): return state
    for letter in ('V', 'W', 'Y', 'Z'):
        drive = Path(letter + ':/')
        if drive.exists():
            if os.path.samefile(drive, state): return drive
            continue
        subprocess.run(['subst.exe', letter + ':', str(state)], check=True, creationflags=HIDDEN)
        return drive
    raise RuntimeError('No free drive alias for PostgreSQL Unicode path compatibility')


def environment(config, state):
    env = dict(os.environ)
    for line in (ROOT / '.env.example').read_text(encoding='utf-8-sig').splitlines():
        if line.strip() and not line.lstrip().startswith('#') and '=' in line:
            key, value = line.split('=', 1); env[key.strip()] = value.strip()
    ports, secret, runtime = config['ports'], config['secrets'], config['runtime']
    origin = f"http://127.0.0.1:{ports['web']}"
    env.update(APP_ENV='development', API_LISTEN_ADDR=f"127.0.0.1:{ports['api']}", PUBLIC_ORIGIN=origin,
        DATABASE_URL=f"postgres://team_web:{secret['database']}@127.0.0.1:{ports['database']}/cangjie_visual?sslmode=disable",
        DATABASE_MIGRATIONS_DIR=str(ROOT / 'database'), PGPASSWORD=secret['database'], PGCLIENTENCODING='UTF8',
        MEDIA_ROOT=str(state / 'media'), WORKER_TEMP_ROOT=str(state / 'worker-tmp'),
        DELETION_LEDGER_PATH=str(state / 'ledger/deletions.jsonl'), WORKER_ID='worker-01', WORKER_TOKEN=secret['worker'],
        WORKER_CAPABILITIES='probe,audio_analysis,video_analysis', ASR_MODEL_NAME='not-installed', ASR_MODEL_REVISION='not-installed',
        WORKER_PROCESSOR_VERSION=runtime['processor_version'], FFMPEG_BUILD_SHA256=runtime['ffmpeg_build_sha256'],
        VISION_FFMPEG_BINARY=runtime['ffmpeg_binary'], VISION_ANALYSIS_ENABLED='1', VISION_MODEL_REVISION=runtime['visual_model']['revision'],
        VISION_MODEL_ROOT=config['models'], VISION_RUNTIME='cpu', VISION_RUNTIME_CONFIG=str(ROOT / 'workers/config/runtime.cpu.json'),
        VISION_CPU_THREADS='4', VISION_CV_THREADS='4', VISION_BATCH_SIZE='1', VISION_FIXED_BATCH='0',
        VISION_GPU_DETECTION='0', VISION_GPU_YOLOX='0', VISION_GPU_THREADS='1', VISION_PREFETCH='1', WORKER_CONCURRENCY='1', WORKER_CLEANUP_ENABLED='1',
        MODEL_MONTHLY_BUDGET='0', REPORT_API_KEY='', CAPTURE_ENABLED='false', ASR_DEVICE='cpu',
        BOOTSTRAP_TEST_PASSWORD=secret['login'], VITE_DATA_SOURCE='api', TEAM_API_TARGET=f"http://127.0.0.1:{ports['api']}",
        GOMAXPROCS='2', GOTOOLCHAIN='local', GOTELEMETRY='off', PYTHONUTF8='1', PYTHONUNBUFFERED='1')
    for name in list(env):
        if name.startswith('MTX_'): del env[name]
    runtime_root = ascii_root(Path(config['go']).parents[2])
    temp = runtime_root / 'tmp'; temp.mkdir(exist_ok=True)
    env.update(GOCACHE=str(runtime_root / 'go-cache'), GOMODCACHE=str(runtime_root / 'go-mod'), GOTMPDIR=str(temp), TMP=str(temp), TEMP=str(temp))
    env['PATH'] = str(Path(config['node']).parent) + os.pathsep + env.get('PATH', '')
    return env


def pg_tools(config):
    original = Path(config['pg_bin'])
    root = original.parents[1]
    return ascii_root(root) / original.relative_to(root)


def run(command, env, state, *, cwd=ROOT):
    with (state / 'prepare.log').open('ab') as output:
        result = subprocess.run([str(a) for a in command], cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                                stdout=output, stderr=output, creationflags=HIDDEN)
    if result.returncode: raise RuntimeError(f'{Path(command[0]).name} failed; inspect {state / "prepare.log"}')


def sql(config, state, statement, database='cangjie_visual'):
    command = [pg_tools(config) / 'psql.exe', '-X', '-q', '-At', '-h', '127.0.0.1', '-p', str(config['ports']['database']),
               '-U', 'team_web', '-d', database, '-v', 'ON_ERROR_STOP=1']
    result = subprocess.run([str(a) for a in command], env=environment(config, state), input=statement.encode('utf-8'), capture_output=True, creationflags=HIDDEN)
    if result.returncode:
        with (state / 'prepare.log').open('ab') as output: output.write(result.stderr)
        raise RuntimeError('Local database query failed; inspect prepare.log')
    return result.stdout.decode('utf-8').strip()


def database_start(config, state):
    env = environment(config, state)
    alias = ascii_root(state)
    pgdata = alias / 'postgres-data'
    pg = pg_tools(config)
    if not (pgdata / 'PG_VERSION').exists():
        check_port(config['ports']['database'])
        password = alias / 'init-password.private'; password.write_text(config['secrets']['database'], encoding='utf-8')
        try: run([pg / 'initdb.exe', '-D', pgdata, '-U', 'team_web', '-A', 'scram-sha-256', '--encoding=UTF8', '--locale=C', '--pwfile', password], env, state)
        finally: password.unlink(missing_ok=True)
    status = subprocess.run([str(pg / 'pg_ctl.exe'), 'status', '-D', str(pgdata)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, creationflags=HIDDEN)
    if status.returncode:
        check_port(config['ports']['database'])
        run([pg / 'pg_ctl.exe', 'start', '-D', pgdata, '-l', alias / 'postgres.log', '-w', '-o',
             f"-h 127.0.0.1 -p {config['ports']['database']} -c shared_buffers=32MB -c max_connections=40"], env, state)
    return pgdata


def records(state): return load(state / 'processes.json') if (state / 'processes.json').exists() else {}


def alive(record):
    from capture_dev import alive as check
    return check(record)


def stop(config, state):
    from capture_dev import identity
    from capture_worker_job import Job
    current = records(state)
    if any(alive(r) for r in current.values()):
        if sql(config, state, "SELECT count(*) FROM teaching.analysis_jobs WHERE status='running'") != '0':
            raise RuntimeError('Analysis is running; cancel it in the page and wait before stopping')
    for name in ('worker', 'web', 'api'):
        record = current.get(name)
        if record:
            if name == 'worker':
                job = Job(record['job'])
                try:
                    if alive(record): identity(record['pid'], terminate=True, expected=record)
                    job.terminate_and_wait()
                finally: job.close()
            elif alive(record): identity(record['pid'], terminate=True, expected=record)
            current.pop(name); save(state / 'processes.json', current)
    alias = ascii_root(state); pgdata = alias / 'postgres-data'
    if (pgdata / 'PG_VERSION').exists():
        status = subprocess.run([str(pg_tools(config) / 'pg_ctl.exe'), 'status', '-D', str(pgdata)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, creationflags=HIDDEN)
        if status.returncode == 0: run([pg_tools(config) / 'pg_ctl.exe', 'stop', '-D', pgdata, '-m', 'fast', '-w'], environment(config, state), state)


def prepare(args, state):
    if any(alive(r) for r in records(state).values()): raise RuntimeError('Stop this deployment before preparing a new build')
    prior = load(state / 'private.json') if (state / 'private.json').exists() else {}
    secret = prior.get('secrets') or {key: secrets.token_hex(24) for key in ('database', 'worker', 'login')}
    if args.login_from and not prior:
        secret['login'] = load(Path(args.login_from))['login_password']
    config = dict(ports=dict(web=8088, api=18088, database=55437), secrets=secret,
                  go=str(Path(args.go).resolve(strict=True)), pg_bin=str(Path(args.pg_bin).resolve(strict=True)),
                  node=str(Path(args.node).resolve(strict=True)), python=str(Path(args.python).resolve(strict=True)),
                  models=str(Path(args.models).resolve(strict=True)))
    if not (ROOT / 'frontend/node_modules/vite/bin/vite.js').exists(): raise RuntimeError('Install frontend locked dependencies with npm ci first')
    for name in ('media', 'worker-tmp', 'ledger', 'bin'): (state / name).mkdir(exist_ok=True)
    env = {**os.environ, 'VISION_RUNTIME': 'cpu', 'PYTHONUTF8': '1'}
    config['runtime'] = json.loads(subprocess.check_output([config['python'], '-m', 'workers', 'runtime-info'], cwd=ROOT, env=env, creationflags=HIDDEN))
    config['code_sha'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    config['source_dirty'] = bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True).strip())
    save(state / 'private.json', config); env = environment(config, state)
    for name in ('api', 'migrate', 'seed-dev', 'init-deletion-ledger'):
        run([config['go'], 'build', '-o', state / f'bin/{name}.exe', f'./cmd/{name}'], env, state)
    run([config['node'], ROOT / 'frontend/node_modules/vite/bin/vite.js', 'build'], env, state, cwd=ROOT / 'frontend')
    database_start(config, state)
    if sql(config, state, "SELECT count(*) FROM pg_database WHERE datname='cangjie_visual'", database='postgres') == '0':
        run([pg_tools(config) / 'createdb.exe', '-h', '127.0.0.1', '-p', str(config['ports']['database']), '-U', 'team_web', 'cangjie_visual'], env, state)
    run([state / 'bin/migrate.exe'], env, state)
    run([state / 'bin/init-deletion-ledger.exe'], env, state)
    run([state / 'bin/seed-dev.exe'], env, state)
    seed = (ROOT / 'deploy/team-web/development.sql').read_text(encoding='utf-8')
    sql(config, state, seed)
    receipt = dict(url='http://127.0.0.1:8088/#/sessions', code_sha=config['code_sha'], source_dirty=config['source_dirty'],
                   processor_version=config['runtime']['processor_version'], visual_model=config['runtime']['visual_model'], execution='cpu')
    save(state / 'deployment.json', receipt)
    (state / '使用说明.txt').write_text('团队网站：http://127.0.0.1:8088/#/sessions\n账号：admin（或 teacher_demo）\n密码：' + secret['login'] + '\n\n在课堂详情上传录像，再点“举手与低头分析”，到“视觉观察”查看人物记录与回放。\n来源须先在“素材与来源”登记并核验。\n只使用CPU，每次一个分析任务。旧试验库保留，正式版不连接旧试验库。\n', encoding='utf-8')
    print('Prepared official team website. Private account guide: ' + str(state / '使用说明.txt'), flush=True)


def wait_ready(config, timeout=35):
    url = f"http://127.0.0.1:{config['ports']['web']}/health/ready"
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with HTTP.open(url, timeout=2) as response:
                if response.status == 200: return
        except OSError: pass
        time.sleep(.25)
    raise RuntimeError('Readiness timeout; inspect local api/web/worker logs')


def start(config, state):
    from capture_dev import identity
    current = records(state)
    if any(alive(r) for r in current.values()):
        if set(current) != {'api','web','worker'} or not all(alive(r) for r in current.values()):
            raise RuntimeError('Deployment is only partly running; stop the owned deployment before restarting')
        wait_ready(config, 5); print('Team website already running: http://127.0.0.1:8088/#/sessions'); return
    for name in ('web', 'api'): check_port(config['ports'][name])
    database_start(config, state)
    env = environment(config, state)
    job_name = 'Local\\CangjieTeamVision-' + secrets.token_hex(16)
    services = [
        ('api', [state / 'bin/api.exe'], env),
        ('web', [config['node'], ROOT / 'frontend/node_modules/vite/bin/vite.js', 'preview', ROOT / 'frontend', '--host', '127.0.0.1', '--port', str(config['ports']['web']), '--strictPort'], env),
        ('worker', [config['python'], Path(__file__).resolve(), '--data-root', state, 'supervise-worker', '--job', job_name],
         {**env, 'WORKER_CAPABILITIES': 'probe,video_analysis', 'WORKER_API_BASE': f"http://127.0.0.1:{config['ports']['api']}/internal/v1"})]
    save(state / 'processes.json', {})
    try:
        for name, command, child_env in services:
            with (state / f'{name}.log').open('ab') as output:
                process = subprocess.Popen([str(a) for a in command], cwd=ROOT, env=child_env, stdin=subprocess.DEVNULL,
                    stdout=output, stderr=output, creationflags=HIDDEN, close_fds=True)
            record = identity(process.pid)
            if not record: raise RuntimeError(name + ' exited on startup')
            if name == 'worker': record['job'] = job_name
            current[name] = record; save(state / 'processes.json', current)
        wait_ready(config)
        if not all(alive(r) for r in current.values()): raise RuntimeError('A team process exited; inspect local logs')
    except Exception:
        stop(config, state); raise
    print('Team website running: http://127.0.0.1:8088/#/sessions', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--data-root', type=Path, default=ROOT / 'var/team-web')
    sub = parser.add_subparsers(dest='command', required=True)
    setup = sub.add_parser('prepare')
    for name in ('go', 'pg-bin', 'node', 'python', 'models'): setup.add_argument('--' + name, required=True)
    setup.add_argument('--login-from')
    sub.add_parser('start'); sub.add_parser('start-detached'); sub.add_parser('stop'); sub.add_parser('status')
    sub.add_parser('startup-paths')
    worker = sub.add_parser('supervise-worker'); worker.add_argument('--job', required=True)
    args = parser.parse_args(); state = args.data_root.resolve(); state.mkdir(parents=True, exist_ok=True)
    if args.command == 'supervise-worker':
        from capture_worker_job import supervise
        return supervise(args.job, "import sys; sys.argv=['workers','run']; from workers.__main__ import main; main()")
    if args.command == 'prepare': prepare(args, state); return 0
    config = load(state / 'private.json')
    if args.command == 'startup-paths':
        # Only paths are printed for the native BAT wrapper; credentials stay private.
        for target in (Path(config['go']).parents[2], state):
            marker = target / '.team-web-alias-owner'
            if not marker.exists(): marker.write_text(secrets.token_hex(16), encoding='ascii')
            print(str(target), flush=True)
        return 0
    if args.command == 'start-detached':
        with (state / 'launcher.log').open('ab') as output:
            process = subprocess.Popen([config['python'], str(Path(__file__).resolve()), '--data-root', str(state), 'start'],
                cwd=ROOT, env={**os.environ, 'PYTHONUTF8': '1'}, stdin=subprocess.DEVNULL, stdout=output, stderr=output,
                creationflags=HIDDEN | getattr(subprocess, 'CREATE_BREAKAWAY_FROM_JOB', 0), close_fds=True)
        code = process.wait(timeout=55)
        if code: raise RuntimeError('Detached start failed; inspect launcher.log')
        wait_ready(config); print('Team website running: http://127.0.0.1:8088/#/sessions'); return 0
    if args.command == 'start': start(config, state)
    elif args.command == 'stop': stop(config, state); print('Team website stopped')
    else: print(json.dumps({name: alive(record) for name, record in records(state).items()}))
    return 0


if __name__ == '__main__':
    try: raise SystemExit(main())
    except Exception as error:
        print('Team website: ' + str(error), file=sys.stderr); raise SystemExit(1)
