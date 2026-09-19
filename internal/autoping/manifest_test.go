package autoping

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

const testManifestYAML = `schema_version: 1
id: cliproxyapi-auto-ping
metadata:
  name: "Codex 5h Auto-Ping"
  version: "0.1.0"
  author: "HungNth"
  github_repository: "https://github.com/HungNth/cliproxyapi-auto-ping"
  description: "Sends minimal targeted Codex requests at configured daily milestones."
  config_fields:
    - name: auto_ping_disabled
      type: boolean
      description: "Set to true to disable background Codex inference requests."
    - name: schedule
      type: array
      description: "List of daily auto-ping milestone times in 24-hour HH:MM format, e.g. 05:00, 10:00, 15:00, 20:00."
    - name: timezone
      type: string
      description: "Timezone for schedule milestones, e.g. Local, UTC, or Asia/Ho_Chi_Minh."
    - name: retry_cooldown
      type: string
      description: "Cooldown delay before retrying after an activation failure."
    - name: max_concurrency
      type: integer
      description: "Maximum number of credentials processed concurrently."
    - name: request_timeout
      type: string
      description: "Timeout for Codex inference requests."
    - name: prompt
      type: string
      description: "Minimal prompt text sent to Codex."
    - name: model
      type: string
      description: "Explicit model name or 'auto'."
    - name: model_candidates
      type: array
      description: "Ordered list of model candidates to try when model is auto."
    - name: transport
      type: enum
      enum_values: ["direct_http", "scheduler_boost"]
      description: "Primary activation transport."
    - name: scheduler_boost_fallback
      type: boolean
      description: "Fall back to scheduler_boost only on transport or host failures."
    - name: exclude_credentials
      type: array
      description: "Credential IDs excluded from automatic pings."
    - name: state_path
      type: string
      description: "Path to the persistent state JSON file."
defaults:
  auto_ping_disabled: false
  schedule: ["05:00", "10:00", "15:00", "20:00"]
  timezone: "Local"
  retry_cooldown: "2m"
  max_concurrency: 1
  request_timeout: "60s"
  prompt: "ping"
  model: "auto"
  model_candidates: ["gpt-5.5", "gpt-5.6-luna"]
  transport: "direct_http"
  scheduler_boost_fallback: true
  exclude_credentials: []
  state_path: "cliproxyapi-auto-ping/state.json"
`

func manifestVariant(t *testing.T, edits ...string) []byte {
	t.Helper()
	text := testManifestYAML
	for _, edit := range edits {
		switch {
		case strings.HasPrefix(edit, "+"):
			text += edit[1:]
		default:
			from, to, found := strings.Cut(edit, "=>")
			if !found || !strings.Contains(text, from) {
				t.Fatalf("bad manifest edit %q", edit)
			}
			text = strings.Replace(text, from, to, 1)
		}
	}
	return []byte(text)
}

func TestParseManifestAcceptsCanonicalDocument(t *testing.T) {
	manifest, err := ParseManifest([]byte(testManifestYAML))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || manifest.ID != "cliproxyapi-auto-ping" {
		t.Fatalf("manifest identity = %#v", manifest)
	}
	if manifest.Metadata.Name != "Codex 5h Auto-Ping" || manifest.Metadata.Version != "0.1.0" || manifest.Metadata.Author != "HungNth" {
		t.Fatalf("metadata = %#v", manifest.Metadata)
	}
	if len(manifest.Metadata.ConfigFields) != 13 {
		t.Fatalf("config fields = %d, want 13", len(manifest.Metadata.ConfigFields))
	}
	defaults := manifest.Defaults
	if !defaults.AutoPingEnabled || !slices.Equal(defaults.Schedule, []string{"05:00", "10:00", "15:00", "20:00"}) || defaults.Timezone != "Local" || defaults.Model != "auto" {
		t.Fatalf("defaults = %#v", defaults)
	}
	if got := defaults.Models(); !slices.Equal(got, []string{"gpt-5.5", "gpt-5.6-luna"}) {
		t.Fatalf("model candidates = %#v", got)
	}
	if defaults.StatePath != "cliproxyapi-auto-ping/state.json" {
		t.Fatalf("state path = %q", defaults.StatePath)
	}
}

