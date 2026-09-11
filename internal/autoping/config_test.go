package autoping

import (
	"errors"
	"testing"
	"time"
)

func TestConfigDefaultsRequireExplicitOptIn(t *testing.T) {
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoPingEnabled {
		t.Fatal("auto-ping must be disabled by default")
	}
	if cfg.ScanInterval != time.Minute || cfg.Model != "auto" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if got := cfg.Models(); len(got) != 2 || got[0] != "gpt-5.5" || got[1] != "gpt-5.6-luna" {
		t.Fatalf("model candidates = %#v", got)
	}
}

func TestConfigParsesFlatStandaloneShape(t *testing.T) {
	cfg, err := ParseConfig([]byte(`
auto_ping_enabled: true
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
state_path: data/auto-ping.json
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
}

func TestConfigRejectsAutoWithoutCandidates(t *testing.T) {
	_, err := ParseConfig([]byte("model: auto\nmodel_candidates: []\n"))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}
