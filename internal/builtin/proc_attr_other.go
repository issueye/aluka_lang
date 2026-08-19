//go:build !windows

package builtin

import "os/exec"

func applyWindowsHide(cmd *exec.Cmd, hide bool) {}
