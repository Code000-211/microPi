//go:build !unix

package docsearch

import (
	"io"
	"os"
)

func mmapFile(f *os.File, size int) ([]byte, func() error, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(f, data); err != nil {
		return nil, nil, err
	}
	return data, func() error { return nil }, nil
}

func fileIdentity(fi os.FileInfo) (dev uint64, ino uint64, size int64) {
	return 0, uint64(fi.ModTime().UnixNano()), fi.Size()
}
