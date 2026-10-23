"""Optional LAN capture gateway for the existing team-web database and accounts."""
import ipaddress
import os
from pathlib import Path
import secrets
import socket
import subprocess


def secure_private_storage(state):
    """Do not leave a CA signing key writable by other ordinary local users."""
    target=state/'caddy-storage';target.mkdir(exist_ok=True)
    if os.name!='nt':
        target.chmod(0o700);return
    script='''$ErrorActionPreference='Stop'
$path=$env:CANGJIE_CAPTURE_STORE
$acl=Get-Acl -LiteralPath $path
$owner=([Security.Principal.NTAccount]::new($acl.Owner)).Translate([Security.Principal.SecurityIdentifier]).Value
$current=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$allowed=@($owner,$current,'S-1-5-32-544','S-1-5-18')
$unsafe=@($acl.Access | Where-Object {$_.AccessControlType -eq 'Allow' -and $_.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value -notin $allowed})
if ($acl.AreAccessRulesProtected -and $unsafe.Count -eq 0) { exit 0 }
$acl.SetAccessRuleProtection($true,$false)
foreach($entry in @($acl.Access)) { $acl.RemoveAccessRuleAll($entry) }
foreach($sid in @($owner,$current,'S-1-5-32-544','S-1-5-18') | Select-Object -Unique) {
 $identity=[Security.Principal.SecurityIdentifier]::new($sid)
 $rule=[Security.AccessControl.FileSystemAccessRule]::new($identity,'FullControl','ContainerInherit,ObjectInherit','None','Allow')
 $acl.SetAccessRule($rule)
}
Set-Acl -LiteralPath $path -AclObject $acl
'''
    child_env={**os.environ,'CANGJIE_CAPTURE_STORE':str(target)}
    # A launcher started in PowerShell 7 can export its incompatible module path
    # to Windows PowerShell 5, preventing its built-in Security module loading.
    for name in list(child_env):
        if name.casefold()=='psmodulepath':del child_env[name]
    result=subprocess.run(['powershell.exe','-NoProfile','-NonInteractive','-Command',script],
        env=child_env,capture_output=True,
        creationflags=getattr(subprocess,'CREATE_NO_WINDOW',0))
    if result.returncode:raise RuntimeError('Could not restrict capture CA storage to deployment owner and administrators')


def configure(config, state, args):
    ip = ipaddress.IPv4Address(args.ip)
    if not ip.is_private or ip.is_loopback or ip.is_link_local:
        raise ValueError('Use the reachable private IPv4 address of this computer')
    with socket.socket() as probe:
        probe.bind((str(ip), 0))
    settings = dict(config.get('capture', {}))
    settings.update(ip=str(ip), https=8443, ca=8081, udp=8189, media=8889, control=9997)
    for name in ('caddy', 'mediamtx', 'ffmpeg', 'ffprobe'):
        settings[name] = str(Path(getattr(args, name)).resolve(strict=True))
    for name in ('control_token', 'event_token'):
        settings.setdefault(name, secrets.token_hex(32))
    config['capture'] = settings
    for name in ('capture-recordings', 'capture-public', 'caddy-storage'):
        (state / name).mkdir(exist_ok=True)
    secure_private_storage(state)


def origin(config):
    c = config['capture']
    return f"https://{c['ip']}:{c['https']}"


def check_address(config):
    try:
        with socket.socket() as probe:probe.bind((config['capture']['ip'],0))
    except OSError as error:
        raise RuntimeError('Capture LAN address changed; stop this deployment and enable-capture with the current Wi-Fi IPv4') from error


def environment(config, state):
    c = config['capture']
    return dict(PUBLIC_ORIGIN=origin(config), CAPTURE_ENABLED='true',
        CAPTURE_LIVE_VISION_ENABLED='true',
        CAPTURE_PUBLIC_URL=origin(config), CAPTURE_MEDIA_URL=origin(config) + '/media',
        CAPTURE_CONTROL_URL=f"http://127.0.0.1:{c['control']}",
        CAPTURE_CONTROL_TOKEN=c['control_token'], CAPTURE_EVENT_TOKEN=c['event_token'],
        CAPTURE_EVENT_URL=f"http://127.0.0.1:{config['ports']['api']}/internal/v1/streams/events",
        CAPTURE_RECORD_ROOT=str(state / 'capture-recordings'),
        CAPTURE_FFMPEG=c['ffmpeg'], CAPTURE_FFPROBE=c['ffprobe'])


