package autoping

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDiagnosticsReportsUsageObservationIntegration(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	request, _ := json.Marshal(managementRequest{
		Method: http.MethodGet,
		Path:   "/v0/management/cliproxyapi-auto-ping/diagnostics",
	})
	response := decodeManagementResponse(t, runtime.Handle(t.Context(), "management.handle", request))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if !strings.Contains(string(response.Body), "/wham/usage") || !strings.Contains(string(response.Body), "target_trigger_at") {
		t.Fatalf("diagnostics omitted dynamic usage scheduling details: %s", response.Body)
	}
}
