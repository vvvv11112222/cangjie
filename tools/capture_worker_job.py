"""Own the B0 worker process tree in a Windows Job (no breakaway or inherited job handle).

The child waits on stdin before importing Worker code. Assignment failure or supervisor
termination before assignment closes that pipe, so an uncontained worker never starts.
"""
import ctypes
from ctypes import wintypes
import subprocess
import sys
import time


class BasicLimits(ctypes.Structure):
    _fields_ = [("process_time", ctypes.c_int64), ("job_time", ctypes.c_int64),
                ("flags", wintypes.DWORD), ("min_working_set", ctypes.c_size_t),
                ("max_working_set", ctypes.c_size_t), ("active_limit", wintypes.DWORD),
                ("affinity", ctypes.c_size_t), ("priority", wintypes.DWORD),
                ("scheduling", wintypes.DWORD)]


class ExtendedLimits(ctypes.Structure):
    _fields_ = [("basic", BasicLimits), ("io", ctypes.c_uint64 * 6),
                ("process_memory", ctypes.c_size_t), ("job_memory", ctypes.c_size_t),
                ("peak_process_memory", ctypes.c_size_t), ("peak_job_memory", ctypes.c_size_t)]


class Accounting(ctypes.Structure):
    _fields_ = [("times", ctypes.c_int64 * 4), ("faults", wintypes.DWORD),
                ("total", wintypes.DWORD), ("active", wintypes.DWORD), ("terminated", wintypes.DWORD)]


class Job:
    def __init__(self, name, *, create=False):
        self.kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        for function, args, result in (
            ("CreateJobObjectW", [ctypes.c_void_p, wintypes.LPCWSTR], wintypes.HANDLE),
            ("OpenJobObjectW", [wintypes.DWORD, wintypes.BOOL, wintypes.LPCWSTR], wintypes.HANDLE),
            ("SetInformationJobObject", [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD], wintypes.BOOL),
            ("QueryInformationJobObject", [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD, ctypes.c_void_p], wintypes.BOOL),
            ("AssignProcessToJobObject", [wintypes.HANDLE, wintypes.HANDLE], wintypes.BOOL),
            ("TerminateJobObject", [wintypes.HANDLE, wintypes.UINT], wintypes.BOOL),
            ("CloseHandle", [wintypes.HANDLE], wintypes.BOOL),
        ):
            api = getattr(self.kernel, function)
            api.argtypes, api.restype = args, result
        self.handle = self.kernel.CreateJobObjectW(None, name) if create else self.kernel.OpenJobObjectW(0x0004 | 0x0008, False, name)
        if not self.handle:
            if not create and ctypes.get_last_error() == 2:
                return  # Supervisor has not created the job yet, or already exited.
            raise ctypes.WinError(ctypes.get_last_error())
        if create:
            if ctypes.get_last_error() == 183:
                self.close()
                raise RuntimeError("Worker Job already exists")
            limits = ExtendedLimits()
            limits.basic.flags = 0x2000  # JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
            if not self.kernel.SetInformationJobObject(self.handle, 9, ctypes.byref(limits), ctypes.sizeof(limits)):
                error = ctypes.WinError(ctypes.get_last_error())
                self.close()
                raise error

    def assign(self, process):
        # Popen owns a stable process handle on Windows; never reopen an arbitrary PID.
        if not self.kernel.AssignProcessToJobObject(self.handle, int(process._handle)):
            raise ctypes.WinError(ctypes.get_last_error())

    def terminate_and_wait(self, timeout=10):
        if not self.handle:
            return
        if not self.kernel.TerminateJobObject(self.handle, 1):
            raise ctypes.WinError(ctypes.get_last_error())
        deadline = time.monotonic() + timeout
        while True:
            info = Accounting()
            if not self.kernel.QueryInformationJobObject(self.handle, 1, ctypes.byref(info), ctypes.sizeof(info), None):
                raise ctypes.WinError(ctypes.get_last_error())
            if info.active == 0:
                return
            if time.monotonic() >= deadline:
                raise RuntimeError("Worker descendants have not stopped; process record retained")
            time.sleep(.05)

    def close(self):
        if self.handle:
            self.kernel.CloseHandle(self.handle)
            self.handle = None


def supervise(name, code):
    job = Job(name, create=True)
    child = None
    try:
        bootstrap = "import sys; gate=sys.stdin.read(1); sys.stdin.close(); " \
                    "exec(sys.argv[1]) if gate == '1' else sys.exit(1)"
        child = subprocess.Popen([sys.executable, "-c", bootstrap, code], stdin=subprocess.PIPE,
                                 creationflags=subprocess.CREATE_NO_WINDOW)
        job.assign(child)
        child.stdin.write(b"1")
        child.stdin.close()
        return child.wait()
    finally:
        # A failed assignment must also release the gated child, which is outside the Job.
        if child is not None and child.stdin is not None and not child.stdin.closed:
            child.stdin.close()
        if child is not None and child.poll() is None:
            child.kill()
            child.wait(timeout=10)
        try:
            job.terminate_and_wait()
        finally:
            job.close()
