package server

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// openAPISyncConfig lets the test locate both the router source file and the
// OpenAPI document regardless of where `go test` runs from.
type openAPISyncConfig struct {
	routerPath  string // file containing mux.Handle*("METHOD path", ...)
	openAPIPath string
}

func defaultOpenAPISyncConfig(t *testing.T) openAPISyncConfig {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// file is .../internal/server/openapi_sync_test.go; we want project root.
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return openAPISyncConfig{
		routerPath:  filepath.Join(root, "internal", "server", "app.go"),
		openAPIPath: filepath.Join(root, "docs", "openapi.yaml"),
	}
}

// routerOperations scans the router source for the set of (METHOD, path) tuples
// that mux.HandleFunc / mux.Handle register. We intentionally use a regex over
// the source file rather than instantiating App.Handler() — instantiating App
// pulls storage, JWT signing, metrics, and the broker event buffer, none of
// which this test needs. The source-of-truth extraction must match the real
// code, so the regex mirrors the two Go mux calls we actually use.
func routerOperations(t *testing.T, src string) map[string]bool {
	t.Helper()
	// Capture both "GET /livez" (HandleFunc) and "GET /api/v1/foo" (Handle).
	re := regexp.MustCompile(`mux\.Handle(?:Func)?\(\s*"(GET|POST|PUT|DELETE|PATCH) ([^"]+)"`)
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		out[m[1]+" "+m[2]] = true
	}
	return out
}

// openAPIOperations parses docs/openapi.yaml and returns the set of (METHOD,
// path) tuples it documents. Only the verbs the project actually uses are
// considered; unknown verbs produce a parse error so a typo in the OpenAPI
// fails loudly.
func openAPIOperations(t *testing.T, path string) (map[string]bool, error) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	allowed := map[string]bool{
		"get": true, "post": true, "put": true, "delete": true, "patch": true,
	}
	for p, methods := range doc.Paths {
		for verb := range methods {
			v := strings.ToLower(verb)
			if !allowed[v] {
				continue
			}
			out[strings.ToUpper(v)+" "+p] = true
		}
	}
	return out, nil
}

// TestRouterOpenAPISync guards against drift between the registered routes
// (app.go) and the public contract (docs/openapi.yaml). On drift it prints the
// symmetric diff so the next person can fix both sides without guessing.
//
// Excluded routes: the SPA fallback ("/") is a catch-all, not an operation,
// so it must not appear in the OpenAPI document.
func TestRouterOpenAPISync(t *testing.T) {
	cfg := defaultOpenAPISyncConfig(t)

	srcBytes, err := os.ReadFile(cfg.routerPath)
	if err != nil {
		t.Fatalf("read router source: %v", err)
	}
	router := routerOperations(t, string(srcBytes))
	openAPI, err := openAPIOperations(t, cfg.openAPIPath)
	if err != nil {
		t.Fatalf("parse openapi: %v", err)
	}

	// Drop SPA fallback — it is not an API operation.
	delete(router, "GET /")

	var onlyInRouter, onlyInOpenAPI []string
	for op := range router {
		if !openAPI[op] {
			onlyInRouter = append(onlyInRouter, op)
		}
	}
	for op := range openAPI {
		if !router[op] {
			onlyInOpenAPI = append(onlyInOpenAPI, op)
		}
	}
	sort.Strings(onlyInRouter)
	sort.Strings(onlyInOpenAPI)

	if len(onlyInRouter) > 0 || len(onlyInOpenAPI) > 0 {
		var b strings.Builder
		b.WriteString("router ↔ openapi drift detected\n")
		if len(onlyInRouter) > 0 {
			b.WriteString("\nRegistered in router but missing from docs/openapi.yaml (add the operation):\n")
			for _, op := range onlyInRouter {
				b.WriteString("  + " + op + "\n")
			}
		}
		if len(onlyInOpenAPI) > 0 {
			b.WriteString("\nDocumented in docs/openapi.yaml but not registered in router (remove the operation):\n")
			for _, op := range onlyInOpenAPI {
				b.WriteString("  - " + op + "\n")
			}
		}
		t.Fatal(b.String())
	}
}
