package plugins_test

import (
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/mosquitto/plugins"
)

func loadCatalog(t *testing.T, version string) *plugins.Catalog {
	t.Helper()
	cat, err := plugins.LoadVersion(version)
	if err != nil {
		t.Fatalf("load plugin catalog %s: %v", version, err)
	}
	return cat
}

func parse(t *testing.T, body string) *conf.File {
	t.Helper()
	f, err := conf.Parse(strings.NewReader(body), "mosquitto.conf")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return f
}

func render(t *testing.T, f *conf.File) string {
	t.Helper()
	out, err := f.Render()
	if err != nil {
		t.Fatalf("render config: %v", err)
	}
	return string(out)
}

// TestCatalogKnowsDynamicSecurity pins that the catalog of every
// supported broker version carries the dynamic security plugin with the
// option the plugin needs.
func TestCatalogKnowsDynamicSecurity(t *testing.T) {
	all, err := plugins.LoadAll()
	if err != nil {
		t.Fatalf("load all plugin catalogs: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no plugin catalog was embedded")
	}
	for version, cat := range all {
		spec, ok := cat.Lookup("dynamic-security")
		if !ok {
			t.Fatalf("catalog %s does not know the dynamic-security plugin", version)
		}
		if spec.Module == "" {
			t.Errorf("catalog %s: the dynamic-security plugin has no module path", version)
		}
		if spec.ReloadKind != plugins.ReloadRestart {
			t.Errorf("catalog %s: a plugin must be loaded at start-up, got reload kind %q", version, spec.ReloadKind)
		}
		opt, ok := spec.Option("config_file")
		if !ok {
			t.Fatalf("catalog %s: the dynamic-security plugin has no config_file option", version)
		}
		if !opt.Required {
			t.Errorf("catalog %s: the config_file option must be required", version)
		}
	}
}

// TestCatalogInheritsBetweenVersions pins that a newer version inherits
// the plugin set of the older one.
func TestCatalogInheritsBetweenVersions(t *testing.T) {
	cat := loadCatalog(t, "2.1")
	if _, ok := cat.Lookup("dynamic-security"); !ok {
		t.Fatal("the 2.1 catalog does not inherit the dynamic-security plugin")
	}
	if cat.VersionName() != "2.1" {
		t.Errorf("catalog version = %q, want 2.1", cat.VersionName())
	}
}

// TestExtractReadsPluginsAndOptions pins that the plugin directives of a
// file become a document, that a known plugin carries its id and that the
// options the catalog does not know are preserved apart.
func TestExtractReadsPluginsAndOptions(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	body := `# the broker configuration
persistence true

plugin /usr/lib/mosquitto_dynamic_security.so
plugin_opt_config_file /mosquitto/config/dynamic-security.json
plugin_opt_something_unknown keep-me

plugin /usr/lib/other_plugin.so
plugin_opt_whatever 1
`
	doc := plugins.Extract(parse(t, body), cat)
	if len(doc.Plugins) != 2 {
		t.Fatalf("extracted %d plugins, want 2", len(doc.Plugins))
	}
	ds := doc.Plugins[0]
	if ds.ID != "dynamic-security" {
		t.Errorf("plugin id = %q, want dynamic-security", ds.ID)
	}
	if got := ds.Options["config_file"]; got != "/mosquitto/config/dynamic-security.json" {
		t.Errorf("config_file = %q", got)
	}
	if got := ds.UnknownOptions["something_unknown"]; got != "keep-me" {
		t.Errorf("unknown option = %q, want keep-me", got)
	}
	other := doc.Plugins[1]
	if other.ID != "" {
		t.Errorf("an unknown plugin must not get an id, got %q", other.ID)
	}
	if got := other.UnknownOptions["whatever"]; got != "1" {
		t.Errorf("unknown option = %q, want 1", got)
	}
}

// TestApplyKeepsUnknownOptionsAndLines pins that saving the document
// without an edit leaves the file byte-for-byte identical, and that an
// unknown plugin and its options survive a change.
func TestApplyKeepsUnknownOptionsAndLines(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	body := `# a comment
persistence true

plugin /usr/lib/mosquitto_dynamic_security.so
plugin_opt_config_file /mosquitto/config/dynamic-security.json
plugin_opt_something_unknown keep-me

plugin /usr/lib/other_plugin.so
plugin_opt_whatever 1
`
	f := parse(t, body)
	doc := plugins.Extract(f, cat)
	updated, changed, err := plugins.Apply(f, cat, doc)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("changed directives = %v, want none", changed)
	}
	if got := render(t, updated); got != body {
		t.Errorf("the file changed without an edit:\n%s", got)
	}
}

