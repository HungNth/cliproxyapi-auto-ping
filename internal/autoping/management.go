package autoping

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

const pluginID = "auto-ping"

type rpcEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type registrationResult struct {
	SchemaVersion int             `json:"schema_version"`
	Metadata      pluginMetadata  `json:"metadata"`
	Capabilities  map[string]bool `json:"capabilities"`
}

type pluginMetadata struct {
	Name             string        `json:"Name"`
	Version          string        `json:"Version"`
	Author           string        `json:"Author"`
	GitHubRepository string        `json:"GitHubRepository"`
	Description      string        `json:"Description"`
	ConfigFields     []configField `json:"ConfigFields"`
}

type configField struct {
	Name         string   `json:"Name"`
	Type         string   `json:"Type"`
	EnumValues   []string `json:"EnumValues,omitempty"`
	Description  string   `json:"Description"`
	DefaultValue any      `json:"DefaultValue,omitempty"`
}

type managementRoute struct {
	Method      string `json:"Method"`
	Path        string `json:"Path"`
	Description string `json:"Description,omitempty"`
}

type managementRegistration struct {
	Routes []managementRoute `json:"Routes"`
}

type managementRequest struct {
	Method         string              `json:"Method"`
	Path           string              `json:"Path"`
	Headers        http.Header         `json:"Headers"`
	Query          map[string][]string `json:"Query"`
	Body           []byte              `json:"Body"`
	HostCallbackID string              `json:"host_callback_id,omitempty"`
}

type managementResponse struct {
	StatusCode int         `json:"StatusCode"`
	Headers    http.Header `json:"Headers,omitempty"`
	Body       []byte      `json:"Body,omitempty"`
}

type statusPayload struct {
	Plugin   string            `json:"plugin"`
	Version  string            `json:"version"`
	AutoPing statusConfig      `json:"auto_ping"`
	Accounts []CredentialState `json:"accounts"`
}

type statusConfig struct {
	Enabled                 bool     `json:"enabled"`
	ScanInterval            string   `json:"scan_interval"`
	ActivationDelay         string   `json:"activation_delay"`
	RetryCooldown           string   `json:"retry_cooldown"`
	MaxConcurrency          int      `json:"max_concurrency"`
	RequestTimeout          string   `json:"request_timeout"`
	Model                   string   `json:"model"`
	ModelCandidates         []string `json:"model_candidates,omitempty"`
	Transport               string   `json:"transport"`
	SchedulerBoostFallback  bool     `json:"scheduler_boost_fallback"`
	ExcludedCredentialCount int      `json:"excluded_credential_count"`
}

type diagnosticsPayload struct {
	Status                statusPayload `json:"status"`
	RequiredHostCallbacks []string      `json:"required_host_callbacks"`
	Notes                 []string      `json:"notes"`
}

func (r *Runtime) Handle(ctx context.Context, method string, request []byte) []byte {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		configData, err := lifecycleConfig(request)
		if err != nil {
			return failureEnvelope("invalid_config", err.Error())
		}
		cfg, err := ParseConfig(configData)
		if err != nil {
			return failureEnvelope("invalid_config", err.Error())
		}
		if err := r.Configure(ctx, cfg); err != nil {
			return failureEnvelope("configure_failed", safeErrorMessage(err.Error()))
		}
		return successEnvelope(r.registration())
	case "plugin.shutdown":
		if err := r.Shutdown(ctx); err != nil {
			return failureEnvelope("shutdown_failed", err.Error())
		}
		return successEnvelope(map[string]bool{"shutdown": true})
	case "management.register":
		return successEnvelope(managementRegistration{Routes: []managementRoute{
			{Method: http.MethodGet, Path: "/auto-ping/status", Description: "Show Codex five-hour auto-ping status."},
			{Method: http.MethodGet, Path: "/auto-ping/diagnostics", Description: "Explain per-credential auto-ping decisions."},
			{Method: http.MethodPost, Path: "/auto-ping/ping", Description: "Send a manual targeted Codex ping."},
		}})
	case "management.handle":
		response, err := r.handleManagement(ctx, request)
		if err != nil {
			return failureEnvelope("management_failed", safeErrorMessage(err.Error()))
		}
		return successEnvelope(response)
	case "scheduler.pick":
		response, err := r.schedulerPick(request)
		if err != nil {
			return failureEnvelope("scheduler_target_missing", err.Error())
		}
		return successEnvelope(response)
	default:
		return failureEnvelope("unknown_method", "unknown method: "+method)
	}
}

