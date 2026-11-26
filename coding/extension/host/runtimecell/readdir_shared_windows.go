//go:build windows

package runtimecell

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/windows"
)

func readDirShared(dir string) ([]fs.DirEntry, error) {
	name, err := windows.UTF16PtrFromString(extendedPath(dir))
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: dir, Err: err}
	}
	handle, err := windows.CreateFile(name, windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: dir, Err: err}
	}
	file := os.NewFile(uintptr(handle), dir)
	defer func() { _ = file.Close() }()
	entries, err := file.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// extendedPath is path in the \\?\ form that CreateFile needs once a path is longer than MAX_PATH, which os.Open applies by itself.
func extendedPath(path string) string {
	if len(path) < 248 || strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if strings.HasPrefix(abs, `\\`) {
		return `\\?\UNC\` + abs[2:]
	}
	return `\\?\` + abs
}
