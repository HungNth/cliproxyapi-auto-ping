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
	runtime := NewRuntime(host, Options{})
	result := runtime.activate(t.Context(), DefaultConfig(), file, AuthMaterial{AccessToken: "token-a", AccountID: "account-codex-a"})
	if !result.Success || result.Model != "gpt-5.6-luna" || result.Transport != TransportDirectHTTP {
		t.Fatalf("result = %#v", result)
	}
	if len(host.streamRequests) != 2 {
		t.Fatalf("stream requests = %d, want 2", len(host.streamRequests))
	}
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
	runtime := NewRuntime(host, Options{})
	result := runtime.activate(t.Context(), DefaultConfig(), file, AuthMaterial{AccessToken: "token-a"})
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

	runtime := NewRuntime(host, Options{})
	host.modelExecuteFunc = func(request ModelExecuteRequest) (ModelExecuteResponse, error) {
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

	result := runtime.activate(context.Background(), DefaultConfig(), target, AuthMaterial{AccessToken: "token-a"})
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
