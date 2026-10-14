from dataclasses import replace
import json
from pathlib import Path
from uuid import uuid4

import httpx
import pytest

from workers.common.contracts import LeaseLost, WorkerError
from workers.common.deletion import check_fence, clean_claim, exclusive_root
from workers.common.runner import Runner, execute_claim
from workers.common.lifecycle import Guard, scratch_path
from workers.common.transport import Transport
from .fake_go import make_claim


@pytest.fixture
def configured(settings):
    return replace(settings, cleanup_enabled=True,
                   cleanup_contract_path=Path(__file__).parent / 'fixtures/cleanup.schema.json')


@pytest.fixture
def deletion():
    return {'deletion_request_id': str(uuid4()), 'session_id': str(uuid4()),
            'storage_generation': 2, 'job_ids': [str(uuid4()), str(uuid4())]}


def leftovers(root, job):
    directory = root / job / str(uuid4())
    directory.mkdir(parents=True)
    (directory / 'input.bin').write_bytes(b'synthetic private bytes')
    (directory / 'lease.deadline').write_text('9999999999')
    return directory


def mock_transport(configured, deletion, handler=None):
    calls = []
    def handle(request):
        calls.append(request)
        assert request.headers['Authorization'] == 'Bearer test-token'
        assert 'X-Lease-Token' not in request.headers
        if handler:
            value = handler(request)
            if value is not None:
                return value
        if request.url.path.endswith('claim-cleanup'):
            assert json.loads(request.content) == {'worker_id': configured.worker_id}
            return httpx.Response(200, json={'data': deletion})
        assert request.url.path.endswith('/' + deletion['deletion_request_id'] + '/cleanup-complete')
        assert json.loads(request.content) == {'storage_generation': deletion['storage_generation']}
        return httpx.Response(200, json={'data': {
            'deletion_request_id': deletion['deletion_request_id'],
            'storage_generation': deletion['storage_generation'], 'worker_id': configured.worker_id}})
    return Transport(configured, httpx.Client(transport=httpx.MockTransport(handle))), calls


def test_cleanup_removes_all_leases_only_for_server_jobs(configured, deletion):
    for job in deletion['job_ids']:
        leftovers(configured.temp_root, job)
        leftovers(configured.temp_root, job)
    keep = leftovers(configured.temp_root, str(uuid4()))
    runner = Runner(configured)
    runner.transport.close()
    runner.transport, calls = mock_transport(configured, deletion)
    receipt = runner.cleanup_once()
    assert receipt['storage_generation'] == 2
    assert keep.exists()
    assert all(not (configured.temp_root / job).exists() for job in deletion['job_ids'])
    assert len(calls) == 2
    # Safe replay following a restart does not require the deleted paths to exist.
    assert runner.cleanup_once() == receipt
    runner.transport.close()


@pytest.mark.parametrize('field,value', [
    ('job_ids', ['../outside']), ('job_ids', [str(uuid4()), str(uuid4()), '../x']),
    ('storage_generation', True), ('storage_generation', 0),
    ('session_id', '../../outside'), ('deletion_request_id', 'invalid'),
    ('unknown_field', 'extra'),
])
def test_bad_claim_never_changes_files_or_acknowledges(configured, deletion, field, value):
    keep = leftovers(configured.temp_root, deletion['job_ids'][0])
    deletion[field] = value
    runner = Runner(configured)
    runner.transport.close()
    runner.transport, calls = mock_transport(configured, deletion)
    with pytest.raises(WorkerError, match='协议'):
        runner.cleanup_once()
    assert keep.exists() and len(calls) == 1
    runner.transport.close()


def test_delete_failure_never_sends_receipt(configured, deletion, monkeypatch):
    leftovers(configured.temp_root, deletion['job_ids'][0])
    runner = Runner(configured)
    runner.transport.close()
    runner.transport, calls = mock_transport(configured, deletion)
    def denied(_):
        raise PermissionError('in-use file')
    monkeypatch.setattr('workers.common.deletion.shutil.rmtree', denied)
    with pytest.raises(WorkerError, match='不回报成功'):
        runner.cleanup_once()
    assert len(calls) == 1
    runner.transport.close()


def test_unexpected_job_content_is_not_deleted(configured, deletion):
    path = configured.temp_root / deletion['job_ids'][0]
    path.mkdir(parents=True)
    (path / 'foreign.txt').write_text('keep')
    with exclusive_root(configured.temp_root):
        with pytest.raises(WorkerError):
            clean_claim(configured.temp_root, deletion)
    assert (path / 'foreign.txt').exists()


def test_links_to_other_storage_block_receipt(configured, deletion, tmp_path):
    outside = tmp_path / 'outside'
    outside.mkdir()
    (outside / 'keep').write_text('keep')
    configured.temp_root.mkdir()
    link = configured.temp_root / deletion['job_ids'][0]
    try:
        link.symlink_to(outside, target_is_directory=True)
    except OSError:
        # Windows without symlink privilege can create a directory junction.
        import subprocess
        subprocess.run(['cmd', '/c', 'mklink', '/J', str(link), str(outside)],
                       check=True, capture_output=True)
    try:
        with exclusive_root(configured.temp_root):
            with pytest.raises(WorkerError, match='链接'):
                clean_claim(configured.temp_root, deletion)
        assert (outside / 'keep').read_text() == 'keep'
    finally:
        if link.is_symlink():
            link.unlink()
        else:
            link.rmdir()  # Remove the verified test junction itself, not its target.