func (r *Runtime) registration() registrationResult {
	return registrationResult{
		SchemaVersion: 1,
		Metadata: pluginMetadata{
			Name:             "Codex 5h Auto-Ping",
			Version:          r.version,
			Author:           "HungNth",
			GitHubRepository: "https://github.com/HungNth/cliproxyapi-auto-ping",
			Description:      "Starts inactive Codex rolling five-hour windows with one minimal targeted request.",
			ConfigFields: []configField{
				{Name: "auto_ping_enabled", Type: "boolean", Description: "Explicit opt-in for background Codex inference requests (Default: false).", DefaultValue: false},
				{Name: "scan_interval", Type: "string", Description: "Quota scan interval as a Go duration, for example 1m, 30s (Default: 1m).", DefaultValue: "1m"},
				{Name: "activation_delay", Type: "string", Description: "Safety delay after a fixed reset boundary before pinging (Default: 5s).", DefaultValue: "5s"},
				{Name: "retry_cooldown", Type: "string", Description: "Cooldown delay before retrying after an activation failure (Default: 15m).", DefaultValue: "15m"},
				{Name: "max_concurrency", Type: "integer", Description: "Maximum number of credentials processed concurrently (Default: 1).", DefaultValue: 1},
				{Name: "request_timeout", Type: "string", Description: "Timeout for quota observation and inference requests (Default: 60s).", DefaultValue: "60s"},
				{Name: "prompt", Type: "string", Description: "Minimal prompt text sent to Codex (Default: ping).", DefaultValue: "ping"},
				{Name: "max_output_tokens", Type: "integer", Description: "Maximum output tokens requested from Codex (Default: 1).", DefaultValue: 1},
				{Name: "model", Type: "string", Description: "Explicit model name or 'auto' (Default: auto).", DefaultValue: "auto"},
				{Name: "model_candidates", Type: "array", Description: "Ordered list of model candidates to try when model=auto (Default: [gpt-5.5, gpt-5.6-luna]).", DefaultValue: []string{"gpt-5.5", "gpt-5.6-luna"}},
				{Name: "transport", Type: "enum", EnumValues: []string{TransportDirectHTTP, TransportSchedulerBoost}, Description: "Primary activation transport: direct_http or scheduler_boost (Default: direct_http).", DefaultValue: TransportDirectHTTP},
				{Name: "scheduler_boost_fallback", Type: "boolean", Description: "Fall back to scheduler_boost only on transport/host failures (Default: true).", DefaultValue: true},
				{Name: "exclude_credentials", Type: "array", Description: "List of credential IDs excluded from automatic pings (Default: []).", DefaultValue: []string{}},
				{Name: "state_path", Type: "string", Description: "Path to the persistent state JSON file (Default: auto-ping/state.json).", DefaultValue: "auto-ping/state.json"},
			},
		},
		Capabilities: map[string]bool{"management_api": true, "scheduler": true},
	}
}

