package prompts

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	maxSnapshotFiles = 150
	maxSnapshotBytes = 6000
)

var (
	snapshotMu    sync.Mutex
	snapshotCache = make(map[string]string)
)

// BuildProjectSnapshot returns a compact <project> listing for cwd (cached per cwd),
// or "" if PI_PROJECT_SNAPSHOT=off/0 or cwd is empty.
func BuildProjectSnapshot(cwd string) string {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("PI_PROJECT_SNAPSHOT"))); v == "off" || v == "0" || v == "false" {
		return ""
	}
	if cwd == "" {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	snapshotMu.Lock()
	if cached, ok := snapshotCache[abs]; ok {
		snapshotMu.Unlock()
		return cached
	}
	snapshotMu.Unlock()

	snap := computeProjectSnapshot(abs)

	snapshotMu.Lock()
	if len(snapshotCache) > 64 {
		clear(snapshotCache)
	}
	snapshotCache[abs] = snap
	snapshotMu.Unlock()
	return snap
}

func computeProjectSnapshot(cwd string) string {
	if files, ok := walkSmallRepoFiles(cwd); ok && len(files) > 0 {
		return joinCapped(files)
	}
	return summarizeDirs(cwd, 2)
}

func walkSmallRepoFiles(cwd string) ([]string, bool) {
	var files []string
	tooMany := false
	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		if tooMany {
			return
		}
		des, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, de := range des {
			if tooMany {
				return
			}
			name := de.Name()
			if name == ".git" || name == "node_modules" || name == ".work" || name == "target" || name == ".cache" {
				continue
			}
			p := name
			if rel != "" {
				p = rel + "/" + name
			}
			if de.IsDir() {
				walk(filepath.Join(dir, name), p)
			} else {
				files = append(files, p)
				if len(files) > maxSnapshotFiles {
					tooMany = true
					return
				}
			}
		}
	}
	walk(cwd, "")
	if tooMany {
		return nil, false
	}
	sort.Strings(files)
	return files, true
}

func summarizeDirs(cwd string, maxDepth int) string {
	var entries []string
	var walk func(dir string, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		if len(entries) >= maxSnapshotFiles {
			return
		}
		des, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, de := range des {
			if len(entries) >= maxSnapshotFiles {
				return
			}
			name := de.Name()
			if name == ".git" || name == "node_modules" || name == ".work" || name == "target" {
				continue
			}
			p := name
			if rel != "" {
				p = rel + "/" + name
			}
			if de.IsDir() {
				entries = append(entries, p+"/")
				if depth < maxDepth {
					walk(filepath.Join(dir, name), p, depth+1)
				}
			} else {
				entries = append(entries, p)
			}
		}
	}
	walk(cwd, "", 1)
	if len(entries) == 0 {
		return ""
	}
	return joinCapped(entries)
}

func joinCapped(entries []string) string {
	var b strings.Builder
	for i, e := range entries {
		if b.Len()+len(e)+1 > maxSnapshotBytes {
			break
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e)
	}
	return b.String()
}
