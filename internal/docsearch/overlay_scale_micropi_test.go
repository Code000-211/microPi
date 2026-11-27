package docsearch

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// An edited file that holds one query term does not score and cover like the files that hold all of them: the overlay is weighed on the
// base's scale. Before, the edited notes.md got score 1.4 and coverage 1.0 and auto-recall would have injected it.
func TestAnEditedFileIsScoredOnTheBasesScale(t *testing.T) {
	dir := t.TempDir()
	for i := range 30 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("doc%02d.md", i)), []byte("# Topic\nkubernetes deployment rollout strategy canary analysis here\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	notes := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notes, []byte("# Notes\nunrelated text about lunch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := Build(Config{Roots: []string{dir}, Extensions: []string{"md"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, []byte("# Notes\nunrelated text about lunch and a canary bird\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := ScanExtensions([]string{dir}, []string{"md"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	changed, deleted := idx.Diff(files)
	ov := BuildOverlay(idx, changed, deleted, 0)
	res := SearchCombined(idx, ov, "kubernetes deployment rollout canary", SearchOpts{K: 40})
	var best, edited *Hit
	for i := range res.Hits {
		h := &res.Hits[i]
		if h.Path == "notes.md" {
			edited = h
		} else if best == nil {
			best = h
		}
	}
	if edited == nil || best == nil {
		t.Fatalf("hits = %+v", res.Hits)
	}
	if edited.Score >= best.Score || edited.Coverage > 0.5 {
		t.Fatalf("edited file: score %.3f coverage %.3f; best full match: score %.3f coverage %.3f", edited.Score, edited.Coverage, best.Score, best.Coverage)
	}
}