func lifecycleConfig(request []byte) ([]byte, error) {
	if len(request) == 0 {
		return nil, nil
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(request, &wire); err != nil {
		return nil, fmt.Errorf("decode lifecycle request: %w", err)
	}
	raw := wire["config_yaml"]
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		var bytesValue []byte
		if bytesErr := json.Unmarshal(raw, &bytesValue); bytesErr != nil {
			return nil, errors.New("config_yaml must be a string or byte array")
		}
		return bytesValue, nil
	}
	if strings.ContainsAny(text, ":\n{") {
		return []byte(text), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(text)
	if err == nil && utf8.Valid(decoded) {
		return decoded, nil
	}
	return []byte(text), nil
}

func (r *Runtime) handleManagement(ctx context.Context, raw []byte) (managementResponse, error) {
	var request managementRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return jsonManagement(http.StatusBadRequest, map[string]string{"error": "invalid management request"}), nil
	}
	path := strings.TrimSuffix(request.Path, "/")
	switch {
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/auto-ping/status"):
		return jsonManagement(http.StatusOK, r.status()), nil
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/auto-ping/diagnostics"):
		return jsonManagement(http.StatusOK, diagnosticsPayload{
			Status: r.status(),
			RequiredHostCallbacks: []string{
				"host.auth.list", "host.auth.get", "host.auth.get_runtime", "host.auth.save",
				"host.http.do", "host.http.do_stream", "host.http.stream_read", "host.http.stream_close",
				"host.model.execute", "host.log",
			},
			Notes: []string{
				"Auto-ping never logs or persists credential secrets.",
				"Exactly-once delivery is impossible without upstream idempotency; rare crash duplicates are accepted.",
			},
		}), nil
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/auto-ping/ping"):
		var input ManualPingRequest
		if err := json.Unmarshal(request.Body, &input); err != nil || strings.TrimSpace(input.CredentialID) == "" {
			return jsonManagement(http.StatusBadRequest, map[string]string{"error": "credential_id is required"}), nil
		}
		output, err := r.ManualPing(ctx, input)
		if err != nil {
			status := http.StatusBadGateway
			message := err.Error()
			switch {
			case strings.Contains(message, "not found"):
				status = http.StatusNotFound
			case strings.Contains(message, "not eligible"), strings.Contains(message, "not ready"):
				status = http.StatusConflict
			case strings.Contains(message, "in-flight"):
				status = http.StatusConflict
			}
			output.Error = safeErrorMessage(message)
			return jsonManagement(status, output), nil
		}
		return jsonManagement(http.StatusOK, output), nil
	default:
		return jsonManagement(http.StatusNotFound, map[string]string{"error": "not found"}), nil
	}
}

func (r *Runtime) status() statusPayload {
	cfg, store, _ := r.snapshot()
	accounts := []CredentialState{}
	if store != nil {
		accounts = store.Accounts()
	}
	return statusPayload{
		Plugin:  pluginID,
		Version: r.version,
		AutoPing: statusConfig{
			Enabled:                 cfg.AutoPingEnabled,
			ScanInterval:            cfg.ScanInterval.String(),
			ActivationDelay:         cfg.ActivationDelay.String(),
			RetryCooldown:           cfg.RetryCooldown.String(),
			MaxConcurrency:          cfg.MaxConcurrency,
			RequestTimeout:          cfg.RequestTimeout.String(),
			Model:                   cfg.Model,
			ModelCandidates:         cfg.Models(),
			Transport:               cfg.Transport,
			SchedulerBoostFallback:  cfg.SchedulerBoostFallback,
			ExcludedCredentialCount: len(cfg.ExcludeCredentials),
		},
		Accounts: accounts,
	}
}

func jsonManagement(status int, value any) managementResponse {
	body, _ := json.MarshalIndent(value, "", "  ")
	return managementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": {"application/json; charset=utf-8"}, "Cache-Control": {"no-store"}},
		Body:       body,
	}
}

func successEnvelope(value any) []byte {
	result, err := json.Marshal(value)
	if err != nil {
		return failureEnvelope("encode_failed", err.Error())
	}
	encoded, _ := json.Marshal(rpcEnvelope{OK: true, Result: result})
	return encoded
}

func failureEnvelope(code, message string) []byte {
	encoded, _ := json.Marshal(rpcEnvelope{OK: false, Error: &rpcError{Code: code, Message: safeErrorMessage(message)}})
	return encoded
}
