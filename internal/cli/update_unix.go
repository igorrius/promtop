//go:build !windows

package cli

import (
	"fmt"
	"syscall"
)

const installScriptName = "install.sh"

// runScript replaces this process with the script: nothing of promtop stays
// running while its binary is swapped, and the script's exit code is promtop's.
// It returns only if the script could not be started.
func runScript(script []byte, env []string) int {
	err := syscall.Exec("/bin/sh", []string{"sh", "-c", string(script)}, env) //nolint:gosec // running the project's install script is the command's purpose
	return fail(fmt.Errorf("run the install script: %w", err))
}
