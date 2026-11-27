package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// After a command that may change files (InvalidateSearchCache), the cached tree is revalidated, not dropped, and a search still sees what
// the command did: a new file, a removed file, and a .gitignore edited in place (which leaves its folder's mtime alone).
func TestSearchAfterACommandSeesItsChangesWithoutAFullWalk(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "build/\n")
	write("src/a.txt", "NEEDLE a\n")
	write("src/b.txt", "NEEDLE b\n")
	write("logs/x.log", "NEEDLE log\n")
	grep := &GrepTool{CWD: dir}
	search := func() string {
		t.Helper()
		raw, _ := json.Marshal(grepParams{Pattern: "NEEDLE", Literal: true})
		res, err := grep.Execute(context.Background(), "c", raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		return extractText(res.Content)
	}
	if out := search(); !strings.Contains(out, "x.log") || !strings.Contains(out, "a.txt") {
		t.Fatalf("first search: %q", out)
	}
	key := normalizeCacheKey(dir) + "|1"
	searchCacheMu.Lock()
	first := treeCache[key]
	searchCacheMu.Unlock()

	// What a command does: a new file, a removed one, and logs/ ignored by editing .gitignore in place. The mtimes are set apart so a
	// coarse file system clock cannot hide the edit.
	time.Sleep(20 * time.Millisecond)
	write("src/c.txt", "NEEDLE c\n")
	if err := os.Remove(filepath.Join(dir, "src", "b.txt")); err != nil {
		t.Fatal(err)
	}
	write(".gitignore", "build/\nlogs/\n")
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(dir, ".gitignore"), later, later)
	InvalidateSearchCache(dir)

	out := search()
	if !strings.Contains(out, "c.txt") || strings.Contains(out, "b.txt") || strings.Contains(out, "x.log") {
		t.Fatalf("search after the command: %q", out)
	}

	// A command that changed nothing the tree depends on keeps the tree: the next search revalidates instead of walking.
	searchCacheMu.Lock()
	second := treeCache[key]
	searchCacheMu.Unlock()
	if second == first {
		t.Fatal("the changed tree was kept")
	}
	InvalidateSearchCache(dir)
	_ = search()
	searchCacheMu.Lock()
	third := treeCache[key]
	searchCacheMu.Unlock()
	if third != second {
		t.Fatal("an unchanged tree was walked again after a command")
	}

	// Only .gitignore changes, in place: no folder's mtime moves, so only its own mtime shows the change.
	write(".gitignore", "build/\nlogs/\nsrc/c.txt\n")
	latest := later.Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(dir, ".gitignore"), latest, latest)
	InvalidateSearchCache(dir)
	if out := search(); strings.Contains(out, "c.txt") || !strings.Contains(out, "a.txt") {
		t.Fatalf("search after editing .gitignore in place: %q", out)
	}
}
