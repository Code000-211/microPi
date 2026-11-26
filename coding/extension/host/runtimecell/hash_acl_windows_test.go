//go:build windows

package runtimecell

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// denyListing makes dir unlistable to the current user with an ACL deny entry that the test cleanup lifts.
func denyListing(t *testing.T, dir string) {
	t.Helper()
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("icacls", dir, "/deny", current.Username+":(RD)").CombinedOutput(); err != nil {
		t.Fatalf("icacls deny: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("icacls", dir, "/remove:d", current.Username).Run() })
	if _, err := os.ReadDir(dir); err == nil {
		t.Fatal("the ACL deny did not stop the current user listing the directory, so the test would prove nothing")
	}
}

// A directory that is there and denies the walk by ACL is not one another process is deleting: the hash fails. Its Lstat succeeds, which is what tells it from a delete-pending directory (whose Lstat is denied too).
func TestHashGoModuleFailsClosedOnALiveACLDeniedDirectory(t *testing.T) {
	root := t.TempDir()
	for name, content := range goModuleFiles() {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := hashGoModule(root)
	if strings.HasPrefix(want, "error:") {
		t.Fatal(want)
	}
	denied := filepath.Join(root, "locked")
	if err := os.Mkdir(denied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(denied, "locked.go"), []byte("package locked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	denyListing(t, denied)
	if got := hashGoModule(root); !strings.HasPrefix(got, "error:") {
		t.Fatalf("a live directory that denies the walk produced reusable hash %q", got)
	}
}
