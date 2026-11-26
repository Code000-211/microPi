//go:build unix

package docsearch

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func mmapFile(f *os.File, size int) ([]byte, func() error, error) {
	if size == 0 {
		return nil, func() error { return nil }, nil
	}
	data, err := unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, nil, err
	}
	return data, func() error {
		return unix.Munmap(data)
	}, nil
}

func fileIdentity(fi os.FileInfo) (dev uint64, ino uint64, size int64) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), uint64(st.Ino), fi.Size()
	}
	return 0, uint64(fi.ModTime().UnixNano()), fi.Size()
}
