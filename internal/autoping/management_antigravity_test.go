package autoping

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagementHandlePingAntigravityAllSuccess200(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-mgmt-200")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
		return ModelExecuteResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"type":"response.completed"}`),
		}, nil
	}

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"credential_id": "ag-mgmt-200",
	})
	rawReq, _ := json.Marshal(managementRequest{
		Method: http.MethodPost,
		Path:   runtime.routePath("/ping"),
		Body:   body,
	})

	rawResp, err := runtime.handleManagement(t.Context(), rawReq)
	if err != nil {
		t.Fatal(err)
	}
	if rawResp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 on all-success, got %d: %s", rawResp.StatusCode, string(rawResp.Body))
	}

	var resp ManualPingResponse
	if err := json.Unmarshal(rawResp.Body, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("expected resp.Success = true, got %#v", resp)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 target results, got %d: %#v", len(resp.Results), resp.Results)
	}
	if !resp.Results[0].Success || !resp.Results[1].Success {
		t.Fatalf("expected both target results to succeed: %#v", resp.Results)
	}
}

func TestManagementHandlePingAntigravityMixedResults207(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-mgmt-207")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
		if req.Model == AntigravityModelGemini {
			return ModelExecuteResponse{
				StatusCode: http.StatusOK,
				Body:       []byte(`{"type":"response.completed"}`),
			}, nil
		}
		return ModelExecuteResponse{
			StatusCode: http.StatusBadRequest,
			Body:       []byte(`{"error":{"message":"claude model quota exhausted"}}`),
		}, nil
	}

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"credential_id": "ag-mgmt-207",
	})
	rawReq, _ := json.Marshal(managementRequest{
		Method: http.MethodPost,
		Path:   runtime.routePath("/ping"),
		Body:   body,
	})

	rawResp, err := runtime.handleManagement(t.Context(), rawReq)
	if err != nil {
		t.Fatal(err)
	}
	if rawResp.StatusCode != 207 {
		t.Fatalf("expected HTTP 207 Multi-Status on mixed results, got %d: %s", rawResp.StatusCode, string(rawResp.Body))
	}

	var resp ManualPingResponse
	if err := json.Unmarshal(rawResp.Body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Success {
		t.Fatal("expected overall success = false on mixed results")
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 target results, got %d", len(resp.Results))
	}
	if !resp.Results[0].Success || resp.Results[1].Success {
		t.Fatalf("expected Gemini success and Claude failure: %#v", resp.Results)
	}
}

func TestManagementHandlePingAntigravityAllFailed502(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-mgmt-502")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	host.modelExecuteFunc = func(req ModelExecuteRequest) (ModelExecuteResponse, error) {
		return ModelExecuteResponse{
			StatusCode: http.StatusBadGateway,
			Body:       []byte(`{"error":{"message":"upstream gateway failure"}}`),
		}, nil
	}

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"credential_id": "ag-mgmt-502",
	})
	rawReq, _ := json.Marshal(managementRequest{
		Method: http.MethodPost,
		Path:   runtime.routePath("/ping"),
		Body:   body,
	})

	rawResp, err := runtime.handleManagement(t.Context(), rawReq)
	if err != nil {
		t.Fatal(err)
	}
	if rawResp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected HTTP 502 Bad Gateway on all failures, got %d: %s", rawResp.StatusCode, string(rawResp.Body))
	}
}

func TestManagementHandlePingAntigravityUnsupportedModel400(t *testing.T) {
	host := newFakeHost()
	file, document := antigravityCredential("ag-mgmt-400")
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"credential_id": "ag-mgmt-400",
		"model":         "unsupported-model",
	})
	rawReq, _ := json.Marshal(managementRequest{
		Method: http.MethodPost,
		Path:   runtime.routePath("/ping"),
		Body:   body,
	})

	rawResp, err := runtime.handleManagement(t.Context(), rawReq)
	if err != nil {
		t.Fatal(err)
	}
	if rawResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400 Bad Request on unsupported model, got %d: %s", rawResp.StatusCode, string(rawResp.Body))
	}
}

func TestManagementStatusAndDiagnosticsExposeAntigravityContractsWithoutSecrets(t *testing.T) {
	host := newFakeHost()
	agFile, agDoc := antigravityCredential("ag-status-1")
	codexFile, codexDoc := credential("codex-status-1", "token-codex-123", 1)
	host.auths = []AuthFile{agFile, codexFile}
	host.docs[agFile.AuthIndex] = agDoc
	host.docs[codexFile.AuthIndex] = codexDoc

	statePath := filepath.Join(t.TempDir(), "state.json")
	store, err := LoadStateStore(t.Context(), statePath)
	if err != nil {
		t.Fatal(err)
	}
	geminiReset := time.Date(2026, 10, 14, 16, 0, 0, 0, time.UTC)
	_ = store.Update(t.Context(), "ag-status-1", func(current *CredentialState) {
		current.Provider = ProviderAntigravity
		current.Status = "waiting"
		current.Targets = map[string]TargetState{
			TargetKeyGemini5h: {
				Target:          TargetKeyGemini5h,
				ModelFamily:     "gemini",
				SelectedModel:   AntigravityModelGemini,
				Status:          "waiting",
				ObservedResetAt: geminiReset,
				TargetTriggerAt: geminiReset.Add(30 * time.Second),
			},
		}
	})

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "antigravity_reset_margin: 30s")
	cfg.StatePath = statePath
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	// 1. Status route
	rawStatusReq, _ := json.Marshal(managementRequest{
		Method: http.MethodGet,
		Path:   runtime.routePath("/status"),
	})
	statusResp, err := runtime.handleManagement(t.Context(), rawReqWithoutBody(runtime.routePath("/status")))
	if err != nil {
		t.Fatal(err)
	}
	statusStr := string(statusResp.Body)
	if strings.Contains(statusStr, "token-") || strings.Contains(statusStr, "access_token") {
		t.Fatalf("status must not expose token secrets: %s", statusStr)
	}
	if !strings.Contains(statusStr, "gemini_5h") {
		t.Fatalf("status must expose nested gemini_5h target: %s", statusStr)
	}

	// 2. Diagnostics route
	diagResp, err := runtime.handleManagement(t.Context(), rawReqWithoutBody(runtime.routePath("/diagnostics")))
	if err != nil {
		t.Fatal(err)
	}
	diagStr := string(diagResp.Body)
	if !strings.Contains(diagStr, "v7.3.3") || !strings.Contains(diagStr, "forced_provider") {
		t.Fatalf("diagnostics must note CLIProxyAPI v7.3.3 host pinning requirement: %s", diagStr)
	}
	if !strings.Contains(diagStr, "keepalive") {
		t.Fatalf("diagnostics must note reset-aligned keepalive guarantee: %s", diagStr)
	}
	_ = rawStatusReq
}

func rawReqWithoutBody(path string) []byte {
	raw, _ := json.Marshal(managementRequest{
		Method: http.MethodGet,
		Path:   path,
	})
	return raw
}
