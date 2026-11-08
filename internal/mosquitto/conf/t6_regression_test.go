package conf_test

import (
	"testing"

	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/mosquitto/plugins"
	"github.com/fgjcarlos/mcm/internal/mosquitto/settings"
)

func TestT6RendererSeparatesAppendedSettingsAndPlugins(t *testing.T) {
	confCatalog, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatal(err)
	}
	pluginCatalog, err := plugins.LoadVersion("2.0")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := conf.ParseString("log_dest stdout", "mosquitto.conf")
	if err != nil {
		t.Fatal(err)
	}

	settingsFile, _, err := settings.Apply(parsed, settings.Document{
		Entries: []settings.Entry{{Directive: "persistence", Values: []string{"true"}}},
	}, confCatalog)
	if err != nil {
		t.Fatal(err)
	}
	settingsBytes, err := settingsFile.Render()
	if err != nil {
		t.Fatal(err)
	}
	if want := "log_dest stdout\npersistence true\n"; string(settingsBytes) != want {
		t.Errorf("settings output = %q, want %q", settingsBytes, want)
	}

	pluginFile, _, err := plugins.Apply(parsed, pluginCatalog, plugins.Document{
		Plugins: []plugins.Plugin{{Module: "/tmp/custom.so"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pluginBytes, err := pluginFile.Render()
	if err != nil {
		t.Fatal(err)
	}
	if want := "log_dest stdout\nplugin /tmp/custom.so\n"; string(pluginBytes) != want {
		t.Errorf("plugin output = %q, want %q", pluginBytes, want)
	}
}
