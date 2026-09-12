package autoping

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestDirectActivationTargetsCredentialAndFallsBackModelOnce(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(request HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		if request.Headers.Get("Authorization") != "Bearer token-a" {
			t.Fatalf("authorization = %q", request.Headers.Get("Authorization"))
		}
		if request.Headers.Get("ChatGPT-Account-ID") != "account-codex-a" {
			t.Fatalf("account header = %q", request.Headers.Get("ChatGPT-Account-ID"))
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			t.Fatal(err)
		}
		if body.Model == "gpt-5.5" {
			return HTTPStreamResponse{StatusCode: http.StatusNotFound}, []HTTPStreamChunk{{Payload: []byte(`{"error":{"message":"model not found"}}`), Done: true}}, nil
		}
		return successStream()
	}
	runtime := newTestRuntime(t, host, Options{})
	result := runtime.activate(t.Context(), testConfig(t, runtime, ""), file, AuthMaterial{AccessToken: "token-a", AccountID: "account-codex-a"})
	if !result.Success || result.Model != "gpt-5.6-luna" || result.Transport != TransportDirectHTTP {
		t.Fatalf("result = %#v", result)
	}
	if len(host.streamRequests) != 2 {
		t.Fatalf("stream requests = %d, want 2", len(host.streamRequests))
	}
}

func TestEvaluateCodexResponseSurfacesUpstreamDetail(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantSucc   bool
		wantFail   FailureKind
		wantMsg    string
	}{
		{
			name:     "detail field in 400",
			status:   http.StatusBadRequest,
			body:     `{"detail":"Unsupported parameter: max_output_tokens"}`,
			wantSucc: false,
			wantFail: FailureModel,
			wantMsg:  "Codex returned HTTP 400: Unsupported parameter: max_output_tokens",
		},
		{
			name:     "error object in 400",
			status:   http.StatusBadRequest,
			body:     `{"error":{"message":"Invalid parameter"}}`,
			wantSucc: false,
			wantFail: FailureModel,
			wantMsg:  "Codex returned HTTP 400: Invalid parameter",
		},
		{
			name:     "error string in 400",
			status:   http.StatusBadRequest,
			body:     `{"error":"bad request payload"}`,
			wantSucc: false,
			wantFail: FailureModel,
			wantMsg:  "Codex returned HTTP 400: bad request payload",
		},
		{
			name:     "404 not found",
			status:   http.StatusNotFound,
			body:     `{"detail":"Model not found"}`,
			wantSucc: false,
			wantFail: FailureModel,
			wantMsg:  "Codex returned HTTP 404: Model not found",
		},
		{
			name:     "500 internal server error with detail",
			status:   http.StatusInternalServerError,
			body:     `{"detail":"Internal gateway error"}`,
			wantSucc: false,
			wantFail: FailureRetryable,
			wantMsg:  "Codex returned HTTP 500: Internal gateway error",
		},
		{
			name:     "non-JSON error body",
			status:   http.StatusBadRequest,
			body:     `Bad Gateway Plaintext`,
			wantSucc: false,
			wantFail: FailureModel,
			wantMsg:  "Codex returned HTTP 400",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			succ, fail, msg := evaluateCodexResponse(tc.status, []byte(tc.body))
			if succ != tc.wantSucc || fail != tc.wantFail || msg != tc.wantMsg {
				t.Fatalf("got (%v, %q, %q), want (%v, %q, %q)", succ, fail, msg, tc.wantSucc, tc.wantFail, tc.wantMsg)
			}
		})
	}
}

func TestDirectActivationAdvancesCandidateOnBadRequest(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(request HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			t.Fatal(err)
		}
		if body.Model == "gpt-5.5" {
			return HTTPStreamResponse{StatusCode: http.StatusBadRequest}, []HTTPStreamChunk{{Payload: []byte(`{"detail":"Unsupported parameter"}`), Done: true}}, nil
		}
		return successStream()
	}
	runtime := newTestRuntime(t, host, Options{})
	result := runtime.activate(t.Context(), testConfig(t, runtime, ""), file, AuthMaterial{AccessToken: "token-a"})
	if !result.Success || result.Model != "gpt-5.6-luna" || result.Transport != TransportDirectHTTP {
		t.Fatalf("result = %#v", result)
	}
	if len(host.streamRequests) != 2 {
		t.Fatalf("stream requests = %d, want 2", len(host.streamRequests))
	}
}

