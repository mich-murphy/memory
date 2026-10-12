// Command agent-sessions collects coding-agent session files into one bucket.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/mich-murphy/memory/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Main(ctx, os.Args[1:], version, cli.IO{Stdout: os.Stdout, Stderr: os.Stderr})
	stop()
	os.Exit(code)
}