def generate(config, state):
    from team_web import ROOT, save
    c, api = config['capture'], config['ports']['api']
    hook = '"' + (state / 'bin/capture-hook.exe').as_posix() + '"'
    save(state / 'mediamtx.yml', dict(logLevel='warn', authMethod='http',
        authHTTPAddress=f'http://127.0.0.1:{api}/internal/v1/streams/auth', authHTTPExclude=[],
        api=True, apiAddress=f"127.0.0.1:{c['control']}", rtsp=False, rtmp=False, hls=False, srt=False, playback=False,
        webrtc=True, webrtcAddress=f"127.0.0.1:{c['media']}",
        webrtcLocalUDPAddress=f"{c['ip']}:{c['udp']}", webrtcIPsFromInterfaces=False,
        webrtcAdditionalHosts=[c['ip']], pathDefaults=dict(source='publisher', overridePublisher=False,
            record=True, recordPath=(state / 'capture-recordings/%path/%Y-%m-%d_%H-%M-%S-%f').as_posix(),
            recordFormat='fmp4', recordPartDuration='1s', recordSegmentDuration='30s', recordMaxPartSize='50M',
            recordDeleteAfter='0s', runOnReady=hook + ' online', runOnNotReady=hook + ' offline',
            runOnRecordSegmentCreate=hook + ' segment_started', runOnRecordSegmentComplete=hook + ' segment_complete'),
        paths={'~^captures/[0-9a-f-]{36}/[0-9]+$': {}}))
    text = '''{
    admin off
    auto_https disable_redirects
    skip_install_trust
    storage file_system {
        root "%s"
    }
}
%s {
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
    handle /capture-ca.mobileconfig {
        root * "%s"
        header Content-Type application/x-apple-aspen-config
        file_server
    }
    handle / {
        root * "%s"
        rewrite * /phone-setup.html
        file_server
    }
    handle {
        respond 404
    }
}
http://127.0.0.1:%s {
    bind 127.0.0.1
    handle /health/* {
        reverse_proxy 127.0.0.1:%s
    }
    handle {
        redir %s{uri} 302
    }
}
''' % ((state/'caddy-storage').as_posix(), origin(config), c['ip'], api, api, c['media'],
       (ROOT/'frontend/dist').as_posix(), c['ip'],c['ca'],c['ip'],(state/'capture-public').as_posix(),
       (state/'capture-public').as_posix(),(state/'capture-public').as_posix(),
       config['ports']['web'],api,origin(config))
    (state/'Caddyfile').write_text(text, encoding='utf-8')


def publish_certificate(config, state):
    import hashlib
    import shutil
    import ssl
    import plistlib
    import uuid
    import time
    from team_web import save
    source = state/'caddy-storage/pki/authorities/local/root.crt'
    deadline = time.monotonic()+15
    while not source.exists() and time.monotonic()<deadline:
        time.sleep(.2)
    pem = source.read_text(encoding='ascii')
    certificate = ssl.PEM_cert_to_DER_cert(pem)
    shutil.copyfile(source,state/'capture-public/capture-ca.crt')
    fingerprint=hashlib.sha256(certificate).hexdigest()
    payload=dict(PayloadContent=certificate,PayloadCertificateFileName='capture-ca.crt',PayloadType='com.apple.security.root',
        PayloadVersion=1,PayloadIdentifier='cn.cangjie.capture.ca.'+fingerprint[:16],PayloadUUID=str(uuid.uuid4()),
        PayloadDisplayName='观课局域网采集证书',PayloadDescription='仅安装此电脑的公开采集根证书，不包含账号、密钥或设备管理。')
    profile=dict(PayloadContent=[payload],PayloadDisplayName='观课手机采集',PayloadType='Configuration',PayloadVersion=1,
        PayloadIdentifier='cn.cangjie.capture.'+fingerprint[:16],PayloadUUID=str(uuid.uuid4()),PayloadRemovalDisallowed=False,
        PayloadDescription='用于信任同一 Wi-Fi 内这台观课电脑的 HTTPS 入口。无需使用后可在手机设置中移除。')
    (state/'capture-public/capture-ca.mobileconfig').write_bytes(plistlib.dumps(profile))
    page='''<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>观课 · 手机连接</title><style>body{font:16px/1.7 system-ui;background:#f4f7f7;color:#183b3e;margin:0;padding:24px}main{max-width:680px;margin:auto}section{background:white;border-radius:16px;padding:22px;margin:18px 0}a{color:#087f76}button,.button{display:inline-block;background:#087f76;color:white;padding:10px 18px;border-radius:8px;text-decoration:none}code{word-break:break-all;font-size:12px}h1{font-size:28px}h2{font-size:20px}</style><main>
<h1>用手机记录课堂</h1><p>手机与电脑连接同一个 Wi-Fi。首次连接只需安装一次这台电脑的采集证书。</p>
<section><h2>安卓手机</h2><a class="button" href="/capture-ca.crt">下载安卓证书</a><ol><li>在手机设置搜索“安装证书”，选择“CA 证书”，安装刚下载的文件。</li><li>返回浏览器，打开下面的设备检查页；不能出现证书错误。</li></ol></section>
<section><h2>iPhone</h2><a class="button" href="/capture-ca.mobileconfig">下载 iPhone 证书</a><ol><li>请使用 Safari。下载后打开“设置 → 已下载描述文件”，安装“观课手机采集”。</li><li>进入“设置 → 通用 → 关于本机 → 证书信任设置”，为本次安装的 Caddy 根证书开启完全信任。</li></ol></section>
<section><h2>检查设备并开始</h2><p><a class="button" href="%s/capture-check.html">检查摄像头和麦克风</a></p><p>检查通过后，在电脑的课堂详情打开“实时课堂”，创建采集并生成手机链接。手机打开该链接、点击开始。请保持前台、亮屏和供电。</p><p>电脑点击“开始实时观看”，勾选“直播显示举手与低头框”。结束时明确点击停止，等待录像就绪。</p></section>
<section><h2>核对证书</h2><p>仅信任你自己的这台电脑。通过电脑或 USB 核对 SHA-256 指纹：</p><code>%s</code><p>证书匹配地址：%s。停止使用后可在手机设置中移除该证书。</p></section></main></html>''' % (origin(config),fingerprint,origin(config))
    (state/'capture-public/phone-setup.html').write_text(page,encoding='utf-8')
    save(state/'capture-handoff.json', dict(url=origin(config)+'/#/sessions',
        check_url=origin(config)+'/capture-check.html',
        ca_download=f"http://{config['capture']['ip']}:{config['capture']['ca']}/capture-ca.crt",
        ca_sha256=fingerprint, phone_setup=f"http://{config['capture']['ip']}:{config['capture']['ca']}/", android='pending', iphone='pending'))
