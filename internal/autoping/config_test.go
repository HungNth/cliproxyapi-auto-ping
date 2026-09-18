package autoping

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestConfigDefaultsEnableAutoPing(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	cfg := testConfig(t, runtime, "")
	if !cfg.AutoPingEnabled {
		t.Fatal("auto-ping must be enabled by default")
	}
	if !slices.Equal(cfg.Schedule, []string{"05:00", "10:00", "15:00", "20:00"}) || cfg.Timezone != "Local" || cfg.Model != "auto" {
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
schedule:
  - "16:00"
  - "08:00"
timezone: UTC
retry_cooldown: 5m
max_concurrency: 2
request_timeout: 45s
prompt: "1"
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
	if !cfg.AutoPingEnabled || !slices.Equal(cfg.Schedule, []string{"08:00", "16:00"}) || cfg.Timezone != "UTC" || cfg.MaxConcurrency != 2 {
		t.Fatalf("unexpected parsed config: %#v", cfg)
	}
	if cfg.Location == nil || cfg.Location != time.UTC {
		t.Fatalf("expected time.UTC location, got %v", cfg.Location)
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

func TestConfigRejectsInvalidSchedule(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	invalidSchedules := []string{
		"schedule: []\n",
		"schedule: [\"5:00\"]\n",
		"schedule: [\"24:00\"]\n",
		"schedule: [\"12:60\"]\n",
		"schedule: [\"invalid\"]\n",
	}
	for _, item := range invalidSchedules {
		_, err := runtime.parseConfig([]byte(item))
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("schedule %s expected ErrInvalidConfig, got %v", item, err)
		}
	}
}

func TestConfigRejectsInvalidTimezone(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	_, err := runtime.parseConfig([]byte("timezone: \"Invalid/Bogus_Zone\"\n"))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestConfigRejectsAutoWithoutCandidates(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	_, err := runtime.parseConfig([]byte("model: auto\nmodel_candidates: []\n"))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestConfigParsesCaseInsensitiveTimezonesAndEmbeddedTzdata(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	cases := []struct {
		input    string
		wantName string
	}{
		{"timezone: \"Asia/Ho_Chi_Minh\"\n", "Asia/Ho_Chi_Minh"},
		{"timezone: \"UTC\"\n", "UTC"},
		{"timezone: \"utc\"\n", "UTC"},
		{"timezone: \"Local\"\n", "Local"},
		{"timezone: \"local\"\n", "Local"},
	}
	for _, tc := range cases {
		cfg, err := runtime.parseConfig([]byte(tc.input))
		if err != nil {
			t.Fatalf("parseConfig(%q) failed: %v", tc.input, err)
		}
		if cfg.Timezone != tc.wantName || cfg.Location == nil {
			t.Fatalf("parseConfig(%q) = (%q, %v), want name %q", tc.input, cfg.Timezone, cfg.Location, tc.wantName)
		}
	}
}