func TestDirectActivationPayloadOmitsUnsupportedParameters(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	var capturedBody map[string]any
	host.httpStreamFunc = func(request HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		if err := json.Unmarshal(request.Body, &capturedBody); err != nil {
			t.Fatal(err)
		}
		return successStream()
	}

	runtime := newTestRuntime(t, host, Options{})
	result := runtime.activate(t.Context(), testConfig(t, runtime, ""), file, AuthMaterial{AccessToken: "token-a"})
	if !result.Success {
		t.Fatalf("result = %#v", result)
	}
	assertPayloadOmitsTokens(t, "direct activation", capturedBody)
}

func TestDirectBusinessFailureDoesNotUseSchedulerFallback(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return HTTPStreamResponse{StatusCode: http.StatusUnauthorized}, []HTTPStreamChunk{{Done: true}}, nil
	}
	host.modelExecuteFunc = func(ModelExecuteRequest) (ModelExecuteResponse, error) {
		t.Fatal("scheduler fallback must not run for authentication failure")
		return ModelExecuteResponse{}, nil
	}
	runtime := newTestRuntime(t, host, Options{})
	result := runtime.activate(t.Context(), testConfig(t, runtime, ""), file, AuthMaterial{AccessToken: "token-a"})
	if result.Failure != FailureAuth || len(host.modelRequests) != 0 {
		t.Fatalf("result = %#v, model requests = %d", result, len(host.modelRequests))
	}
}

func TestTransportFailureUsesConfirmedSchedulerFallback(t *testing.T) {
	host := newFakeHost()
	target, targetDocument := credential("codex-a", "token-a", 0)
	peer, peerDocument := credential("codex-b", "token-b", 5)
	host.auths = []AuthFile{target, peer}
	host.docs[target.AuthIndex] = targetDocument
	host.docs[peer.AuthIndex] = peerDocument
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) {
		return HTTPStreamResponse{}, nil, errors.New("stream bridge unavailable")
	}

	runtime := newTestRuntime(t, host, Options{})
	host.modelExecuteFunc = func(request ModelExecuteRequest) (ModelExecuteResponse, error) {
		var captured map[string]any
		if err := json.Unmarshal(request.Body, &captured); err != nil {
			t.Fatal(err)
		}
		assertPayloadOmitsTokens(t, "scheduler", captured)
		raw, _ := json.Marshal(schedulerRequest{
			Candidates: []schedulerCandidate{{ID: "codex-a"}, {ID: "codex-b"}},
			Headers:    request.Headers,
		})
		decision, err := runtime.schedulerPick(raw)
		if err != nil {
			return ModelExecuteResponse{}, err
		}
		if !decision.Handled || decision.AuthID != "codex-a" {
			t.Fatalf("scheduler decision = %#v", decision)
		}
		return ModelExecuteResponse{StatusCode: http.StatusOK, Body: []byte(`{"id":"resp_fallback"}`)}, nil
	}

	result := runtime.activate(context.Background(), testConfig(t, runtime, ""), target, AuthMaterial{AccessToken: "token-a"})
	if !result.Success || result.Transport != TransportSchedulerBoost {
		t.Fatalf("result = %#v", result)
	}
	current, _ := host.GetRuntimeAuth(context.Background(), target.AuthIndex)
	if current.Priority != 0 {
		t.Fatalf("priority was not restored: %d", current.Priority)
	}
	if host.saveCalls < 2 {
		t.Fatalf("save calls = %d, want boost and restore", host.saveCalls)
	}
	if strings.TrimSpace(result.Message) != "" {
		t.Fatalf("unexpected result message: %q", result.Message)
	}
}

func assertPayloadOmitsTokens(t *testing.T, transport string, payload map[string]any) {
	t.Helper()
	if _, exists := payload["max_output_tokens"]; exists {
		t.Fatalf("%s payload must not contain max_output_tokens: %#v", transport, payload)
	}
	if _, exists := payload["max_tokens"]; exists {
		t.Fatalf("%s payload must not contain max_tokens: %#v", transport, payload)
	}
}
