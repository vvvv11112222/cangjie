"""Windows process ownership checks for the website launcher."""
import ctypes
from ctypes import wintypes


def identity(pid, terminate=False, expected=None):
    """Inspect/terminate through the SAME process handle, preventing PID reuse races."""
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.OpenProcess.restype = wintypes.HANDLE
    kernel.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
    kernel.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel.GetProcessTimes.argtypes = [wintypes.HANDLE] + [ctypes.POINTER(wintypes.FILETIME)] * 4
    kernel.QueryFullProcessImageNameW.argtypes = [wintypes.HANDLE, wintypes.DWORD, wintypes.LPWSTR, ctypes.POINTER(wintypes.DWORD)]
    kernel.TerminateProcess.argtypes = [wintypes.HANDLE, wintypes.UINT]
    kernel.GetExitCodeProcess.argtypes = [wintypes.HANDLE, ctypes.POINTER(wintypes.DWORD)]
    kernel.WaitForSingleObject.argtypes = [wintypes.HANDLE, wintypes.DWORD]
    handle = kernel.OpenProcess(0x1000 | (0x100001 if terminate else 0), False, int(pid))
    if not handle:
        if ctypes.get_last_error() == 87:  # PID no longer exists
            return None
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        exit_code = wintypes.DWORD()
        if not kernel.GetExitCodeProcess(handle, ctypes.byref(exit_code)):
            raise ctypes.WinError(ctypes.get_last_error())
        if exit_code.value != 259:  # STILL_ACTIVE; a retained process handle may outlive exit.
            return None
        created, exited, system, user = (wintypes.FILETIME() for _ in range(4))
        buffer, size = ctypes.create_unicode_buffer(32768), wintypes.DWORD(32768)
        if not kernel.GetProcessTimes(handle, ctypes.byref(created), ctypes.byref(exited), ctypes.byref(system), ctypes.byref(user)):
            raise ctypes.WinError(ctypes.get_last_error())
        if not kernel.QueryFullProcessImageNameW(handle, 0, buffer, ctypes.byref(size)):
            raise ctypes.WinError(ctypes.get_last_error())
        current = dict(pid=int(pid), executable=buffer.value.lower(), created=(created.dwHighDateTime << 32) | created.dwLowDateTime)
        if terminate:
            if current != {key: expected[key] for key in current}:
                raise RuntimeError("Refusing to terminate a reused PID")
            if not kernel.TerminateProcess(handle, 0):
                raise ctypes.WinError(ctypes.get_last_error())
            if kernel.WaitForSingleObject(handle, 10000) != 0:
                raise RuntimeError("Process has not stopped; process record retained")
        return current
    finally:
        kernel.CloseHandle(handle)


def alive(record):
    return identity(record["pid"]) == {key: record[key] for key in ("pid", "executable", "created")}
