"""Resolve DirectML indices from the current DXGI inventory, never a stale label."""
import ctypes as c
from ctypes import wintypes as w
import os
import uuid
from functools import lru_cache


def dxgi_adapters():
    if os.name != 'nt':
        return []
    class GUID(c.Structure):
        _fields_ = [('data', c.c_ubyte * 16)]
    class LUID(c.Structure):
        _fields_ = [('low', w.DWORD), ('high', w.LONG)]
    class Desc(c.Structure):
        _fields_ = [('description', w.WCHAR * 128), ('vendor', w.UINT), ('device', w.UINT),
                    ('subsystem', w.UINT), ('revision', w.UINT), ('video', c.c_size_t),
                    ('system', c.c_size_t), ('shared', c.c_size_t), ('luid', LUID), ('flags', w.UINT)]
    def method(obj, index, result, *types):
        table = c.cast(obj, c.POINTER(c.POINTER(c.c_void_p))).contents
        return c.WINFUNCTYPE(result, c.c_void_p, *types)(table[index])
    iid = GUID.from_buffer_copy(uuid.UUID('770aae78-f26f-4dba-a829-253c83d1b387').bytes_le)
    factory = c.c_void_p()
    dll = c.WinDLL('dxgi.dll')
    create = dll.CreateDXGIFactory1
    create.argtypes = [c.POINTER(GUID), c.POINTER(c.c_void_p)]
    create.restype = c.c_long
    def checked(hr):
        if hr:
            raise OSError(hex(hr & 0xffffffff))
    checked(create(c.byref(iid), c.byref(factory)))
    items = []
    try:
        for index in range(32):
            adapter = c.c_void_p()
            hr = method(factory, 12, c.c_long, w.UINT, c.POINTER(c.c_void_p))(factory, index, c.byref(adapter))
            if (hr & 0xffffffff) == 0x887a0002:
                break
            checked(hr)
            try:
                desc = Desc()
                checked(method(adapter, 10, c.c_long, c.POINTER(Desc))(adapter, c.byref(desc)))
                items.append({'device_id': index, 'name': desc.description, 'vendor_id': int(desc.vendor),
                              'dedicated_mb': round(desc.video / 1024**2), 'software': bool(desc.flags & 2)})
            finally:
                method(adapter, 2, w.ULONG)(adapter)
    finally:
        method(factory, 2, w.ULONG)(factory)
    return items


def choose_adapter(adapters, preferred_name='NVIDIA', override=None):
    hardware = [a for a in adapters if not a['software']]
    if override is not None:
        matches = [a for a in hardware if a['device_id'] == int(override)]
        if not matches:
            raise ValueError('指定的显卡编号不存在或是软件显卡')
        return matches[0]
    preferred = [a for a in hardware if preferred_name.casefold() in a['name'].casefold()]
    if not hardware:
        raise ValueError('没有找到可用的硬件显卡')
    return max(preferred or hardware, key=lambda a: a['dedicated_mb'])


@lru_cache(maxsize=1)
def selected_device():
    try:
        adapters = dxgi_adapters()
        selected = choose_adapter(adapters, override=os.environ.get('VISION_DML_DEVICE'))
        return {'selected': selected, 'adapters': adapters, 'error': None}
    except Exception as error:
        return {'selected': None, 'adapters': [], 'error': str(error)}
