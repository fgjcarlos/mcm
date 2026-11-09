// Command gen-support-matrix renders the versioned Mosquitto directive
// support matrix to docs/support-matrix.md so the published matrix can be
// regenerated from the canonical catalog and upstream inventories.
//
// Run with `go run ./scripts/gen-support-matrix`.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
)

func main() {
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dst := filepath.Join(root, "docs", "support-matrix.md")

	md, err := render()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(dst, []byte(md), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", dst)
}

// render builds the markdown of the support matrix for every broker
// version MCM ships a catalog for.
func render() (string, error) {
	catalogs, err := catalog.LoadAll()
	if err != nil {
		return "", err
	}
	versions := make([]string, 0, len(catalogs))
	for v := range catalogs {
		versions = append(versions, v)
	}
	sort.Strings(versions)

	var b strings.Builder
	b.WriteString("# Mosquitto directive support matrix\n\n")
	b.WriteString("Generated from `internal/mosquitto/catalog` — DO NOT EDIT BY HAND.\n")
	b.WriteString("Regenerate with `go run ./scripts/gen-support-matrix`.\n\n")
	b.WriteString("MCM does not promise compatibility with every Mosquitto release. This page states\n")
	b.WriteString("exactly which directives MCM manages for each broker version it supports, and\n")
	b.WriteString("which directives it does not.\n\n")
	b.WriteString("The three levels are:\n\n")
	b.WriteString("- **managed** — MCM owns the directive end to end: it is edited through a\n")
	b.WriteString("  dedicated graphical control, validated against the catalog, and written\n")
	b.WriteString("  through a reviewed apply revision.\n")
	b.WriteString("- **partial** — MCM knows the directive, validates it and preserves it\n")
	b.WriteString("  losslessly on import and round-trip, but exposes no dedicated graphical\n")
	b.WriteString("  control for it. The operator edits it in the advanced root editor.\n")
	b.WriteString("- **unsupported** — the Mosquitto manual documents the directive for that\n")
	b.WriteString("  version but MCM does not manage it. MCM preserves it byte-for-byte on\n")
	b.WriteString("  import so a foreign configuration is never silently dropped, but it\n")
	b.WriteString("  offers no editor and no validation for it.\n\n")
	b.WriteString("A directive MCM does not manage is not a directive the broker rejects. Mosquitto\n")
	b.WriteString("supports it; MCM only declines to administer it.\n\n")

	for _, version := range versions {
		upstream, err := catalog.UpstreamDirectives(version)
		if err != nil {
			return "", err
		}
		matrix, err := catalog.SupportMatrix(catalogs[version], upstream)
		if err != nil {
			return "", err
		}
		counts := matrix.Counts()
		fmt.Fprintf(&b, "## Mosquitto %s\n\n", version)
		fmt.Fprintf(&b, "The Mosquitto %s manual documents %d directives. MCM manages %d of them,\n",
			version, len(upstream), counts[catalog.SupportManaged])
		fmt.Fprintf(&b, "partially supports %d and does not manage %d.\n\n",
			counts[catalog.SupportPartial], counts[catalog.SupportUnsupported])

		for _, level := range []catalog.SupportLevel{
			catalog.SupportManaged,
			catalog.SupportPartial,
			catalog.SupportUnsupported,
		} {
			fmt.Fprintf(&b, "### %s (%d)\n\n", levelTitle(level), counts[level])
			directives := matrix.Directives(level)
			if len(directives) == 0 {
				b.WriteString("None.\n\n")
				continue
			}
			for _, d := range directives {
				fmt.Fprintf(&b, "- `%s`\n", d)
			}
			b.WriteString("\n")
		}

		b.WriteString("### Managed by surface\n\n")
		b.WriteString("| Surface | Directives | Count |\n")
		b.WriteString("| --- | --- | --- |\n")
		for _, surface := range []catalog.Surface{
			catalog.SurfaceSettings,
			catalog.SurfaceListeners,
			catalog.SurfaceBridges,
			catalog.SurfacePlugins,
			catalog.SurfaceAccess,
			catalog.SurfaceAdvanced,
		} {
			names := matrix.SurfaceDirectives(surface)
			if len(names) == 0 {
				continue
			}
			fmt.Fprintf(&b, "| %s | %s | %d |\n",
				surface, "`"+strings.Join(names, "`, `")+"`", len(names))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Why a directive can be unsupported\n\n")
	b.WriteString("MCM manages the configuration it can guarantee. A directive it does not\n")
	b.WriteString("administer stays in the file exactly as the operator wrote it, is reported\n")
	b.WriteString("as unknown during validation, and never blocks an import. See the\n")
	b.WriteString("[configuration workflow](./configuration-workflow.md) for the import, preview,\n")
	b.WriteString("apply and recovery path the matrix applies to.\n")

	return b.String(), nil
}

// levelTitle renders the human title of a support level.
func levelTitle(level catalog.SupportLevel) string {
	switch level {
	case catalog.SupportManaged:
		return "Managed"
	case catalog.SupportPartial:
		return "Partial"
	case catalog.SupportUnsupported:
		return "Unsupported"
	default:
		return string(level)
	}
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
