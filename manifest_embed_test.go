package main

import (
	"testing"

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
	foundDisabledField := false
	for _, f := range manifest.Metadata.ConfigFields {
		if f.Name == "auto_ping_disabled" {
			foundDisabledField = true
			break
		}
	}
	if !foundDisabledField {
		t.Fatal("manifest must declare auto_ping_disabled config field")
	}
}
