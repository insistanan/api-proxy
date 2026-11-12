//go:build !windows

package intent

import "os/exec"

func hideProcessWindow(cmd *exec.Cmd) {}
