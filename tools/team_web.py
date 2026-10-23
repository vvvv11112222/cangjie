"""Local team website: official Go API, React build and selected vision Worker.

Private state stays in the chosen project data directory. Never resets a database,
changes migration history, installs drivers or terminates processes by name.
"""
import argparse
import hashlib
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
    # Reuse an existing mapping before considering any free drive. A gap (e.g.
    # free W: with this directory already on Y:) must not trigger a new subst.
    for letter in ('V', 'W', 'Y', 'Z'):
        drive = Path(letter + ':/')
        if drive.exists() and os.path.samefile(drive, state): return drive
    for letter in ('V', 'W', 'Y', 'Z'):
        drive = Path(letter + ':/')
        if drive.exists(): continue
        result = subprocess.run(['subst.exe', letter + ':', str(state)], capture_output=True, creationflags=HIDDEN)
        if result.returncode == 0: return drive
        break  # A restricted logon may deny DOS-device mappings for every letter.
    return junction_root(state)


def junction_root(state):
    """ASCII directory alias when Windows denies SUBST; never move database data."""
    local = Path(os.environ.get('LOCALAPPDATA', ''))
    if not local.is_absolute() or not str(local).isascii():
        raise RuntimeError('No ASCII local application directory for database path compatibility')
    digest = hashlib.sha256(os.path.normcase(str(state.resolve())).encode('utf-8')).hexdigest()[:24]
    link = local / 'Cangjie/runtime-links' / digest
    link.parent.mkdir(parents=True, exist_ok=True)
    if link.exists():
        if os.path.samefile(link, state): return link
        raise RuntimeError('Database directory alias belongs to another directory; it was preserved')
    result = subprocess.run(['powershell.exe', '-NoProfile', '-NonInteractive', '-Command',
        'New-Item -ItemType Junction -Path $env:CANGJIE_LINK -Target $env:CANGJIE_TARGET -ErrorAction Stop | Out-Null'],
        env={**os.environ, 'CANGJIE_LINK':str(link), 'CANGJIE_TARGET':str(state.resolve())},
        capture_output=True, creationflags=HIDDEN)
    if result.returncode or not link.exists() or not os.path.samefile(link, state):
        raise RuntimeError('Could not create local database directory alias; existing data was preserved')
    return link


def environment(config, state):
    env = dict(os.environ)
    # Ignore another terminal/project's stale adapter ordinal or acceleration flags.
    for name in list(env):
        if name.startswith('VISION_'): del env[name]
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
        VISION_MODEL_ROOT=config['models'], WORKER_CONCURRENCY='1', WORKER_CLEANUP_ENABLED='1',
        MODEL_MONTHLY_BUDGET='0', REPORT_API_KEY='', CAPTURE_ENABLED='false', ASR_DEVICE='cpu',
        MODEL_SETTINGS_DIR=str(state / 'model-settings'), ASR_PLANNER=str(state / 'bin/audio-plan.exe'), ASR_MODEL_FROM_CLAIM='1',
        BOOTSTRAP_TEST_PASSWORD=secret['login'], VITE_DATA_SOURCE='api', TEAM_API_TARGET=f"http://127.0.0.1:{ports['api']}",
        GOMAXPROCS='2', GOTOOLCHAIN='local', GOTELEMETRY='off', PYTHONUTF8='1', PYTHONUNBUFFERED='1')
    from vision_runtime import execution_environment
    env.update(execution_environment(config.get('vision_execution', {'engine':'cpu','batch_size':1}), ROOT))
    for name in list(env):
        if name.startswith('MTX_'): del env[name]
    if config.get('capture'):
        from team_capture import environment as capture_environment
        env.update(capture_environment(config, state))
    # Optional private configuration enables the unified audio/report flow.
    # Keep the visual-only default free of external calls and credentials.
    if config.get('analysis_config'):
        settings = load(Path(config['analysis_config']))
        if not isinstance(settings, dict) or any(not isinstance(k, str) or not k.startswith(('ASR_', 'REPORT_', 'MODEL_')) or not isinstance(v, str) for k,v in settings.items()):
            raise RuntimeError('Analysis config must contain only string ASR_/REPORT_/MODEL_ settings')
        env.update(settings)
        if env.get('ASR_CLOUD_ENABLED') != 'true' or not env.get('ASR_API_KEY'):
            raise RuntimeError('Analysis config requires explicit cloud ASR enablement and credentials')
        env['ASR_PLANNER'] = str(state / 'bin/audio-plan.exe')
    runtime_root = ascii_root(state)
    temp = runtime_root / 'tmp'; temp.mkdir(exist_ok=True)
    env.update(GOCACHE=str(runtime_root / 'go-cache'), GOMODCACHE=str(runtime_root / 'go-mod'), GOTMPDIR=str(temp), TMP=str(temp), TEMP=str(temp))
    env['PATH'] = str(Path(config['node']).parent) + os.pathsep + env.get('PATH', '')
    return env


