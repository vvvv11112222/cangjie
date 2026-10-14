//go:build !windows

package storage

import "syscall"

func (l *Local) FreeBytes() (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(l.root, &stat); err != nil {
		return 0, err
	}
	free := uint64(stat.Bavail) * uint64(stat.Bsize)
	if free > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1), nil
	}
	return int64(free), nil
}
