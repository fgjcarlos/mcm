package main

import (
	"os"

	"github.com/fgjcarlos/mcm/internal/cli"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cmd := cli.NewRootCommand(cli.VersionInfo{
		Version: version,
		Commit:  commit,
		Date:    date,
	})

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
