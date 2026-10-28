// Command promtop shows a live dashboard for a Prometheus /metrics endpoint
// — in the terminal or the browser — and draws one-off snapshots of it,
// choosing a suitable widget for every metric automatically.
package main

import (
	"os"

	"github.com/igorrius/promtop/internal/cli"
)

// version is set by the release build (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	os.Exit(cli.Main(os.Args[1:], version))
}
