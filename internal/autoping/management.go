package autoping

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

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
	ID            string          `json:"id,omitempty"`
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
		cfg, err := r.parseConfig(configData)
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
			{Method: http.MethodGet, Path: r.routePath("/status"), Description: "Show Codex five-hour auto-ping status."},
			{Method: http.MethodGet, Path: r.routePath("/diagnostics"), Description: "Explain per-credential auto-ping decisions."},
			{Method: http.MethodPost, Path: r.routePath("/ping"), Description: "Send a manual targeted Codex ping."},
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
		ID:            r.manifest.ID,
		Metadata: pluginMetadata{
			Name:             r.manifest.Metadata.Name,
			Version:          r.manifest.Metadata.Version,
			Author:           r.manifest.Metadata.Author,
			GitHubRepository: r.manifest.Metadata.GitHubRepository,
			Description:      r.manifest.Metadata.Description,
			ConfigFields:     r.registrationFields(),
		},
		Capabilities: map[string]bool{"management_api": true, "scheduler": true},
	}
}

func (r *Runtime) registrationFields() []configField {
	fields := make([]configField, 0, len(r.manifest.Metadata.ConfigFields))
	for _, field := range r.manifest.Metadata.ConfigFields {
		value := r.manifest.Defaults.fieldValue(field.Name)
		fields = append(fields, configField{
			Name:         field.Name,
			Type:         field.Type,
			EnumValues:   field.EnumValues,
			Description:  strings.TrimSuffix(field.Description, ".") + " (Default: " + formatDefaultHint(value) + ").",
			DefaultValue: value,
		})
	}
	return fields
}

func formatDefaultHint(value any) string {
	switch typed := value.(type) {
	case []string:
		return "[" + strings.Join(typed, ", ") + "]"
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case string:
		return typed
	}
	return fmt.Sprintf("%v", value)
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

func (r *Runtime) routePath(suffix string) string {
	return "/" + r.manifest.ID + suffix
}

func (r *Runtime) handleManagement(ctx context.Context, raw []byte) (managementResponse, error) {
	var request managementRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return jsonManagement(http.StatusBadRequest, map[string]string{"error": "invalid management request"}), nil
	}
	path := strings.TrimSuffix(request.Path, "/")
	switch {
	case request.Method == http.MethodGet && strings.HasSuffix(path, r.routePath("/status")):
		return jsonManagement(http.StatusOK, r.status()), nil
	case request.Method == http.MethodGet && strings.HasSuffix(path, r.routePath("/diagnostics")):
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
	case request.Method == http.MethodPost && strings.HasSuffix(path, r.routePath("/ping")):
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
		Plugin:  r.manifest.ID,
		Version: r.manifest.Metadata.Version,
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
