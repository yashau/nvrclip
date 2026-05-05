package config

import "testing"

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
