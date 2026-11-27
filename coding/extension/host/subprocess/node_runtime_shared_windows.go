//go:build windows

package subprocess

// sharedNodeRuntimeRoot: on Windows the runtime stays in the per-home cache (no owner check on the install directory).
func sharedNodeRuntimeRoot() string { return "" }

func pruneSharedNodeRuntimes(string, string) {}
