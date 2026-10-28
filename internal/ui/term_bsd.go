//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package ui

import "syscall"

const (
	ioctlGetTermios = syscall.TIOCGETA
	ioctlSetTermios = syscall.TIOCSETA
)
