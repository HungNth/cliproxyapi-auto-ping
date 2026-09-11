package autoping

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func TestManagementRegistersAuthenticatedRoutesOnly(t *testing.T) {
	runtime := NewRuntime(newFakeHost(), Options{})
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
}

func TestManagementStatusNeverExposesCredentialToken(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) { return successStream() }

	cfg := DefaultConfig()
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	runtime := NewRuntime(host, Options{})
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ManualPing(t.Context(), ManualPingRequest{CredentialID: "codex-a"}); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: "/v0/management/auto-ping/status"})
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
