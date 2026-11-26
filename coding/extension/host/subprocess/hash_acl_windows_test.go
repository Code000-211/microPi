//go:build windows

package subprocess

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"
)

// A directory that is there and denies the walk by ACL is not one another process is deleting: the hash of a module it holds source for fails.
func TestHashSourceDirFailsClosedOnALiveACLDeniedDirectory(t *testing.T) {
	extension, replaced := writeFixtureTree(t)
	denied := filepath.Join(replaced, "locked")
	if err := os.Mkdir(denied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(denied, "locked.go"), []byte("package locked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("icacls", denied, "/deny", current.Username+":(RD)").CombinedOutput(); err != nil {
		t.Fatalf("icacls deny: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("icacls", denied, "/remove:d", current.Username).Run() })
	if _, err := os.ReadDir(denied); err == nil {
		t.Fatal("the ACL deny did not stop the current user listing the directory, so the test would prove nothing")
	}
	if got, err := hashSourceDir(extension, "go"); err == nil {
		t.Fatalf("a live directory that denies the walk produced reusable hash %s", got)
	}
}
