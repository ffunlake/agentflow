package main

import (
	"os"

	"agentflow/internal/cli"
)

// Override at build time with -ldflags '-X main.version=<version>'.
var version = "0.0.0-dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, version))
}
