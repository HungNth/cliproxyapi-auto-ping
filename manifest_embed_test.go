package main

import (
	"slices"
	"testing"
	"time"

	"github.com/HungNth/cliproxyapi-auto-ping/internal/autoping"
)

// The embedded manifest ID is the build and packaging basename contract.
func TestEmbeddedManifestMatchesPackagingContract(t *testing.T) {
	manifest, err := autoping.ParseManifest(pluginManifest)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "cliproxyapi-auto-ping" {
		t.Fatalf("manifest id = %q, want cliproxyapi-auto-ping", manifest.ID)
	}
	if manifest.Metadata.Version == "" {
		t.Fatal("manifest version is empty")
	}
	if !manifest.Defaults.AutoPingEnabled {
		t.Fatal("shipped auto_ping_disabled default must be false so a newly enabled plugin starts pinging")
	}
	if !slices.Equal(manifest.Defaults.Schedule, []string{"05:00", "10:00", "15:00", "20:00"}) {
		t.Fatalf("defaults schedule = %#v", manifest.Defaults.Schedule)
	}
	if manifest.Defaults.Timezone != "Local" {
		t.Fatalf("defaults timezone = %q", manifest.Defaults.Timezone)
	}
	if manifest.Defaults.RetryCooldown != time.Minute {
		t.Fatalf("defaults retry_cooldown = %v, want 1m", manifest.Defaults.RetryCooldown)
	}

	foundDisabledField := false
	foundScheduleField := false
	foundTimezoneField := false
	for _, f := range manifest.Metadata.ConfigFields {
		switch f.Name {
		case "auto_ping_disabled":
			foundDisabledField = true
		case "schedule":
			foundScheduleField = true
		case "timezone":
			foundTimezoneField = true
		case "scan_interval", "activation_delay", "max_output_tokens":
			t.Fatalf("manifest must not declare obsolete config field %q", f.Name)
		}
	}
	if !foundDisabledField {
		t.Fatal("manifest must declare auto_ping_disabled config field")
	}
	if !foundScheduleField {
		t.Fatal("manifest must declare schedule config field")
	}
	if !foundTimezoneField {
		t.Fatal("manifest must declare timezone config field")
	}
}
