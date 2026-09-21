//go:build !windows

package runtimeprobe

import "os/exec"

func hideWindow(*exec.Cmd) {}
