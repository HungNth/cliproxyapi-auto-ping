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
}
