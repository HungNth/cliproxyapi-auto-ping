package autoping

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLoadStateStoreRejectsPriorVersionsWithActionableError(t *testing.T) {
	for _, version := range []int{1, 2} {
		path := filepath.Join(t.TempDir(), "state.json")
		data := []byte(`{"version":` + strconv.Itoa(version) + `,"credentials":{}}`)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadStateStore(t.Context(), path)
		if err == nil {
			t.Fatalf("version %d: expected error", version)
		}
		if !strings.Contains(err.Error(), "unsupported state schema version: please remove stale state file") {
			t.Fatalf("version %d: error = %q", version, err)
		}
	}
}
