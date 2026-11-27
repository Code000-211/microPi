package docsearch

import (
	"os"
	"path/filepath"
	"testing"
)

// A root that is a symlink to a folder is indexed like the folder (a project reached through a link, a ~/docs link in roots).
func TestScanFollowsASymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "a.md"), []byte("# A\nalpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	files, err := ScanExtensions([]string{link}, []string{"md"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Rel != "a.md" {
		t.Fatalf("files = %+v, want a.md", files)
	}
}
