package docsearch

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestEngine(dir string) *Engine {
	return &Engine{roots: []string{dir}, extensions: []string{"md", "py"}, cache: map[resultCacheKey]Results{}, readyCh: make(chan struct{})}
}

// A check holds its view while it reads the base: a rebuild that replaces the view meanwhile neither unmaps the base under it nor is
// replaced by the view the check computed on the older base.
func TestRefreshOnAnOlderBaseNeitherCrashesNorRevertsARebuild(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\nalpha beta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := Build(Config{Roots: []string{dir}, Extensions: []string{"md"}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "x.idx")
	if err := idx.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(dir)
	e.setView(loaded, nil)

	v := e.acquireView() // what MaybeRefresh holds
	fresh, err := Build(Config{Roots: []string{dir}, Extensions: []string{"md"}})
	if err != nil {
		t.Fatal(err)
	}
	e.setView(fresh, nil) // a rebuild finishes meanwhile
	files, err := ScanExtensions([]string{dir}, []string{"md"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	v.Base.Diff(files) // reads the mmapped base: it must still be mapped
	e.setViewOnBase(v.Base, nil)
	v.release()
	if e.CurrentView().Base != fresh {
		t.Fatal("the check put the older base back over the rebuild")
	}
}

// A file the walk lists but the index skips (blank here) is a change the first check acts on once; the next checks find nothing new and
// leave the view as it is, instead of rebuilding on every search.
func TestASkippedFileDoesNotRebuildOnEveryCheck(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\nalpha beta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "__init__.py"), []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := Build(Config{Roots: []string{dir}, Extensions: []string{"md", "py"}})
	if err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(dir)
	e.setView(idx, nil)
	check := func() uint64 {
		e.mu.Lock()
		e.notified = true
		e.mu.Unlock()
		e.MaybeRefresh()
		e.bgWG.Wait()
		return e.CurrentView().Generation
	}
	first := check()
	for range 3 {
		if got := check(); got != first {
			t.Fatalf("generation %d after a check that found nothing new, want %d", got, first)
		}
	}
}
