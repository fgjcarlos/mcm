// Command gen-env-table renders internal/config.EnvBindingsMarkdown() to
// internal/config/env_table.md so the docs can be regenerated from the
// canonical table. Run with `go run ./scripts/gen-env-table`.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fgjcarlos/mcm/internal/config"
)

func main() {
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dst := filepath.Join(root, "internal", "config", "env_table.md")

	var b strings.Builder
	b.WriteString("# MCM environment variable reference\n\n")
	b.WriteString("Generated from `internal/config.EnvBindingsMarkdown()` — DO NOT EDIT BY HAND.\n")
	b.WriteString("Regenerate with `go run ./scripts/gen-env-table`.\n\n")
	b.WriteString(config.EnvBindingsMarkdown())
	if err := os.WriteFile(dst, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", dst)
}

func findRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", cwd)
		}
		dir = parent
	}
}
