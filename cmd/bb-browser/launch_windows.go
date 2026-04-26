//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess    = 0x00000008
	createNewProcGroup = 0x00000200
)

func configureDetach(cmd *exec.Cmd, detach bool) {
	if !detach {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= detachedProcess | createNewProcGroup
	cmd.SysProcAttr.HideWindow = false
}

func releaseChild(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Release()
}
