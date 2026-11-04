package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const updateUsage = `promtop update — replace this binary with the latest release

Usage:
  promtop update

Runs the install script (scripts/install.sh, or install.ps1 on Windows) on the
directory this binary is in. It verifies the download's checksum and does nothing
if the latest release is already there. Set PROMTOP_VERSION=v0.5.0 for a specific
release. A copy from a Linux package is left to the package manager.
`

// installScriptBase is where the install scripts are downloaded from. A variable
// so that CI can point a build at the scripts of the commit under test (-ldflags -X).
var installScriptBase = "https://raw.githubusercontent.com/igorrius/promtop/main/scripts/"

func runUpdate(args []string) int {
	fs := flag.NewFlagSet("promtop update", flag.ContinueOnError)
	fs.Usage = func() { _, _ = fmt.Fprint(fs.Output(), updateUsage) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		return usageErr(fmt.Errorf("update takes no arguments, got %q", fs.Arg(0)))
	}

	exe, err := os.Executable()
	if err != nil {
		return fail(fmt.Errorf("cannot find this binary: %w", err))
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	if packageManaged(dir) {
		return fail(fmt.Errorf("%s comes from a Linux package: update it with the package manager", exe))
	}

	ctx, stop := signalContext()
	defer stop()
	script, err := fetchScript(ctx, installScriptBase+installScriptName)
	if err != nil {
		return fail(err)
	}
	return runScript(script, updateEnv(os.Environ(), dir))
}

// packageManaged reports whether dir is where the Linux packages put the binary.
// The install script stays out of these directories too.
func packageManaged(dir string) bool {
	return dir == "/usr/bin" || dir == "/bin"
}

// fetchScript downloads the whole script before anything runs, so that a failed
// or cut off download is an error here rather than half a script executed.
func fetchScript(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "promtop/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download the install script: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download the install script: %s: %s", url, resp.Status)
	}
	script, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("download the install script: %w", err)
	}
	return script, nil
}

// updateEnv points the script at dir: this binary is the one to update, whichever
// promtop comes first on PATH.
func updateEnv(environ []string, dir string) []string {
	const name = "PROMTOP_INSTALL_DIR="
	env := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if !strings.HasPrefix(kv, name) {
			env = append(env, kv)
		}
	}
	return append(env, name+dir)
}
