package docsearch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A cache file whose postings or definition map point outside the index is rejected by the full validation (Load without the .ok stamp),
// instead of loading and then panicking in Search.
func TestLoadRejectsPostingsAndDefinitionsOutOfRange(t *testing.T) {
	for name, mutate := range map[string]func(*Index){
		"definition section": func(idx *Index) {
			for i := range idx.DefMap.Vals {
				idx.DefMap.Vals[i] = 1 << 20
			}
		},
		"posting offsets": func(idx *Index) {
			for i := range idx.PostOffsets {
				idx.PostOffsets[i] = uint32(i * 1000)
			}
		},
		"posting section": func(idx *Index) {
			for i := range idx.PostSecs {
				idx.PostSecs[i] = 1 << 20
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc AlphaBeta() int {\n\treturn 1\n}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			idx, err := Build(Config{Roots: []string{dir}, Extensions: []string{"go"}})
			if err != nil {
				t.Fatal(err)
			}
			mutate(idx)
			path := filepath.Join(t.TempDir(), "x.idx")
			if err := idx.Save(path); err != nil {
				t.Fatal(err)
			}
			_ = os.Remove(path + ".ok")
			if _, err := Load(path, []string{dir}); !errors.Is(err, ErrBadFile) {
				t.Fatalf("Load = %v, want ErrBadFile", err)
			}
		})
	}
}
