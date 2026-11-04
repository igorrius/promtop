//go:build windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const installScriptName = "install.ps1"

// runScript runs the script and waits for it. Windows cannot replace a process, and
// exiting first would return the prompt in the middle of the update and lose its
// exit code. Waiting is safe: a running exe cannot be overwritten but can be renamed,
// so the script moves this one to promtop.exe.old, which the next update removes.
func runScript(script []byte, env []string) int {
	dir, err := os.MkdirTemp("", "promtop-update-")
	if err != nil {
		return fail(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, installScriptName)
	if err := os.WriteFile(file, script, 0o600); err != nil {
		return fail(err)
	}

	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", file) //nolint:gosec // file is the script just written to our own temp directory
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			return exit.ExitCode()
		}
		return fail(fmt.Errorf("run the install script: %w", err))
	}
	return 0
}
