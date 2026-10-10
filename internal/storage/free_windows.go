//go:build windows

package storage

import (
	"fmt"
	"syscall"
	"unsafe"
)

func (l *Local) FreeBytes() (int64, error) {
	path, err := syscall.UTF16PtrFromString(l.root)
	if err != nil {
		return 0, err
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	var available uint64
	result, _, callErr := proc.Call(uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(&available)), 0, 0)
	if result == 0 {
		return 0, fmt.Errorf("read storage free bytes: %w", callErr)
	}
	if available > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1), nil
	}
	return int64(available), nil
}
