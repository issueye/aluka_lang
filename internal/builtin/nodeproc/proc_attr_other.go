//go:build !windows

package nodeproc

import "os/exec"

func applyWindowsHide(cmd *exec.Cmd, hide bool) {}