func TestParseManifestRejectsInvalidDocuments(t *testing.T) {
	cases := []struct {
		name     string
		edits    []string
		sentinel error
	}{
		{name: "unknown top-level key", edits: []string{"+\nbogus_top_key: 1\n"}, sentinel: ErrInvalidManifest},
		{name: "unknown defaults key", edits: []string{"  prompt: \"ping\"=>  prompt: \"ping\"\n  bogus_default: 1"}, sentinel: ErrInvalidManifest},
		{name: "missing schema version", edits: []string{"schema_version: 1\n=>"}, sentinel: ErrInvalidManifest},
		{name: "unsupported schema version", edits: []string{"schema_version: 1=>schema_version: 2"}, sentinel: ErrInvalidManifest},
		{name: "empty id", edits: []string{"id: cliproxyapi-auto-ping=>id: \"\""}, sentinel: ErrInvalidManifest},
		{name: "missing metadata name", edits: []string{"  name: \"Codex 5h Auto-Ping\"\n=>"}, sentinel: ErrInvalidManifest},
		{name: "missing config field", edits: []string{"    - name: prompt\n      type: string\n      description: \"Minimal prompt text sent to Codex.\"\n=>"}, sentinel: ErrInvalidManifest},
		{name: "missing auto_ping_disabled config field", edits: []string{"    - name: auto_ping_disabled\n      type: boolean\n      description: \"Set to true to disable background Codex inference requests.\"\n=>"}, sentinel: ErrInvalidManifest},
		{name: "duplicate config field", edits: []string{"    - name: state_path=>    - name: prompt\n      type: string\n      description: \"Duplicate prompt field.\"\n    - name: state_path"}, sentinel: ErrInvalidManifest},
		{name: "unsupported config field", edits: []string{"    - name: prompt=>    - name: bogus_field"}, sentinel: ErrInvalidManifest},
		{name: "wrong config field type", edits: []string{"    - name: auto_ping_disabled\n      type: boolean=>    - name: auto_ping_disabled\n      type: string"}, sentinel: ErrInvalidManifest},
		{name: "incomplete transport enum", edits: []string{"      enum_values: [\"direct_http\", \"scheduler_boost\"]=>      enum_values: [\"direct_http\"]"}, sentinel: ErrInvalidManifest},
		{name: "enum on non-enum field", edits: []string{"    - name: model_candidates\n      type: array=>    - name: model_candidates\n      type: array\n      enum_values: [\"x\"]"}, sentinel: ErrInvalidManifest},
		{name: "empty field description", edits: []string{"      description: \"Minimal prompt text sent to Codex.\"=>      description: \"\""}, sentinel: ErrInvalidManifest},
		{name: "missing default key", edits: []string{"  max_concurrency: 1\n=>"}, sentinel: ErrInvalidConfig},
		{name: "missing auto_ping_disabled default", edits: []string{"  auto_ping_disabled: false\n=>"}, sentinel: ErrInvalidConfig},
		{name: "invalid default schedule", edits: []string{"  schedule: [\"05:00\", \"10:00\", \"15:00\", \"20:00\"]=>  schedule: [\"25:00\"]"}, sentinel: ErrInvalidConfig},
		{name: "invalid default timezone", edits: []string{"  timezone: \"Local\"=>  timezone: \"Invalid/Bogus_Zone\""}, sentinel: ErrInvalidConfig},
		{name: "invalid default concurrency", edits: []string{"  max_concurrency: 1=>  max_concurrency: 0"}, sentinel: ErrInvalidConfig},
		{name: "auto default without candidates", edits: []string{"  model_candidates: [\"gpt-5.5\", \"gpt-5.6-luna\"]=>  model_candidates: []"}, sentinel: ErrInvalidConfig},
		{name: "invalid default transport", edits: []string{"  transport: \"direct_http\"=>  transport: \"bogus\""}, sentinel: ErrInvalidConfig},
		{name: "empty default state path", edits: []string{"  state_path: \"cliproxyapi-auto-ping/state.json\"=>  state_path: \"\""}, sentinel: ErrInvalidConfig},
		{name: "empty default prompt", edits: []string{"  prompt: \"ping\"=>  prompt: \"\""}, sentinel: ErrInvalidConfig},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseManifest(manifestVariant(t, testCase.edits...))
			if err == nil {
				t.Fatal("expected manifest to be rejected")
			}
			if !errors.Is(err, testCase.sentinel) {
				t.Fatalf("error = %v, want sentinel %v", err, testCase.sentinel)
			}
		})
	}
}

