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
		t.Fatal("shipped auto_ping_enabled default must be true so a newly enabled plugin starts pinging")
	}
}
