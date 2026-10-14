"""Worker-owned cleanup only; Go controls deletion and storage generations."""
from contextlib import contextmanager
import json
import os
from pathlib import Path
import shutil
import stat
import tempfile
from uuid import UUID

from jsonschema import Draft202012Validator, FormatChecker

from .contracts import LeaseLost, WorkerError


def plain_path(path):
    """Reject symlinks and Windows junction/reparse points, including dangling links."""
    path = Path(path)
    try:
        info = path.lstat()
    except FileNotFoundError:
        return
    if stat.S_ISLNK(info.st_mode) or getattr(info, 'st_file_attributes', 0) & 0x400:
        raise WorkerError('CLEANUP_UNSAFE_PATH', '临时目录包含链接，拒绝确认清理')


def owned_root(root):
    path = Path(root).absolute()
    plain_path(path)
    resolved = path.resolve()
    if resolved == Path(resolved.anchor):
        raise WorkerError('CLEANUP_UNSAFE_PATH', '拒绝使用磁盘根目录作为 Worker 临时目录')
    resolved.mkdir(parents=True, exist_ok=True)
    return resolved


@contextmanager
def exclusive_root(root):
    """OS lock releases on crash. Parent holds it until its media child exits."""
    root = owned_root(root)
    lock = root / '.worker.lock'
    plain_path(lock)
    with lock.open('a+b') as file:
        file.seek(0, os.SEEK_END)
        if file.tell() == 0:
            file.write(b'0')
            file.flush()
        file.seek(0)
        try:
            if os.name == 'nt':
                import msvcrt
                msvcrt.locking(file.fileno(), msvcrt.LK_NBLCK, 1)
            else:
                import fcntl
                fcntl.flock(file.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError as error:
            raise WorkerError('WORKER_ROOT_BUSY', '该临时目录已有 Worker 运行，拒绝并发处理或清理') from error
        try:
            yield root
        finally:
            file.seek(0)
            if os.name == 'nt':
                msvcrt.locking(file.fileno(), msvcrt.LK_UNLCK, 1)
            else:
                fcntl.flock(file.fileno(), fcntl.LOCK_UN)


def cleanup_schema(settings):
    path = settings.cleanup_contract_path or Path(__file__).resolve().parents[2] / 'contracts/v1.schema.json'
    try:
        schema = json.loads(Path(path).read_text(encoding='utf-8'))
        for name in ('WorkerCleanupClaimRequest', 'WorkerCleanup', 'WorkerCleanupComplete', 'WorkerCleanupReceipt'):
            if name not in schema.get('$defs', {}):
                raise ValueError('missing cleanup definitions')
        Draft202012Validator.check_schema(schema)
        return schema
    except Exception as error:
        raise WorkerError('CONFIG_MISMATCH', '清理需要后端已交付的完整协议文件，不能使用旧 Schema') from error


def validate_cleanup(settings, value, definition):
    schema = cleanup_schema(settings)
    try:
        Draft202012Validator({**schema, '$ref': f'#/$defs/{definition}'},
                             format_checker=FormatChecker()).validate(value)
    except Exception as error:
        raise WorkerError('INVALID_RESULT', '清理消息不符合后端协议') from error


def _fence_path(root, session_id):
    directory = owned_root(root) / '.deletion-fences'
    plain_path(directory)
    directory.mkdir(exist_ok=True)
    path = directory / (str(UUID(session_id)) + '.json')
    plain_path(path)
    return path


def check_fence(root, claim):
    path = _fence_path(root, claim['session_id'])
    if not path.exists():
        return
    try:
        generation = json.loads(path.read_text(encoding='utf-8'))['storage_generation']
        if type(generation) is not int or generation < 1:
            raise ValueError('invalid fence')
    except (OSError, ValueError, KeyError, TypeError) as error:
        raise WorkerError('CLEANUP_STATE_INVALID', '本地删除记录损坏，停止任务写入') from error
    if claim['storage_generation'] <= generation:
        raise LeaseLost('该课堂已进入删除流程，拒绝旧代次重新写入')


def _persist_fence(root, claim):
    path = _fence_path(root, claim['session_id'])
    generation = claim['storage_generation']
    if path.exists():
        old = json.loads(path.read_text(encoding='utf-8'))['storage_generation']
        if type(old) is not int or old < 1:
            raise WorkerError('CLEANUP_STATE_INVALID', '本地删除记录损坏，拒绝回报')
        generation = max(generation, old)
    descriptor, name = tempfile.mkstemp(prefix='.pending-', dir=path.parent)
    try:
        with os.fdopen(descriptor, 'w', encoding='utf-8') as file:
            json.dump({'storage_generation': generation}, file)
            file.flush()
            os.fsync(file.fileno())
        os.replace(name, path)
        if os.name != 'nt':
            fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def clean_claim(root, claim):
    """Caller holds exclusive_root; failure means no cleanup receipt may be sent."""
    root = owned_root(root)
    targets = []
    for job in claim['job_ids']:
        target = root / str(UUID(job))
        plain_path(target)
        if target.resolve().parent != root:
            raise WorkerError('CLEANUP_UNSAFE_PATH', '任务目录超出 Worker 临时目录')
        if not target.exists():
            continue
        if not target.is_dir():
            raise WorkerError('CLEANUP_UNSAFE_PATH', '任务目录格式异常')
        # Check the complete target set before removing anything. A malformed lease
        # or link is not silently skipped and never produces a success receipt.
        for lease in target.iterdir():
            plain_path(lease)
            if not lease.is_dir() or lease.name != str(UUID(lease.name)):
                raise WorkerError('CLEANUP_UNSAFE_PATH', '任务目录不是自有租约目录')
            for parent, directories, files in os.walk(lease, followlinks=False):
                for name in directories + files:
                    plain_path(Path(parent) / name)
        targets.append(target)
    _persist_fence(root, claim)
    for target in targets:
        shutil.rmtree(target)
        if target.exists():
            raise WorkerError('CLEANUP_INCOMPLETE', '临时文件清理未完成，不回报成功')
    return len(targets)
