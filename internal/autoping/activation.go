package autoping

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	codexUsageURL     = "https://chatgpt.com/backend-api/wham/usage"
	codexResponsesURL = "https://chatgpt.com/backend-api/codex/responses"
	maxCapturedBody   = 64 * 1024
)

type FailureKind string

const (
	FailureNone      FailureKind = ""
	FailureTransport FailureKind = "transport"
	FailureTimeout   FailureKind = "timeout"
	FailureAuth      FailureKind = "auth"
	FailureModel     FailureKind = "model"
	FailureRetryable FailureKind = "retryable"
	FailureBusiness  FailureKind = "business"
)

type ActivationResult struct {
	Success    bool
	Model      string
	Transport  string
	StatusCode int
	Failure    FailureKind
	Message    string
}

type operationError struct {
	kind    FailureKind
	message string
}

func (e *operationError) Error() string { return e.message }

func newOperationError(kind FailureKind, message string) error {
	return &operationError{kind: kind, message: message}
}

func operationFailure(err error) FailureKind {
	if typed, ok := errors.AsType[*operationError](err); ok {
		return typed.kind
	}
	return FailureTransport
}

func (r *Runtime) fetchObservation(ctx context.Context, material AuthMaterial, observedAt time.Time) (Observation, error) {
	headers := http.Header{
		"Accept":        {"application/json"},
		"Authorization": {"Bearer " + material.AccessToken},
		"OpenAI-Beta":   {"codex-1"},
		"Originator":    {"codex_cli_rs"},
	}
	if material.AccountID != "" {
		headers.Set("ChatGPT-Account-ID", material.AccountID)
	}
	response, err := r.host.HTTPDo(ctx, HTTPRequest{Method: http.MethodGet, URL: codexUsageURL, Headers: headers})
	if err != nil {
		return Observation{}, newOperationError(FailureTransport, "quota request failed")
	}
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return Observation{}, newOperationError(FailureAuth, "credential authentication failed")
	case response.StatusCode >= 500:
		return Observation{}, newOperationError(FailureRetryable, fmt.Sprintf("quota endpoint returned HTTP %d", response.StatusCode))
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return Observation{}, newOperationError(FailureBusiness, fmt.Sprintf("quota endpoint returned HTTP %d", response.StatusCode))
	}
	observation, err := ParseFiveHourObservation(response.Body, observedAt)
	if err != nil {
		return Observation{}, err
	}
	return observation, nil
}

func (r *Runtime) activate(ctx context.Context, cfg Config, file AuthFile, material AuthMaterial) ActivationResult {
	models := cfg.Models()
	last := ActivationResult{Failure: FailureBusiness, Message: "no model candidate available"}
	for index, model := range models {
		var result ActivationResult
		if cfg.Transport == TransportSchedulerBoost {
			result = r.schedulerActivate(ctx, cfg, file, model)
		} else {
			result = r.directActivate(ctx, cfg, material, model)
			if !result.Success && cfg.SchedulerBoostFallback && (result.Failure == FailureTransport || result.Failure == FailureTimeout) {
				result = r.schedulerActivate(ctx, cfg, file, model)
			}
		}
		last = result
		if result.Success {
			return result
		}
		if result.Failure != FailureModel || cfg.Model != "auto" || index+1 == len(models) {
			return result
		}
	}
	return last
}

func (r *Runtime) directActivate(ctx context.Context, cfg Config, material AuthMaterial, model string) ActivationResult {
	requestBody, err := json.Marshal(map[string]any{
		"model":        model,
		"instructions": "Reply with one token.",
		"input": []map[string]any{{
			"type":    "message",
			"role":    "user",
			"content": []map[string]string{{"type": "input_text", "text": cfg.Prompt}},
		}},
		"max_output_tokens": cfg.MaxOutputTokens,
		"reasoning":         map[string]string{"effort": "none", "summary": "auto"},
		"store":             false,
		"stream":            true,
	})
	if err != nil {
		return ActivationResult{Model: model, Transport: TransportDirectHTTP, Failure: FailureBusiness, Message: "encode Codex request failed"}
	}

	requestCtx, cancel := context.WithTimeoutCause(ctx, cfg.RequestTimeout, errors.New("Codex request timeout"))
	defer cancel()
	headers := http.Header{
		"Accept":        {"text/event-stream"},
		"Authorization": {"Bearer " + material.AccessToken},
		"Content-Type":  {"application/json"},
		"OpenAI-Beta":   {"responses=v1"},
		"Originator":    {"codex_cli_rs"},
		"User-Agent":    {"codex_cli_rs/0.154.0"},
	}
	if material.AccountID != "" {
		headers.Set("ChatGPT-Account-ID", material.AccountID)
	}
	stream, err := r.host.HTTPDoStream(requestCtx, HTTPRequest{
		Method: http.MethodPost, URL: codexResponsesURL, Headers: headers, Body: requestBody,
	})
	if err != nil {
		failure := FailureTransport
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			failure = FailureTimeout
		}
		return ActivationResult{Model: model, Transport: TransportDirectHTTP, Failure: failure, Message: "Codex transport failed"}
	}
	if stream.StreamID == "" {
		return ActivationResult{Model: model, Transport: TransportDirectHTTP, StatusCode: stream.StatusCode, Failure: FailureTransport, Message: "Codex stream bridge unavailable"}
	}
	body, readErr := r.drainHTTPStream(requestCtx, stream.StreamID)
	if readErr != nil {
		failure := FailureTransport
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			failure = FailureTimeout
		}
		return ActivationResult{Model: model, Transport: TransportDirectHTTP, StatusCode: stream.StatusCode, Failure: failure, Message: "Codex stream failed"}
	}
	success, failure, message := evaluateCodexResponse(stream.StatusCode, body)
	return ActivationResult{
		Success: success, Model: model, Transport: TransportDirectHTTP,
		StatusCode: stream.StatusCode, Failure: failure, Message: message,
	}
}

