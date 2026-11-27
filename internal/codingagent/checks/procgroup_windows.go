//go:build windows

package checks

import "os/exec"

// ownProcessGroup: on Windows cancelling a checker kills its process (exec.CommandContext's default).
func ownProcessGroup(*exec.Cmd) {}
