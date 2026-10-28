//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package ui

import "os"

// On other platforms (e.g. Windows) promtop runs without keyboard input:
// it redraws on every scrape and exits on Ctrl+C.
const interactiveSupported = false

func isTerminal(fd int) bool              { return true }
func makeRaw(fd int) (func(), error)      { return func() {}, nil }
func termSize(fd int) (w, h int, ok bool) { return 0, 0, false }
func notifyResize(ch chan<- os.Signal)    {}