def test_root_lock_blocks_a_second_runner_before_http(configured, deletion):
    runner = Runner(configured)
    runner.transport.close()
    runner.transport, calls = mock_transport(configured, deletion)
    with exclusive_root(configured.temp_root):
        with pytest.raises(WorkerError, match='已有 Worker'):
            runner.cleanup_once()
    assert calls == []
    runner.transport.close()


def test_persisted_fence_stops_old_claim_before_download(configured, deletion, source):
    with exclusive_root(configured.temp_root):
        clean_claim(configured.temp_root, deletion)
    claim = make_claim(configured, source)
    claim['session_id'] = deletion['session_id']
    claim['storage_generation'] = 2
    runner = Runner(configured)
    assert runner.run_claim(claim) == 'stopped'
    assert not scratch_path(configured.temp_root, claim).exists()
    runner.transport.close()
    claim['storage_generation'] = 3
    check_fence(configured.temp_root, claim)


@pytest.mark.parametrize('status', [429, 503])
def test_cleanup_confirmation_retries_identical_bytes(configured, deletion, status):
    attempts = []
    def first_failure(request):
        if request.url.path.endswith('cleanup-complete'):
            attempts.append(request.content)
            if len(attempts) == 1:
                return httpx.Response(status, headers={'Retry-After': '0'})
    transport, _ = mock_transport(configured, deletion, first_failure)
    transport.complete_cleanup(deletion)
    assert len(attempts) == 2 and attempts[0] == attempts[1]
    transport.close()


@pytest.mark.parametrize('status', [401, 403, 409, 410])
def test_cleanup_rejected_confirmation_is_not_retried(configured, deletion, status):
    def failure(request):
        if request.url.path.endswith('cleanup-complete'):
            return httpx.Response(status)
    transport, calls = mock_transport(configured, deletion, failure)
    with pytest.raises(LeaseLost):
        transport.complete_cleanup(deletion)
    assert len(calls) == 1
    transport.close()


def test_confirmation_must_match_worker_and_generation(configured, deletion):
    def mismatch(request):
        return httpx.Response(200, json={'data': {
            'deletion_request_id': deletion['deletion_request_id'],
            'storage_generation': 99, 'worker_id': 'another-worker'}})
    transport, _ = mock_transport(configured, deletion, mismatch)
    with pytest.raises(WorkerError, match='当前 Worker'):
        transport.complete_cleanup(deletion)
    transport.close()


def test_polling_services_cleanup_before_normal_job_claim(configured, deletion, monkeypatch):
    runner = Runner(configured)
    order = []
    monkeypatch.setattr(runner, 'cleanup_once', lambda: order.append('cleanup'))
    monkeypatch.setattr(runner.transport, 'claim', lambda: order.append('claim'))
    runner.run(once=True)
    assert order == ['cleanup', 'claim']


def test_enabling_cleanup_requires_backend_contract(settings, tmp_path):
    # Keep testing an old deployment explicitly: the integrated repository now
    # contains the backend cleanup DTOs and is a valid full contract.
    schema = json.loads((Path(__file__).parent / 'fixtures/cleanup.schema.json').read_text(encoding='utf-8'))
    schema['$defs'].pop('WorkerCleanupReceipt')
    old_contract = tmp_path / 'legacy.schema.json'
    old_contract.write_text(json.dumps(schema), encoding='utf-8')
    with pytest.raises(WorkerError, match='完整协议'):
        replace(settings, cleanup_enabled=True, cleanup_contract_path=old_contract)


def test_legacy_deployment_does_not_poll_cleanup(settings, monkeypatch):
    runner = Runner(settings)
    monkeypatch.setattr(runner.transport, 'claim', lambda: None)
    monkeypatch.setattr(runner.transport, 'claim_cleanup', lambda: pytest.fail('unexpected cleanup RPC'))
    runner.run(once=True)


def test_network_loss_retries_confirmation_without_changing_payload(configured, deletion):
    attempts = []
    def disconnected(request):
        if request.url.path.endswith('cleanup-complete'):
            attempts.append(request.content)
            if len(attempts) == 1:
                raise httpx.ReadError('lost acknowledgement')
    transport, _ = mock_transport(configured, deletion, disconnected)
    transport.complete_cleanup(deletion)
    assert len(attempts) == 2 and attempts[0] == attempts[1]
    transport.close()


def test_damaged_fence_blocks_old_job(configured, deletion, source):
    with exclusive_root(configured.temp_root):
        clean_claim(configured.temp_root, deletion)
    path = configured.temp_root / '.deletion-fences' / (deletion['session_id'] + '.json')
    path.write_text('{broken')
    claim = make_claim(configured, source)
    claim['session_id'] = deletion['session_id']
    with pytest.raises(WorkerError, match='删除记录损坏'):
        check_fence(configured.temp_root, claim)


def test_continuous_poll_retries_failed_cleanup_before_claim(configured, monkeypatch):
    runner = Runner(configured)
    calls = []
    def clean():
        calls.append('cleanup')
        if len(calls) == 1:
            raise WorkerError('CLEANUP_INCOMPLETE', 'busy', True)
    def claim():
        calls.append('claim')
        raise LeaseLost('stop probe')
    monkeypatch.setattr(runner, 'cleanup_once', clean)
    monkeypatch.setattr(runner.transport, 'claim', claim)
    monkeypatch.setattr('workers.common.runner.time.sleep', lambda _: None)
    with pytest.raises(LeaseLost):
        runner.run()
    assert calls == ['cleanup', 'cleanup', 'claim']
