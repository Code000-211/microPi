//go:build !windows && !pig_strip_node_extensions

package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

// µPi: the Node runtime is extracted once beside the executable when that directory is the user's own and private, so a second home links it
// instead of extracting it again; a directory others can write is not used.
func TestNodeRuntimeIsSharedBesideAPrivateExecutable(t *testing.T) {
	t.Setenv("UPI_NODE_RUNTIME_SHARED", "")
	install := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(install, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(install, "upi")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	previous := sharedNodeRuntimeExecutable
	sharedNodeRuntimeExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { sharedNodeRuntimeExecutable = previous })

	for range 2 {
		home := t.TempDir()
		destination := filepath.Join(t.TempDir(), "runtime")
		if err := materializeNodeRuntime(t.Context(), filepath.Join(home, "ext"), destination); err != nil {
			t.Fatal(err)
		}
		if _, err := os.ReadFile(filepath.Join(destination, "runtime.mjs")); err != nil {
			t.Fatalf("launcher tree is incomplete: %v", err)
		}
		if matches, _ := filepath.Glob(filepath.Join(home, "ext", "node-runtime-*")); len(matches) != 0 {
			t.Fatalf("the runtime was extracted into the home cache: %q", matches)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(install, sharedNodeRuntimeDirName, "node-runtime-*")); len(matches) != 1 {
		t.Fatalf("shared runtimes = %q, want one", matches)
	}

	if err := os.Chmod(install, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(install, 0o755) })
	if root := sharedNodeRuntimeRoot(); root != "" {
		t.Fatalf("a directory others can write was used: %s", root)
	}
}
