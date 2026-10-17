//go:build !windows

package proc

import "os/exec"

func Hide(cmd *exec.Cmd) *exec.Cmd { return cmd }
