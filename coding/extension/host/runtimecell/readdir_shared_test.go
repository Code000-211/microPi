package runtimecell

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// WalkDirShared visits what filepath.WalkDir visits, in its order, and honors SkipDir and SkipAll the same way.
func TestWalkDirSharedMatchesFilepathWalkDir(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/x.go", "a/sub/y.go", "b.go", "c/z.go", "c/skip/w.go", "d/last.go"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		stop func(path string, d fs.DirEntry) error
	}{
		{"all", func(string, fs.DirEntry) error { return nil }},
		{"skip a directory", func(path string, d fs.DirEntry) error {
			if d.IsDir() && d.Name() == "skip" {
				return filepath.SkipDir
			}
			return nil
		}},
		{"skip the rest of a directory from a file", func(path string, d fs.DirEntry) error {
			if d.Name() == "x.go" {
				return filepath.SkipDir
			}
			return nil
		}},
		{"skip all", func(path string, d fs.DirEntry) error {
			if d.Name() == "z.go" {
				return filepath.SkipAll
			}
			return nil
		}},
	} {
		var want, got []string
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			want = append(want, path)
			return tc.stop(path, d)
		}); err != nil {
			t.Fatal(err)
		}
		if err := WalkDirShared(root, func(path string, d fs.DirEntry, err error) error {
			got = append(got, path)
			return tc.stop(path, d)
		}); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: WalkDirShared visited %v, want %v", tc.name, got, want)
		}
	}
}

// A directory that cannot be listed reaches fn a second time with the error, and fn decides, as in filepath.WalkDir; a missing root reaches fn with a nil entry.
func TestWalkDirSharedReportsListingErrorsToFn(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "gone"), 0o755); err != nil {
		t.Fatal(err)
	}
	var errs []error
	err := WalkDirShared(filepath.Join(root, "missing"), func(path string, d fs.DirEntry, err error) error {
		if d != nil {
			t.Errorf("a missing root reached fn with entry %v", d)
		}
		errs = append(errs, err)
		return err
	})
	if !errors.Is(err, fs.ErrNotExist) || len(errs) != 1 {
		t.Fatalf("missing root: err %v, fn errors %v", err, errs)
	}
	entries, err := ReadDirShared(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "gone" || !entries[0].IsDir() {
		t.Fatalf("ReadDirShared = %v, %v", entries, err)
	}
	if _, err := ReadDirShared(filepath.Join(root, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadDirShared of a missing directory = %v, want not-exist", err)
	}
}
