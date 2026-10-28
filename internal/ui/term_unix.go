//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package ui

import (
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

const interactiveSupported = true

func tcget(fd int) (*syscall.Termios, error) {
	var t syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), ioctlGetTermios, uintptr(unsafe.Pointer(&t))); e != 0 {
		return nil, e
	}
	return &t, nil
}

func tcset(fd int, t *syscall.Termios) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), ioctlSetTermios, uintptr(unsafe.Pointer(t))); e != 0 {
		return e
	}
	return nil
}

// isTerminal reports whether fd is a terminal.
func isTerminal(fd int) bool {
	_, err := tcget(fd)
	return err == nil
}

// makeRaw puts the terminal into raw mode and returns a restore function.
func makeRaw(fd int) (func(), error) {
	old, err := tcget(fd)
	if err != nil {
		return nil, err
	}
	raw := *old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := tcset(fd, &raw); err != nil {
		return nil, err
	}
	return func() { _ = tcset(fd, old) }, nil
}

type winsize struct {
	Row, Col, X, Y uint16
}

// termSize returns the terminal size of fd.
func termSize(fd int) (w, h int, ok bool) {
	var ws winsize
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); e != 0 {
		return 0, 0, false
	}
	if ws.Col == 0 || ws.Row == 0 {
		return 0, 0, false
	}
	return int(ws.Col), int(ws.Row), true
}

func notifyResize(ch chan<- os.Signal) { signal.Notify(ch, syscall.SIGWINCH) }
