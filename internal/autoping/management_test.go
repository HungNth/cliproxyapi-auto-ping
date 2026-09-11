package autoping

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagementRegistersAuthenticatedRoutesOnly(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	raw := runtime.Handle(t.Context(), "management.register", nil)
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatal("management registration failed")
	}
	var registration struct {
		Routes    []managementRoute `json:"Routes"`
		Resources []any             `json:"Resources"`
	}
	if err := json.Unmarshal(envelope.Result, &registration); err != nil {
		t.Fatal(err)
	}
	if len(registration.Routes) != 3 || len(registration.Resources) != 0 {
		t.Fatalf("registration = %#v", registration)
	}
	for _, route := range registration.Routes {
		if !strings.HasPrefix(route.Path, "/cliproxyapi-auto-ping/") {
			t.Fatalf("route %q does not use the manifest ID prefix", route.Path)
		}
	}
}

func TestManagementStatusNeverExposesCredentialToken(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) { return successStream() }

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ManualPing(t.Context(), ManualPingRequest{CredentialID: "codex-a"}); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: "/v0/management/cliproxyapi-auto-ping/status"})
	raw := runtime.Handle(t.Context(), "management.handle", request)
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	var response managementResponse
	if err := json.Unmarshal(envelope.Result, &response); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(response.Body, []byte("token-a")) {
		t.Fatalf("management response exposed token: %s", response.Body)
	}
}

func TestManagementStatusReportsManifestIdentity(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	request, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: "/v0/management/cliproxyapi-auto-ping/status"})
	raw := runtime.Handle(t.Context(), "management.handle", request)
	response := decodeManagementResponse(t, raw)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var payload struct {
		Plugin  string `json:"plugin"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Plugin != "cliproxyapi-auto-ping" || payload.Version != "0.1.0" {
		t.Fatalf("status identity = %#v", payload)
	}
}

func TestManagementRejectsObsoleteRoutePrefix(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	for _, path := range []string{"/v0/management/auto-ping/status", "/v0/management/auto-ping/diagnostics"} {
		request, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: path})
		response := decodeManagementResponse(t, runtime.Handle(t.Context(), "management.handle", request))
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("obsolete route %q returned %d, want 404", path, response.StatusCode)
		}
	}
}

func decodeManagementResponse(t *testing.T, raw []byte) managementResponse {
	t.Helper()
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	var response managementResponse
	if err := json.Unmarshal(envelope.Result, &response); err != nil {
		t.Fatal(err)
	}
	return response
}
