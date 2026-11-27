package prompts

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Ports microPi packages/coding-agent/test/project-snapshot.test.ts.

func writeSnapshotFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectSnapshotListsASmallProjectsFiles(t *testing.T) {
	dir := t.TempDir()
	writeSnapshotFile(t, filepath.Join(dir, "a.py"), "print('a')\n")
	writeSnapshotFile(t, filepath.Join(dir, "src", "b.ts"), "export const b = 1;")
	if got, want := TakeProjectSnapshot(dir), "The files of the working folder when the session started:\na.py\nsrc/b.ts"; got != want {
		t.Fatalf("snapshot = %q, want %q", got, want)
	}
}

func TestProjectSnapshotSummarizesALargeProjectByFolders(t *testing.T) {
	dir := t.TempDir()
	for i := range 200 {
		writeSnapshotFile(t, filepath.Join(dir, "svc", fmt.Sprintf("s%d", i%4), fmt.Sprintf("f%d.py", i)), "x = 1\n")
	}
	writeSnapshotFile(t, filepath.Join(dir, "README.md"), "hi\n")
	snapshot := TakeProjectSnapshot(dir)
	for _, want := range []string{"201 files; the folders, with how many files each holds:", "README.md\nsvc/ (200 files)\nsvc/s0/ (50 files)"} {
		if !strings.Contains(snapshot, want) {
			t.Fatalf("snapshot lacks %q:\n%s", want, snapshot)
		}
	}
}

func TestProjectSnapshotSkipsBuildFoldersHomeAndEmptyFolders(t *testing.T) {
	dir := t.TempDir()
	writeSnapshotFile(t, filepath.Join(dir, "node_modules", "x", "i.js"), "1")
	writeSnapshotFile(t, filepath.Join(dir, ".hidden", "h.txt"), "1")
	writeSnapshotFile(t, filepath.Join(dir, "dist", "bundle.js"), "1")
	writeSnapshotFile(t, filepath.Join(dir, ".env"), "1")
	if got := TakeProjectSnapshot(dir); got != "" {
		t.Fatalf("snapshot of a folder holding only skipped files = %q", got)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if got := TakeProjectSnapshot(home); got != "" {
			t.Fatalf("home folder snapshot = %q", got)
		}
	}
	if got := TakeProjectSnapshot("/"); got != "" {
		t.Fatalf("root snapshot = %q", got)
	}
}

// A git checkout is listed by git: ignored files are left out, untracked ones are in.
func TestProjectSnapshotUsesGitsListInACheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	writeSnapshotFile(t, filepath.Join(dir, ".gitignore"), "ignored.txt\n")
	writeSnapshotFile(t, filepath.Join(dir, "ignored.txt"), "x")
	writeSnapshotFile(t, filepath.Join(dir, "main.go"), "package main\n")
	if got, want := TakeProjectSnapshot(dir), "The files of the working folder when the session started:\n.gitignore\nmain.go"; got != want {
		t.Fatalf("snapshot = %q, want %q", got, want)
	}
}

func TestSessionProjectSnapshotIsOffInRemoteModeAndWithTheSwitch(t *testing.T) {
	dir := t.TempDir()
	writeSnapshotFile(t, filepath.Join(dir, "a.txt"), "a")
	if got := SessionProjectSnapshot(dir, true); got != "" {
		t.Fatalf("remote snapshot = %q", got)
	}
	t.Setenv("PI_PROJECT_SNAPSHOT", "off")
	if got := SessionProjectSnapshot(dir, false); got != "" {
		t.Fatalf("PI_PROJECT_SNAPSHOT=off snapshot = %q", got)
	}
	t.Setenv("PI_PROJECT_SNAPSHOT", "")
	first := SessionProjectSnapshot(dir, false)
	writeSnapshotFile(t, filepath.Join(dir, "b.txt"), "b")
	if first == "" || SessionProjectSnapshot(dir, false) != first {
		t.Fatalf("the snapshot is taken once: %q then %q", first, SessionProjectSnapshot(dir, false))
	}
}

// The project section follows cwd, as the session's `sections: { project }` does in system-prompt.ts.
func TestProjectSectionFollowsCwd(t *testing.T) {
	sections := BuildSystemPromptSections(Options{Cwd: "/w", Tools: []string{"read"}, Project: "The files of the working folder when the session started:\na.go"})
	last := sections[len(sections)-1]
	if last.Name != "project" || *last.Value != "<project>\nThe files of the working folder when the session started:\na.go\n</project>" || sections[len(sections)-2].Name != "cwd" {
		t.Fatalf("sections end %#v", sections[len(sections)-2:])
	}
}
