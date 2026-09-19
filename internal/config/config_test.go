package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveChannelNumber(t *testing.T) {
	cfg := Config{NVRs: map[string]NVR{
		"office": {Name: "office", Channels: map[string]string{"1": "front-door"}},
	}}
	channel, err := cfg.ResolveChannel("office", "1")
	if err != nil {
		t.Fatal(err)
	}
	if channel.Number != 1 || channel.NVR != "office" || channel.Name != "front-door" {
		t.Fatalf("unexpected result: %#v", channel)
	}
}

func TestLoadAutoTimeOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nvrclip.toml")
	content := `[office]
type = "dahua"
base_url = "192.0.2.1"
username = "admin"
password = "test"
auto_time_offset = true
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.NVRs["office"].TimeOffsetEnabled() {
		t.Fatal("auto_time_offset was not loaded")
	}
}

// Recorder clocks drift, and an unchecked drift silently returns footage from
// the wrong time, so the correction applies unless a config opts out of it.
func TestTimeOffsetEnabledDefaultsOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nvrclip.toml")
	content := `[office]
type = "dahua"
base_url = "192.0.2.1"
username = "admin"
password = "test"

[warehouse]
type = "dahua"
base_url = "192.0.2.2"
username = "admin"
password = "test"
auto_time_offset = false
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.NVRs["office"].TimeOffsetEnabled() {
		t.Fatal("time offset should be on when the config says nothing")
	}
	if cfg.NVRs["warehouse"].TimeOffsetEnabled() {
		t.Fatal("auto_time_offset = false should turn the correction off")
	}
}

func TestResolveChannelAlias(t *testing.T) {
	cfg := Config{NVRs: map[string]NVR{
		"office": {Name: "office", Channels: map[string]string{"1": "front-door"}},
	}}
	channel, err := cfg.ResolveChannel("office", "front-door")
	if err != nil {
		t.Fatal(err)
	}
	if channel.Number != 1 || channel.Name != "front-door" {
		t.Fatalf("unexpected result: %#v", channel)
	}
}

func TestRejectZeroChannel(t *testing.T) {
	cfg := Config{NVRs: map[string]NVR{"office": {Name: "office"}}}
	if _, err := cfg.ResolveChannel("office", "0"); err == nil {
		t.Fatal("expected channel 0 to fail")
	}
}

func TestResolveGlobalChannel(t *testing.T) {
	cfg := Config{NVRs: map[string]NVR{
		"office": {Name: "office", Channels: map[string]string{"1": "shop cashier"}},
	}}
	channel, err := cfg.ResolveGlobalChannel("shop cashier")
	if err != nil {
		t.Fatal(err)
	}
	if channel.NVR != "office" || channel.Number != 1 {
		t.Fatalf("unexpected result: %#v", channel)
	}
}
