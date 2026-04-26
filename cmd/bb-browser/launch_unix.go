//go:build linux || darwin

package main

import (
	"os/exec"
	"syscall"
)

func configureDetach(cmd *exec.Cmd, detach bool) {
	if !detach {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

func releaseChild(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Release()
}
