// Package server_test contains a guard test that ensures the OpenAPI
// specification in docs/openapi.yaml stays in sync with the HTTP routes
// registered by App.buildRouter. The test fails on three conditions:
//
//  1. The spec declares a path that is not registered in the router
//     (i.e. the spec claims an endpoint that the server does not
//     actually serve).
//  2. The router registers a path that the spec does not document
//     (i.e. the server has grown an endpoint that was not added to
//     the public contract — the failure mode originally reported in
//     issue #180).
//  3. The total number of unique top-level paths drifts from the count
//     hardcoded below, which the test updates whenever a new endpoint
//     is added on purpose.
//
// The test is intentionally a pure text/regex check: it does not
// instantiate App, does not hit the network, and runs as part of
// `go test ./internal/server/...` in CI.
package server_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const (
	// expectedPathCount mirrors the unique top-level paths that the
	// router registers in internal/server/app.go. When you add a new
	// route, update both the router AND this number AND the OpenAPI
	// spec. CI will catch any drift.
	expectedPathCount = 29
)

var routePattern = regexp.MustCompile(`mux\.(Handle|HandleFunc)\("[A-Z]+ (/[^"]*)"`)

// pathPattern matches top-level OpenAPI path keys, e.g.:
//   /api/v1/mqtt-users/{id}:
var pathPattern = regexp.MustCompile(`(?m)^  (/\S+?):\s*$`)

func TestOpenAPISpecMatchesRouter(t *testing.T) {
	root := repoRoot(t)
	specBytes, err := os.ReadFile(filepath.Join(root, "docs", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	appBytes, err := os.ReadFile(filepath.Join(root, "internal", "server", "app.go"))
	if err != nil {
		t.Fatalf("read internal/server/app.go: %v", err)
	}

	specPaths := extractPaths(string(specBytes))
	routerPaths := extractRouterPaths(string(appBytes))

	if got, want := len(specPaths), expectedPathCount; got != want {
		t.Errorf("openapi.yaml has %d top-level paths, expected %d; update expectedPathCount and verify both spec and router are intentional",
			got, want)
	}

	// 1. Path declared in the spec but missing from the router.
	for p := range specPaths {
		if _, ok := routerPaths[p]; !ok {
			t.Errorf("spec declares %q but the router does not register it", p)
		}
	}
	// 2. Path registered in the router but missing from the spec.
	for p := range routerPaths {
		if _, ok := specPaths[p]; !ok {
			t.Errorf("router registers %q but the spec does not document it", p)
		}
	}
}

func extractPaths(src string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, m := range pathPattern.FindAllStringSubmatch(src, -1) {
		out[m[1]] = struct{}{}
	}
	return out
}

func extractRouterPaths(src string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, m := range routePattern.FindAllStringSubmatch(src, -1) {
		out[m[2]] = struct{}{}
	}
	return out
}

// repoRoot walks up from this test file's directory until it finds
// go.mod, so the test works regardless of the working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find repo root (go.mod)")
	return ""
}

// TestRouterPathsAreSubset is a fast smoke check: every path the
// router claims to serve must appear in the spec. The two-way check
// above is the source of truth; this exists so a quick `go test -run
// RouterPathsAreSubset` catches the most common drift direction
// (router added, spec forgotten) without producing the full diff.
func TestRouterPathsAreSubset(t *testing.T) {
	root := repoRoot(t)
	appBytes, err := os.ReadFile(filepath.Join(root, "internal", "server", "app.go"))
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}
	specBytes, err := os.ReadFile(filepath.Join(root, "docs", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}

	routerPaths := extractRouterPaths(string(appBytes))
	specPaths := extractPaths(string(specBytes))

	var missing []string
	for p := range routerPaths {
		if _, ok := specPaths[p]; !ok {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("router registers %d paths that the spec does not document:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}