func (r *Runtime) drainHTTPStream(ctx context.Context, streamID string) ([]byte, error) {
	defer func() { _ = r.host.HTTPStreamClose(context.Background(), streamID) }()
	var body []byte
	for {
		if err := ctx.Err(); err != nil {
			return body, err
		}
		chunk, err := r.host.HTTPStreamRead(ctx, streamID)
		if err != nil {
			return body, err
		}
		body = appendCapped(body, chunk.Payload, maxCapturedBody)
		if chunk.Error != "" {
			return body, errors.New("upstream stream error")
		}
		if chunk.Done {
			return body, nil
		}
	}
}

func appendCapped(current, next []byte, limit int) []byte {
	if len(next) >= limit {
		return bytes.Clone(next[len(next)-limit:])
	}
	overflow := len(current) + len(next) - limit
	if overflow > 0 {
		current = bytes.Clone(current[overflow:])
	}
	return append(current, next...)
}

func evaluateCodexResponse(statusCode int, body []byte) (bool, FailureKind, string) {
	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return false, FailureAuth, "credential authentication failed"
	case statusCode == http.StatusNotFound:
		return false, FailureModel, "Codex model not found"
	case statusCode >= 500:
		return false, FailureRetryable, fmt.Sprintf("Codex returned HTTP %d", statusCode)
	case statusCode < 200 || statusCode >= 300:
		if isModelFailure(body) {
			return false, FailureModel, "Codex model is unsupported"
		}
		return false, FailureBusiness, fmt.Sprintf("Codex returned HTTP %d", statusCode)
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return false, FailureBusiness, "Codex response was empty"
	}
	if strings.Contains(trimmed, "event:") || strings.Contains(trimmed, "data:") {
		return evaluateCodexSSE(trimmed)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return false, FailureBusiness, "Codex response was invalid"
	}
	if failure, message := codexFailure(root); failure != FailureNone {
		return false, failure, message
	}
	if hasCodexSuccess(root) {
		return true, FailureNone, ""
	}
	return false, FailureBusiness, "Codex response lacked a success event"
}

func evaluateCodexSSE(stream string) (bool, FailureKind, string) {
	sawCompleted := false
	sawCreated := false
	for line := range strings.SplitSeq(stream, "\n") {
		line = strings.TrimSpace(line)
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event map[string]json.RawMessage
		if json.Unmarshal([]byte(payload), &event) != nil {
			continue
		}
		if failure, message := codexFailure(event); failure != FailureNone {
			return false, failure, message
		}
		eventType := rawString(event["type"])
		switch eventType {
		case "response.completed":
			sawCompleted = true
		case "response.created":
			sawCreated = true
		case "response.failed", "error":
			return false, FailureBusiness, "Codex returned a response error"
		}
		if nestedRaw := event["response"]; len(nestedRaw) > 0 {
			var nested map[string]json.RawMessage
			if json.Unmarshal(nestedRaw, &nested) == nil {
				if failure, message := codexFailure(nested); failure != FailureNone {
					return false, failure, message
				}
				if rawString(nested["status"]) == "completed" && rawString(nested["id"]) != "" {
					sawCompleted = true
				}
				if hasCodexSuccess(nested) {
					sawCreated = true
				}
			}
		}
	}
	if sawCompleted || sawCreated {
		return true, FailureNone, ""
	}
	return false, FailureBusiness, "Codex response lacked a success event"
}

func codexFailure(root map[string]json.RawMessage) (FailureKind, string) {
	raw := root["error"]
	if len(raw) == 0 || string(raw) == "null" {
		return FailureNone, ""
	}
	lowered := strings.ToLower(string(raw))
	switch {
	case strings.Contains(lowered, "model") && (strings.Contains(lowered, "not found") || strings.Contains(lowered, "unsupported") || strings.Contains(lowered, "does not exist") || strings.Contains(lowered, "retired")):
		return FailureModel, "Codex model is unsupported"
	case strings.Contains(lowered, "usage_limit") || strings.Contains(lowered, "usage limit") || strings.Contains(lowered, "quota"):
		return FailureBusiness, "Codex quota is unavailable"
	case strings.Contains(lowered, "unauthorized") || strings.Contains(lowered, "authentication"):
		return FailureAuth, "credential authentication failed"
	default:
		return FailureBusiness, "Codex returned a response error"
	}
}

func hasCodexSuccess(root map[string]json.RawMessage) bool {
	if rawString(root["id"]) != "" {
		return true
	}
	var output []json.RawMessage
	return len(root["output"]) > 0 && json.Unmarshal(root["output"], &output) == nil && len(output) > 0
}

func isModelFailure(body []byte) bool {
	lowered := strings.ToLower(string(body))
	return strings.Contains(lowered, "model") && (strings.Contains(lowered, "not found") || strings.Contains(lowered, "unsupported") || strings.Contains(lowered, "does not exist") || strings.Contains(lowered, "retired"))
}

func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return strings.TrimSpace(value)
}
