import tempfile
import json
import os
import subprocess
import sys
from pathlib import Path
import pytest
from setup_models import pricing, source_env, write_private


def test_budget_is_explicit_and_prices_keep_their_actual_units():
    for bad in ('', '0', '-1', '1e3', '1.0000001', ' 1'):
        with pytest.raises(ValueError): pricing(bad)
    chosen = pricing('1')
    assert chosen['monthly_budget_cny'] == '1'
    assert chosen['asr_billing_mode'] == 'seconds'
    assert chosen['asr_cny_per_second'] == '0.00022'
    assert chosen['asr_input_cny_per_million_tokens'] == ''


def test_key_source_rejects_interpolation_and_line_injection():
    for key in ('', 'sk-${HOME}-secret', 'sk-one\nOTHER=secret', 'sk-`secret`', 'sk-$(secret)'):
        with pytest.raises(ValueError): source_env(key)
    assert 'synthetic-key-12345' in source_env('synthetic-key-12345')


def test_workspace_key_with_dots_is_preserved_as_data():
    key = 'sk-ws-SYNTHETIC.Part_1.Part-2'
    assert source_env(key).splitlines()[0] == 'DASHSCOPE_API_KEY=' + key
    for suffix in ('\nOTHER=value', '\r', ' ', '"', '${HOME}', '`value`'):
        with pytest.raises(ValueError) as failure:
            source_env(key + suffix)
        assert key not in str(failure.value)


def test_private_files_do_not_overwrite_existing_configuration():
    with tempfile.TemporaryDirectory() as folder:
        path = Path(folder)/'config.private.json'
        write_private(path, 'synthetic')
        with pytest.raises(FileExistsError): write_private(path, 'replacement')
        assert path.read_text() == 'synthetic'


@pytest.mark.skipif(os.name != 'nt', reason='Native Windows launcher regression')
@pytest.mark.parametrize('failure', ['none', 'missing_python', 'invalid_config'])
def test_native_launcher_keeps_failures_visible_and_handles_unicode_paths(tmp_path, failure):
    root = tmp_path / '中文 model setup'
    tools = root / 'tools'
    state = root / 'var' / 'team-web'
    tools.mkdir(parents=True)
    state.mkdir(parents=True)
    repo = Path(__file__).resolve().parents[1]
    for source, destination in [(repo / '配置语音与报告.bat', root / '配置语音与报告.bat'),
                                (repo / 'tools/setup_models.bat', tools / 'setup_models.bat')]:
        # Git eol attributes guarantee this for a Windows checkout; retain the
        # actual bytes here to catch accidental rewriting by local generators.
        raw = source.read_bytes()
        assert b'\n' not in raw.replace(b'\r\n', b'')
        destination.write_bytes(raw)
    (tools / 'setup_models.py').write_text('print("MODEL_SETUP_REACHED")\nraise SystemExit(7)\n', encoding='utf-8')
    python = str(root / 'missing.exe') if failure == 'missing_python' else sys.executable
    (state / 'private.json').write_text('{invalid' if failure == 'invalid_config' else json.dumps({'python': python}), encoding='utf-8')
    env = dict(os.environ)
    env.pop('TEAM_PYTHON', None)
    env.pop('TEAM_MODEL_SETUP_NO_PAUSE', None)
    launcher = root / '配置语音与报告.bat'
    # /s /c needs one outer quote pair around a quoted executable and arguments.
    command = f'"{os.environ.get("COMSPEC", "cmd.exe")}" /d /s /c ""{launcher}""'
    result = subprocess.run(command, cwd=os.environ.get('SystemRoot', 'C:/Windows'), input=b'\r\n',
                            capture_output=True, env=env, timeout=20)
    output = (result.stdout + result.stderr).decode('utf-8', errors='replace')
    assert 'You can close this window' in output
    assert 'not recognized' not in output
    if failure == 'none':
        assert result.returncode == 7
        assert 'MODEL_SETUP_REACHED' in output
    else:
        assert result.returncode == 1
        assert 'MODEL_SETUP_REACHED' not in output
        assert ('Python runtime was not found' if failure == 'missing_python' else 'configured Python path') in output
