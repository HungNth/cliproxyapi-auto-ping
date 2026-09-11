package autoping

import (
	"errors"
	"testing"
	"time"
)

func TestConfigDefaultsEnableAutoPing(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	cfg := testConfig(t, runtime, "")
	if !cfg.AutoPingEnabled {
		t.Fatal("auto-ping must be enabled by default")
	}
	if cfg.ScanInterval != time.Minute || cfg.Model != "auto" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if got := cfg.Models(); len(got) != 2 || got[0] != "gpt-5.5" || got[1] != "gpt-5.6-luna" {
		t.Fatalf("model candidates = %#v", got)
	}
}

func TestConfigParsesFlatStandaloneShape(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	cfg, err := runtime.parseConfig([]byte(`
auto_ping_disabled: false
scan_interval: 30s
activation_delay: 7s
retry_cooldown: 5m
max_concurrency: 2
request_timeout: 45s
prompt: "1"
max_output_tokens: 2
model: auto
model_candidates:
  - gpt-5.5
  - gpt-5.6-luna
transport: direct_http
scheduler_boost_fallback: false
exclude_credentials:
  - codex-test
`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AutoPingEnabled || cfg.ScanInterval != 30*time.Second || cfg.MaxConcurrency != 2 {
		t.Fatalf("unexpected parsed config: %#v", cfg)
	}
	if !cfg.Excludes("codex-test") || cfg.SchedulerBoostFallback {
		t.Fatalf("unexpected filters/fallback: %#v", cfg)
	}

	disabledCfg, err := runtime.parseConfig([]byte("auto_ping_disabled: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if disabledCfg.AutoPingEnabled {
		t.Fatal("auto_ping_disabled: true must set AutoPingEnabled to false")
	}
}

func TestConfigRejectsAutoWithoutCandidates(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	_, err := runtime.parseConfig([]byte("model: auto\nmodel_candidates: []\n"))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}
