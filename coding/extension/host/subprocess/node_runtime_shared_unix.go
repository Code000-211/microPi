//go:build !windows

package subprocess

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// sharedNodeRuntimeDirName is the directory beside the executable that holds the extracted Node runtime.
const sharedNodeRuntimeDirName = ".upi-node-runtime"

// sharedNodeRuntimeExecutable names the executable the shared runtime sits beside; tests replace it.
var sharedNodeRuntimeExecutable = os.Executable

// sharedNodeRuntimeRoot is the install-scoped cache root for the extracted Node runtime: a directory beside the executable, so a fresh
// home (CI, a container, a new user profile) links the runtime instead of extracting its ~1,900 files again. Pi keeps its node_modules in its
// install the same way.
//
// It is used only when the executable's directory and the cache directory belong to this user and no one else can write to them: they are
// then as trusted as the executable itself. Otherwise it returns "" and the per-home cache is used, as before.
func sharedNodeRuntimeRoot() string {
	if os.Getenv("UPI_NODE_RUNTIME_SHARED") == "off" {
		return ""
	}
	exe, err := sharedNodeRuntimeExecutable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	if !ownedAndPrivate(dir) {
		return ""
	}
	root := filepath.Join(dir, sharedNodeRuntimeDirName)
	if err := os.Mkdir(root, 0o755); err != nil && !os.IsExist(err) {
		return ""
	}
	if !ownedAndPrivate(root) {
		return ""
	}
	return root
}

// ownedAndPrivate reports whether path is a real directory (not a symlink) owned by this user that neither its group nor others can write.
func ownedAndPrivate(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}

// pruneSharedNodeRuntimes removes the runtimes of other executable versions from root once they are a day old. A launcher holds its own
// hard links, so a process of an older version that still runs keeps its files.
func pruneSharedNodeRuntimes(root, keep string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == keep || !strings.HasPrefix(name, "node-runtime-") || !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < 24*time.Hour {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, name))
	}
}
