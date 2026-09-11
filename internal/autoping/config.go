package autoping

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	TransportDirectHTTP     = "direct_http"
	TransportSchedulerBoost = "scheduler_boost"
)

var ErrInvalidConfig = errors.New("invalid configuration")

type Config struct {
	AutoPingEnabled        bool
	ScanInterval           time.Duration
	ActivationDelay        time.Duration
	RetryCooldown          time.Duration
	MaxConcurrency         int
	RequestTimeout         time.Duration
	Prompt                 string
	MaxOutputTokens        int
	Model                  string
	ModelCandidates        []string
	Transport              string
	SchedulerBoostFallback bool
	ExcludeCredentials     []string
	StatePath              string
}

type rawConfig struct {
	AutoPingEnabled        *bool    `yaml:"auto_ping_enabled"`
	ScanInterval           string   `yaml:"scan_interval"`
	ActivationDelay        string   `yaml:"activation_delay"`
	RetryCooldown          string   `yaml:"retry_cooldown"`
	MaxConcurrency         *int     `yaml:"max_concurrency"`
	RequestTimeout         string   `yaml:"request_timeout"`
	Prompt                 *string  `yaml:"prompt"`
	MaxOutputTokens        *int     `yaml:"max_output_tokens"`
	Model                  *string  `yaml:"model"`
	ModelCandidates        []string `yaml:"model_candidates"`
	Transport              *string  `yaml:"transport"`
	SchedulerBoostFallback *bool    `yaml:"scheduler_boost_fallback"`
	ExcludeCredentials     []string `yaml:"exclude_credentials"`
	StatePath              *string  `yaml:"state_path"`
}

func DefaultConfig() Config {
	return Config{
		AutoPingEnabled:        false,
		ScanInterval:           time.Minute,
		ActivationDelay:        5 * time.Second,
		RetryCooldown:          15 * time.Minute,
		MaxConcurrency:         1,
		RequestTimeout:         time.Minute,
		Prompt:                 "ping",
		MaxOutputTokens:        1,
		Model:                  "auto",
		ModelCandidates:        []string{"gpt-5.5", "gpt-5.6-luna"},
		Transport:              TransportDirectHTTP,
		SchedulerBoostFallback: true,
		StatePath:              "auto-ping/state.json",
	}
}

func ParseConfig(data []byte) (Config, error) {
	cfg := DefaultConfig()
	if len(strings.TrimSpace(string(data))) == 0 {
		return cfg, nil
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, fmt.Errorf("%w: decode YAML: %v", ErrInvalidConfig, err)
	}
	if raw.AutoPingEnabled != nil {
		cfg.AutoPingEnabled = *raw.AutoPingEnabled
	}
	if raw.ScanInterval != "" {
		parsed, err := positiveDuration("scan_interval", raw.ScanInterval)
		if err != nil {
			return Config{}, err
		}
		cfg.ScanInterval = parsed
	}
	if raw.ActivationDelay != "" {
		parsed, err := nonNegativeDuration("activation_delay", raw.ActivationDelay)
		if err != nil {
			return Config{}, err
		}
		cfg.ActivationDelay = parsed
	}
	if raw.RetryCooldown != "" {
		parsed, err := positiveDuration("retry_cooldown", raw.RetryCooldown)
		if err != nil {
			return Config{}, err
		}
		cfg.RetryCooldown = parsed
	}
	if raw.RequestTimeout != "" {
		parsed, err := positiveDuration("request_timeout", raw.RequestTimeout)
		if err != nil {
			return Config{}, err
		}
		cfg.RequestTimeout = parsed
	}
	if raw.MaxConcurrency != nil {
		cfg.MaxConcurrency = *raw.MaxConcurrency
	}
	if cfg.MaxConcurrency < 1 || cfg.MaxConcurrency > 64 {
		return Config{}, fmt.Errorf("%w: max_concurrency must be between 1 and 64", ErrInvalidConfig)
	}
	if raw.Prompt != nil {
		cfg.Prompt = strings.TrimSpace(*raw.Prompt)
	}
	if cfg.Prompt == "" {
		return Config{}, fmt.Errorf("%w: prompt must not be empty", ErrInvalidConfig)
	}
	if raw.MaxOutputTokens != nil {
		cfg.MaxOutputTokens = *raw.MaxOutputTokens
	}
	if cfg.MaxOutputTokens < 1 || cfg.MaxOutputTokens > 16 {
		return Config{}, fmt.Errorf("%w: max_output_tokens must be between 1 and 16", ErrInvalidConfig)
	}
	if raw.Model != nil {
		cfg.Model = strings.TrimSpace(*raw.Model)
	}
	if cfg.Model == "" {
		return Config{}, fmt.Errorf("%w: model must not be empty", ErrInvalidConfig)
	}
	if raw.ModelCandidates != nil {
		cfg.ModelCandidates = uniqueStrings(raw.ModelCandidates)
	}
	if cfg.Model == "auto" && len(cfg.ModelCandidates) == 0 {
		return Config{}, fmt.Errorf("%w: model_candidates must not be empty when model is auto", ErrInvalidConfig)
	}
	if raw.Transport != nil {
		cfg.Transport = strings.TrimSpace(*raw.Transport)
	}
	if cfg.Transport != TransportDirectHTTP && cfg.Transport != TransportSchedulerBoost {
		return Config{}, fmt.Errorf("%w: transport must be %q or %q", ErrInvalidConfig, TransportDirectHTTP, TransportSchedulerBoost)
	}
	if raw.SchedulerBoostFallback != nil {
		cfg.SchedulerBoostFallback = *raw.SchedulerBoostFallback
	}
	if raw.ExcludeCredentials != nil {
		cfg.ExcludeCredentials = uniqueStrings(raw.ExcludeCredentials)
	}
	if raw.StatePath != nil {
		cfg.StatePath = strings.TrimSpace(*raw.StatePath)
	}
	if cfg.StatePath == "" {
		return Config{}, fmt.Errorf("%w: state_path must not be empty", ErrInvalidConfig)
	}
	return cfg, nil
}

func (c Config) Models() []string {
	if c.Model != "auto" {
		return []string{c.Model}
	}
	return slices.Clone(c.ModelCandidates)
}

func (c Config) Excludes(credentialID string) bool {
	return slices.Contains(c.ExcludeCredentials, strings.TrimSpace(credentialID))
}

func positiveDuration(field, value string) (time.Duration, error) {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%w: %s must be a positive duration", ErrInvalidConfig, field)
	}
	return parsed, nil
}

func nonNegativeDuration(field, value string) (time.Duration, error) {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%w: %s must be a non-negative duration", ErrInvalidConfig, field)
	}
	return parsed, nil
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || slices.Contains(result, value) {
			continue
		}
		result = append(result, value)
	}
	return result
}
