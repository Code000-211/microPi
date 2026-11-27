package prompts

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
)

// µPi: a listing of the working folder for the system prompt, taken once when the session starts. Nearly every coding
// run otherwise spends its first turn listing the folder (`find`, `ls`). Bounded, so that a large repository costs a
// short summary.
//
// Ports microPi packages/coding-agent/src/core/project-snapshot.ts.

// maxListedFiles is how many files are listed one per line; above it, folders with their file counts.
const (
	maxListedFiles   = 150
	maxWalkedFiles   = 5_000
	maxGitListBytes  = 16 * 1024 * 1024
	gitListTimeout   = 2 * time.Second
	snapshotMaxDepth = 6
)

var snapshotSkippedDirs = map[string]bool{
	"node_modules": true,
	"target":       true,
	"build":        true,
	"dist":         true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	".git":         true,
	"out":          true,
}

// listSnapshotFiles is git's list of tracked and unignored files where cwd holds a .git, else a walk past hidden and
// build folders. ok is false when the walk reaches maxWalkedFiles.
func listSnapshotFiles(cwd string) (files []string, ok bool) {
	if _, err := os.Stat(filepath.Join(cwd, ".git")); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), gitListTimeout)
		cmd := exec.CommandContext(ctx, "git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
		cmd.Dir = cwd
		out, err := cmd.Output()
		cancel()
		if err == nil && len(out) <= maxGitListBytes {
			for _, name := range strings.Split(string(out), "\x00") {
				if name != "" {
					files = append(files, name)
				}
			}
			sort.Strings(files)
			return files, true
		}
	}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if len(files) >= maxWalkedFiles || depth > snapshotMaxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		slices.SortFunc(entries, func(a, b os.DirEntry) int { return localecompare.Compare(a.Name(), b.Name()) })
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || snapshotSkippedDirs[name] {
				continue
			}
			path := filepath.Join(dir, name)
			if entry.IsDir() {
				walk(path, depth+1)
			} else if entry.Type().IsRegular() {
				if rel, err := filepath.Rel(cwd, path); err == nil {
					files = append(files, filepath.ToSlash(rel))
				}
			}
			if len(files) >= maxWalkedFiles {
				return
			}
		}
	}
	walk(cwd, 0)
	return files, len(files) < maxWalkedFiles
}

// summarizeSnapshot lists the top-level files and the folders with how many files each holds, two levels deep, for a
// project too large to list.
func summarizeSnapshot(files []string) string {
	counts := map[string]int{}
	var top []string
	for _, file := range files {
		parts := strings.Split(file, "/")
		if len(parts) == 1 {
			top = append(top, file)
		}
		for depth := 1; depth <= min(2, len(parts)-1); depth++ {
			counts[strings.Join(parts[:depth], "/")+"/"]++
		}
	}
	dirs := make([]string, 0, len(counts))
	for dir := range counts {
		dirs = append(dirs, dir)
	}
	slices.SortFunc(dirs, localecompare.Compare)
	lines := slices.Clone(top[:min(40, len(top))])
	for _, dir := range dirs[:min(80, len(dirs))] {
		lines = append(lines, fmt.Sprintf("%s (%d files)", dir, counts[dir]))
	}
	return strings.Join(lines, "\n")
}

// TakeProjectSnapshot is the listing of cwd for the system prompt's project section, or "" for a folder that is no
// project (the home folder, /) or cannot be listed. Callers take it once per session.
func TakeProjectSnapshot(cwd string) string {
	root, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if absHome, err := filepath.Abs(home); err == nil && root == absHome {
			return ""
		}
	}
	if slash, err := filepath.Abs("/"); err == nil && root == slash {
		return ""
	}
	files, ok := listSnapshotFiles(root)
	if !ok || len(files) == 0 {
		return ""
	}
	listing := strings.Join(files, "\n")
	if len(files) > maxListedFiles {
		listing = fmt.Sprintf("%d files; the folders, with how many files each holds:\n%s", len(files), summarizeSnapshot(files))
	}
	return "The files of the working folder when the session started:\n" + listing
}

var sessionSnapshots sync.Map // absolute cwd -> string

// SessionProjectSnapshot is the session's project snapshot of cwd (agent-session.ts _getProjectSnapshot): taken once
// and kept, "" in remote mode or with PI_PROJECT_SNAPSHOT=off.
//
// pig divergence: it is kept per process and working folder rather than per AgentSession object, so the CLI's
// prompt build and its Session share one listing.
func SessionProjectSnapshot(cwd string, remote bool) string {
	if remote || os.Getenv("PI_PROJECT_SNAPSHOT") == "off" || cwd == "" {
		return ""
	}
	key, err := filepath.Abs(cwd)
	if err != nil {
		key = cwd
	}
	if cached, ok := sessionSnapshots.Load(key); ok {
		return cached.(string)
	}
	snapshot, _ := sessionSnapshots.LoadOrStore(key, TakeProjectSnapshot(key))
	return snapshot.(string)
}

// ProjectSections is the `sections` option holding the project snapshot, as agent-session.ts passes
// `sections: { project }`, or nil for an empty snapshot.
func ProjectSections(project string) *ai.OrderedSections {
	if project == "" {
		return nil
	}
	return &ai.OrderedSections{{Name: "project", Value: new(project)}}
}