def select_vision(config, state):
    mode = config.get('vision_runtime', 'directml')
    if mode not in ('cpu', 'auto', 'directml'): raise ValueError('Unknown vision runtime')
    result = {'engine':'cpu', 'batch_size':1, 'device':None, 'probe_passed':False,
              'reason':'明确选择 CPU 模式' if mode == 'cpu' else None}
    if mode != 'cpu':
        command = [config['python'], str(ROOT / 'tools/vision_runtime.py'), '--models',config['models'],
                   '--batch-size',str(config.get('vision_batch_size',16))]
        if config.get('accelerator_root'): command += ['--accelerator-root',config['accelerator_root']]
        try:
            probe = subprocess.run(command, cwd=ROOT, capture_output=True, text=True, encoding='utf-8',
                                   env={**os.environ,'PYTHONUTF8':'1'}, creationflags=HIDDEN, timeout=45)
            with (state/'runtime-probe.log').open('a',encoding='utf-8') as output:
                output.write(probe.stdout + probe.stderr)
            details = json.loads(probe.stdout.strip().splitlines()[-1]) if probe.stdout.strip() else {}
            if probe.returncode or details.get('engine') != 'directml' or not details.get('probe_passed'):
                raise RuntimeError(details.get('error','独显检查未完成，请查看 runtime-probe.log'))
            result = details
        except (OSError, subprocess.TimeoutExpired, RuntimeError, ValueError) as error:
            if mode == 'directml': raise RuntimeError('要求独显但检查失败：'+str(error)) from error
            result['reason'] = '独显不可用，已使用 CPU：'+str(error)
    result['requested'] = mode
    config['vision_execution'] = result
    save(state/'vision-runtime.json', result)
    save(state/'private.json', config)
    describe_vision(result)
    return result


def describe_vision(result):
    label = result['device']['name'] if result['device'] else 'CPU'
    message = f"视觉计算设备：{label}；每批 {result['batch_size']} 人。" + (result.get('reason') or '')
    # Ordinary Windows consoles/CI may use cp1252 instead of UTF-8. Reporting a
    # valid device must not abort startup just because the console lacks Chinese.
    encoding = getattr(sys.stdout, 'encoding', None) or 'utf-8'
    print(message.encode(encoding, errors='backslashreplace').decode(encoding),flush=True)


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


def website_url(config):
    if config.get('capture'):
        from team_capture import origin
        return origin(config)+'/#/sessions'
    return f"http://127.0.0.1:{config['ports']['web']}/#/sessions"


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
        if config.get('capture') and sql(config,state,"SELECT count(*) FROM teaching.live_captures WHERE state IN ('waiting','recording','finalizing')") != '0':
            raise RuntimeError('Phone capture is active; stop the recording in the page and wait before stopping')
    for name in ('worker', 'web', 'mediamtx', 'api'):
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
    analysis_config = args.analysis_config or prior.get('analysis_config')
    if prior.get('capture'): config['capture'] = prior['capture']
    if analysis_config: config['analysis_config'] = str(Path(analysis_config).resolve(strict=True))
    config['vision_runtime'] = args.vision_runtime or prior.get('vision_runtime','directml')
    config['vision_batch_size'] = args.vision_batch_size or prior.get('vision_batch_size',16)
    accelerator_root = args.accelerator_root or prior.get('accelerator_root')
    if accelerator_root: config['accelerator_root'] = str(Path(accelerator_root).resolve(strict=True))
    if not (ROOT / 'frontend/node_modules/vite/bin/vite.js').exists(): raise RuntimeError('Install frontend locked dependencies with npm ci first')
    for name in ('media', 'worker-tmp', 'ledger', 'bin'): (state / name).mkdir(exist_ok=True)
    env = {**os.environ, 'VISION_RUNTIME': 'cpu', 'PYTHONUTF8': '1'}
    config['runtime'] = json.loads(subprocess.check_output([config['python'], '-m', 'workers', 'runtime-info'], cwd=ROOT, env=env, creationflags=HIDDEN))
    config['code_sha'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    config['source_dirty'] = bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True).strip())
    select_vision(config, state); env = environment(config, state)
    for name in ('api', 'migrate', 'seed-dev', 'init-deletion-ledger', 'model-config', 'capture-hook'):
        run([config['go'], 'build', '-o', state / f'bin/{name}.exe', f'./cmd/{name}'], env, state)
    run([config['go'], 'build', '-o', state / 'bin/audio-plan.exe', '.'], env, state, cwd=ROOT / 'demos/ai-language-analysis')
    run([config['node'], ROOT / 'frontend/node_modules/vite/bin/vite.js', 'build'], env, state, cwd=ROOT / 'frontend')
    database_start(config, state)
    if sql(config, state, "SELECT count(*) FROM pg_database WHERE datname='cangjie_visual'", database='postgres') == '0':
        run([pg_tools(config) / 'createdb.exe', '-h', '127.0.0.1', '-p', str(config['ports']['database']), '-U', 'team_web', 'cangjie_visual'], env, state)
    run([state / 'bin/migrate.exe'], env, state)
    run([state / 'bin/init-deletion-ledger.exe'], env, state)
    run([state / 'bin/seed-dev.exe'], env, state)
    seed = (ROOT / 'deploy/team-web/development.sql').read_text(encoding='utf-8')
    sql(config, state, seed)
    receipt = dict(url=website_url(config), code_sha=config['code_sha'], source_dirty=config['source_dirty'],
                   processor_version=config['runtime']['processor_version'], visual_model=config['runtime']['visual_model'],
                   execution=config['vision_execution']['engine'], vision_execution=config['vision_execution'])
    save(state / 'deployment.json', receipt)
    (state / '使用说明.txt').write_text('团队网站：http://127.0.0.1:8088/#/sessions\n账号：admin（或 teacher_demo）\n密码：' + secret['login'] + '\n\n点击“上传录像并分析”，选择课堂与文件，在上传区域选择或登记来源；核验后再选择分析方式。\n“举手与低头识别”使用本地模型；“语音转写与教学报告”需要配置云端模型、预算，并获准云端音频和文本处理。\n页面会显示服务未就绪原因，上传成功不代表已开始分析。报告需要人工复核后发布。\n设备见vision-runtime.json和启动提示，每次一个分析任务；云端流程须通过私有配置显式启用。旧试验库保留，正式版不连接旧试验库。\n', encoding='utf-8')
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