// TestApplyWritesManagedOption pins that the managed option of a known
// plugin is rewritten and that the unknown options around it survive.
func TestApplyWritesManagedOption(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	body := `plugin /usr/lib/mosquitto_dynamic_security.so
plugin_opt_config_file /mosquitto/config/dynamic-security.json
plugin_opt_something_unknown keep-me

plugin /usr/lib/other_plugin.so
`
	f := parse(t, body)
	doc := plugins.Extract(f, cat)
	doc.Plugins[0].Options["config_file"] = "/mosquitto/config/other.json"
	updated, changed, err := plugins.Apply(f, cat, doc)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := render(t, updated)
	if !strings.Contains(got, "plugin_opt_config_file /mosquitto/config/other.json") {
		t.Errorf("the managed option was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "plugin_opt_something_unknown keep-me") {
		t.Errorf("the unknown option was dropped:\n%s", got)
	}
	if !strings.Contains(got, "plugin /usr/lib/other_plugin.so") {
		t.Errorf("the unknown plugin was dropped:\n%s", got)
	}
	if len(changed) != 1 || changed[0] != "plugin_opt_config_file" {
		t.Errorf("changed = %v, want [plugin_opt_config_file]", changed)
	}
}

// TestApplyAddsPlugin pins that a plugin that is not in the file yet is
// added with its managed options.
func TestApplyAddsPlugin(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	body := "persistence true\n"
	f := parse(t, body)
	doc := plugins.Document{Plugins: []plugins.Plugin{{
		ID:      "dynamic-security",
		Module:  "/usr/lib/mosquitto_dynamic_security.so",
		Options: map[string]string{"config_file": "/mosquitto/config/dynamic-security.json"},
	}}}
	updated, changed, err := plugins.Apply(f, cat, doc)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := render(t, updated)
	if !strings.Contains(got, "plugin /usr/lib/mosquitto_dynamic_security.so\n") {
		t.Errorf("the plugin was not added:\n%s", got)
	}
	if !strings.Contains(got, "plugin_opt_config_file /mosquitto/config/dynamic-security.json\n") {
		t.Errorf("the option was not added:\n%s", got)
	}
	if !strings.Contains(got, "persistence true") {
		t.Errorf("the rest of the file was dropped:\n%s", got)
	}
	if len(changed) == 0 {
		t.Error("the change set is empty")
	}
}

// TestApplyAddsUnknownPlugin pins that an unknown plugin the file does
// not carry yet is added with its options.
//
// A document that names a plugin MCM does not know must never panic: the
// file may carry no plugin at all, and the match of the document with
// the file then has nothing to point at.
func TestApplyAddsUnknownPlugin(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	body := "persistence true\n"
	f := parse(t, body)
	doc := plugins.Document{Plugins: []plugins.Plugin{{
		Module:         "/usr/lib/my_custom_plugin.so",
		UnknownOptions: map[string]string{"vendor_flag": "keep-me"},
	}}}
	updated, changed, err := plugins.Apply(f, cat, doc)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := render(t, updated)
	if !strings.Contains(got, "plugin /usr/lib/my_custom_plugin.so\n") {
		t.Errorf("the unknown plugin was not added:\n%s", got)
	}
	if !strings.Contains(got, "plugin_opt_vendor_flag keep-me\n") {
		t.Errorf("the unknown option was not added:\n%s", got)
	}
	if !strings.Contains(got, "persistence true") {
		t.Errorf("the rest of the file was dropped:\n%s", got)
	}
	if len(changed) == 0 {
		t.Error("the change set is empty")
	}
}

// TestApplyKeepsExistingPluginWhenUnknownIsAdded pins that an unknown
// plugin the document adds does not take the place of the plugin the
// file already carries.
func TestApplyKeepsExistingPluginWhenUnknownIsAdded(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	body := `plugin /usr/lib/mosquitto_dynamic_security.so
plugin_opt_config_file /mosquitto/config/dynamic-security.json
`
	f := parse(t, body)
	doc := plugins.Document{Plugins: []plugins.Plugin{
		{
			ID:      "dynamic-security",
			Module:  "/usr/lib/mosquitto_dynamic_security.so",
			Options: map[string]string{"config_file": "/mosquitto/config/dynamic-security.json"},
		},
		{Module: "/usr/lib/my_custom_plugin.so"},
	}}
	updated, _, err := plugins.Apply(f, cat, doc)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := render(t, updated)
	if !strings.Contains(got, "plugin /usr/lib/mosquitto_dynamic_security.so\n") {
		t.Errorf("the known plugin was dropped:\n%s", got)
	}
	if !strings.Contains(got, "plugin_opt_config_file /mosquitto/config/dynamic-security.json\n") {
		t.Errorf("the managed option was dropped:\n%s", got)
	}
	if !strings.Contains(got, "plugin /usr/lib/my_custom_plugin.so\n") {
		t.Errorf("the unknown plugin was not added:\n%s", got)
	}
}

// TestApplyAddsUnknownPluginNextToAKnownOne pins that an unknown plugin
// is added even when the file already carries plugins, and that the
// known ones keep their options.
func TestApplyAddsUnknownPluginNextToAKnownOne(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	body := `plugin /usr/lib/mosquitto_dynamic_security.so
plugin_opt_config_file /mosquitto/config/dynamic-security.json
`
	f := parse(t, body)
	doc := plugins.Extract(f, cat)
	doc.Plugins = append(doc.Plugins, plugins.Plugin{Module: "/usr/lib/my_custom_plugin.so"})
	updated, _, err := plugins.Apply(f, cat, doc)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := render(t, updated)
	if !strings.Contains(got, "plugin_opt_config_file /mosquitto/config/dynamic-security.json\n") {
		t.Errorf("the managed option was dropped:\n%s", got)
	}
	if !strings.Contains(got, "plugin /usr/lib/my_custom_plugin.so\n") {
		t.Errorf("the unknown plugin was not added:\n%s", got)
	}
}

// TestValidateRefusesBrokenUnknownOption pins that an option that cannot
// be rendered as a directive is refused instead of breaking the file.
func TestValidateRefusesBrokenUnknownOption(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	doc := plugins.Document{Plugins: []plugins.Plugin{{
		Module:         "/usr/lib/my_custom_plugin.so",
		UnknownOptions: map[string]string{"bad name": "value"},
	}}}
	issues := plugins.Validate(doc, cat)
	if !plugins.HasErrors(issues) {
		t.Fatal("a broken option name was accepted")
	}
	if issues[0].Option != "bad name" {
		t.Errorf("option = %q, want %q", issues[0].Option, "bad name")
	}
}

// TestValidateRefusesBrokenUnknownOptionValue pins that an option value
// with a line break is refused.
func TestValidateRefusesBrokenUnknownOptionValue(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	doc := plugins.Document{Plugins: []plugins.Plugin{{
		Module:         "/usr/lib/my_custom_plugin.so",
		UnknownOptions: map[string]string{"vendor_flag": "one\ntwo"},
	}}}
	if issues := plugins.Validate(doc, cat); !plugins.HasErrors(issues) {
		t.Fatal("a broken option value was accepted")
	}
}

// TestApplyRemovesPlugin pins that a plugin the operator removed from
// the document disappears from the file.
func TestApplyRemovesPlugin(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	body := `plugin /usr/lib/mosquitto_dynamic_security.so
plugin_opt_config_file /mosquitto/config/dynamic-security.json
`
	f := parse(t, body)
	updated, changed, err := plugins.Apply(f, cat, plugins.Document{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := render(t, updated); got != "" {
		t.Errorf("the plugin was not removed:\n%s", got)
	}
	if len(changed) == 0 {
		t.Error("the change set is empty")
	}
}

// TestValidateRefusesMissingRequiredOption pins that a plugin without
// the option it needs is refused.
func TestValidateRefusesMissingRequiredOption(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	doc := plugins.Document{Plugins: []plugins.Plugin{{
		ID:     "dynamic-security",
		Module: "/usr/lib/mosquitto_dynamic_security.so",
	}}}
	issues := plugins.Validate(doc, cat)
	if !plugins.HasErrors(issues) {
		t.Fatal("a plugin without its required option was accepted")
	}
	found := false
	for _, issue := range issues {
		if issue.Kind == plugins.IssueKindMissingOption && issue.Option == "config_file" {
			found = true
		}
	}
	if !found {
		t.Errorf("issues = %v, want a missing_option finding for config_file", issues)
	}
}

// TestValidateRefusesUnknownPlugin pins that a plugin the catalog does not
// know cannot be managed.
func TestValidateRefusesUnknownPlugin(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	doc := plugins.Document{Plugins: []plugins.Plugin{{
		ID:     "not-a-plugin",
		Module: "/usr/lib/not-a-plugin.so",
	}}}
	issues := plugins.Validate(doc, cat)
	if len(issues) != 1 || issues[0].Kind != plugins.IssueKindUnknownPlugin {
		t.Fatalf("issues = %v, want one unknown_plugin finding", issues)
	}
}

// TestExtractMatchesModuleByFileName pins that the plugin is recognised
// whatever directory it lives in, because the directory depends on the
// distribution.
func TestExtractMatchesModuleByFileName(t *testing.T) {
	cat := loadCatalog(t, "2.0")
	body := "plugin /usr/lib/x86_64-linux-gnu/mosquitto_dynamic_security.so\n"
	doc := plugins.Extract(parse(t, body), cat)
	if len(doc.Plugins) != 1 || doc.Plugins[0].ID != "dynamic-security" {
		t.Fatalf("the plugin was not recognised: %+v", doc.Plugins)
	}
}
