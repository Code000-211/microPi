//go:build !windows

package runtimecell

import (
	"io/fs"
	"os"
)

func readDirShared(dir string) ([]fs.DirEntry, error) { return os.ReadDir(dir) }