func TestRuntimeConstructionConsumesManifestBytes(t *testing.T) {
	if _, err := NewRuntime(newFakeHost(), []byte(testManifestYAML), Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntime(newFakeHost(), manifestVariant(t, "id: cliproxyapi-auto-ping=>id: \"\""), Options{}); err == nil {
		t.Fatal("expected invalid manifest to fail runtime construction")
	}
}

func TestRegistrationServesManifestMetadata(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	raw := runtime.Handle(t.Context(), "plugin.register", nil)
	var envelope struct {
		OK     bool               `json:"ok"`
		Result registrationResult `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatalf("registration failed: %s", raw)
	}
	if envelope.Result.ID != "cliproxyapi-auto-ping" {
		t.Fatalf("registration id = %q, want cliproxyapi-auto-ping", envelope.Result.ID)
	}
	metadata := envelope.Result.Metadata
	if metadata.Name != "Codex 5h Auto-Ping" || metadata.Version != "0.1.0" || metadata.Author != "HungNth" || metadata.GitHubRepository != "https://github.com/HungNth/cliproxyapi-auto-ping" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if len(metadata.ConfigFields) != 13 {
		t.Fatalf("config fields = %d, want 13", len(metadata.ConfigFields))
	}
	byName := map[string]configField{}
	for _, field := range metadata.ConfigFields {
		byName[field.Name] = field
	}
	if _, exists := byName["max_output_tokens"]; exists {
		t.Fatal("registration metadata must not contain max_output_tokens")
	}
	disabled := byName["auto_ping_disabled"]
	if disabled.DefaultValue != false || disabled.Description != "Set to true to disable background Codex inference requests (Default: false)." {
		t.Fatalf("auto_ping_disabled = %#v", disabled)
	}
	candidates := byName["model_candidates"]
	values, ok := candidates.DefaultValue.([]any)
	if !ok || len(values) != 2 || values[0] != "gpt-5.5" || values[1] != "gpt-5.6-luna" {
		t.Fatalf("model_candidates default = %#v", candidates.DefaultValue)
	}
	if candidates.Description != "Ordered list of model candidates to try when model is auto (Default: [gpt-5.5, gpt-5.6-luna])." {
		t.Fatalf("model_candidates description = %q", candidates.Description)
	}
	transport := byName["transport"]
	if !slices.Equal(transport.EnumValues, []string{TransportDirectHTTP, TransportSchedulerBoost}) {
		t.Fatalf("transport enum = %#v", transport.EnumValues)
	}
}

func TestInstanceConfigurationOverridesManifestDefaults(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	cfg := testConfig(t, runtime, "")
	if !slices.Equal(cfg.Schedule, []string{"05:00", "10:00", "15:00", "20:00"}) || cfg.Timezone != "Local" || !cfg.AutoPingEnabled {
		t.Fatalf("omitted settings must inherit manifest defaults: %#v", cfg)
	}
	cfg = testConfig(t, runtime, "auto_ping_disabled: true\n")
	if cfg.AutoPingEnabled {
		t.Fatalf("explicit opt-out must override manifest defaults: %#v", cfg)
	}
	cfg = testConfig(t, runtime, "auto_ping_disabled: false\n")
	if !cfg.AutoPingEnabled {
		t.Fatalf("explicit false must preserve auto-ping enabled: %#v", cfg)
	}
	cfg = testConfig(t, runtime, "schedule:\n  - \"08:00\"\n  - \"16:00\"\ntimezone: UTC\nmodel: gpt-9\n")
	if !slices.Equal(cfg.Schedule, []string{"08:00", "16:00"}) || cfg.Timezone != "UTC" || cfg.Model != "gpt-9" || !slices.Equal(cfg.Models(), []string{"gpt-9"}) {
		t.Fatalf("explicit settings must override manifest defaults: %#v", cfg)
	}
}