def seed_demo(config, state):
    # The team-web launcher owns a loopback-only, development database. This
    # explicit command is never part of prepare/start or a production migration.
    if config.get('ports') != {'web':8088,'api':18088,'database':55437}:
        raise RuntimeError('Demo seed requires the dedicated local team-web deployment')
    sql(config, state, (ROOT / 'deploy/team-web/demo-classrooms.sql').read_text(encoding='utf-8'))
    print('50 marked demo classrooms ready; no media permissions, analysis or model calls were created')


def start(config, state):
    from capture_dev import identity
    if config.get('capture'):
        from team_capture import check_address
        check_address(config)
    current = records(state)
    if any(alive(r) for r in current.values()):
        expected = {'api','web','worker','mediamtx'} if config.get('capture') else {'api','web','worker'}
        if set(current) != expected or not all(alive(r) for r in current.values()):
            raise RuntimeError('Deployment is only partly running; stop the owned deployment before restarting')
        wait_ready(config, 5); print('Team website already running: '+website_url(config)); return
    for name in ('web', 'api'): check_port(config['ports'][name])
    select_vision(config, state)  # Re-enumerate on every start; never copy this PC's GPU ordinal to another PC.
    database_start(config, state)
    env = environment(config, state)
    job_name = 'Local\\CangjieTeamVision-' + secrets.token_hex(16)
    worker_env = {k:v for k,v in env.items() if k not in ('ASR_API_KEY', 'REPORT_API_KEY')}
    worker_env.update(WORKER_CAPABILITIES='probe,audio_analysis,video_analysis',
                      WORKER_API_BASE=f"http://127.0.0.1:{config['ports']['api']}/internal/v1")
    services = [
        ('api', [state / 'bin/api.exe'], env),
        ('web', [config['node'], ROOT / 'frontend/node_modules/vite/bin/vite.js', 'preview', ROOT / 'frontend', '--host', '127.0.0.1', '--port', str(config['ports']['web']), '--strictPort'], env),
        ('worker', [config['python'], Path(__file__).resolve(), '--data-root', state, 'supervise-worker', '--job', job_name],
         worker_env)]
    if config.get('capture'):
        from team_capture import generate,secure_private_storage
        secure_private_storage(state)
        generate(config,state)
        for port in ('https','ca','media','control'): check_port(config['capture'][port])
        services[1] = ('web',[config['capture']['caddy'],'run','--config',state/'Caddyfile','--adapter','caddyfile'],env)
        services.insert(1,('mediamtx',[config['capture']['mediamtx'],state/'mediamtx.yml'],env))
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
        if config.get('capture'):
            from team_capture import publish_certificate
            publish_certificate(config,state)
        if not all(alive(r) for r in current.values()): raise RuntimeError('A team process exited; inspect local logs')
    except Exception:
        stop(config, state); raise
    print('Team website running: '+website_url(config), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--data-root', type=Path, default=ROOT / 'var/team-web')
    sub = parser.add_subparsers(dest='command', required=True)
    setup = sub.add_parser('prepare')
    for name in ('go', 'pg-bin', 'node', 'python', 'models'): setup.add_argument('--' + name, required=True)
    setup.add_argument('--login-from')
    setup.add_argument('--analysis-config', help='Private JSON with ASR/REPORT/MODEL settings; enables unified analysis')
    setup.add_argument('--vision-runtime', choices=('auto','directml','cpu'), help='Default: require verified discrete GPU; auto/CPU are explicit diagnostic choices')
    setup.add_argument('--accelerator-root', help='Existing accelerator folder containing directml/onnxruntime; no packages or drivers installed automatically')
    setup.add_argument('--vision-batch-size',type=int,choices=(1,8,16))
    sub.add_parser('start'); sub.add_parser('start-detached'); sub.add_parser('stop'); sub.add_parser('status')
    sub.add_parser('startup-paths')
    sub.add_parser('seed-demo', help='Explicitly add 50 fictional classrooms to this local development database; no analysis')
    capture = sub.add_parser('enable-capture', help='Use trusted LAN HTTPS with the existing database and accounts; stop first')
    for name in ('ip','caddy','mediamtx','ffmpeg','ffprobe'): capture.add_argument('--'+name,required=True)
    worker = sub.add_parser('supervise-worker'); worker.add_argument('--job', required=True)
    args = parser.parse_args(); state = args.data_root.resolve(); state.mkdir(parents=True, exist_ok=True)
    if args.command == 'supervise-worker':
        from capture_worker_job import supervise
        return supervise(args.job, "import sys; sys.argv=['workers','run']; from workers.__main__ import main; main()")
    if args.command == 'prepare': prepare(args, state); return 0
    config = load(state / 'private.json')
    if args.command == 'enable-capture':
        if any(alive(r) for r in records(state).values()): raise RuntimeError('Stop this deployment before configuring capture')
        from team_capture import configure,generate
        configure(config,state,args); generate(config,state)
        config['code_sha']=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
        config['source_dirty']=bool(subprocess.check_output(['git','status','--porcelain'],cwd=ROOT,text=True).strip())
        env=environment(config,state)
        for name in ('api','capture-hook'):
            run([config['go'],'build','-o',state/f'bin/{name}.exe',f'./cmd/{name}'],env,state)
        run([config['node'],ROOT/'frontend/node_modules/vite/bin/vite.js','build'],env,state,cwd=ROOT/'frontend')
        save(state/'private.json',config)
        save(state/'deployment.json',dict(url=website_url(config),code_sha=config['code_sha'],source_dirty=config['source_dirty'],
            processor_version=config['runtime']['processor_version'],visual_model=config['runtime']['visual_model'],
            execution=config['vision_execution']['engine'],vision_execution=config['vision_execution']))
        print('LAN phone capture configured; start this deployment, then trust its public CA on each device.'); return 0
    if args.command == 'startup-paths':
        # Only paths are printed for the native BAT wrapper; credentials stay private.
        for target in (state,):
            marker = target / '.team-web-alias-owner'
            if not marker.exists(): marker.write_text(secrets.token_hex(16), encoding='ascii')
            print(str(ascii_root(target)), flush=True)
        return 0
    if args.command == 'start-detached':
        with (state / 'launcher.log').open('ab') as output:
            process = subprocess.Popen([config['python'], str(Path(__file__).resolve()), '--data-root', str(state), 'start'],
                cwd=ROOT, env={**os.environ, 'PYTHONUTF8': '1'}, stdin=subprocess.DEVNULL, stdout=output, stderr=output,
                creationflags=HIDDEN | getattr(subprocess, 'CREATE_BREAKAWAY_FROM_JOB', 0), close_fds=True)
        code = process.wait(timeout=55)
        if code: raise RuntimeError('Detached start failed; inspect launcher.log')
        wait_ready(config)
        if (state/'vision-runtime.json').exists(): describe_vision(load(state/'vision-runtime.json'))
        print('Team website running: '+website_url(config)); return 0
    if args.command == 'start': start(config, state)
    elif args.command == 'stop': stop(config, state); print('Team website stopped')
    elif args.command == 'seed-demo': seed_demo(config, state)
    else: print(json.dumps({name: alive(record) for name, record in records(state).items()}))
    return 0


if __name__ == '__main__':
    try: raise SystemExit(main())
    except Exception as error:
        print('Team website: ' + str(error), file=sys.stderr); raise SystemExit(1)
