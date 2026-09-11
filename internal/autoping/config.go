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

func configFromRaw(raw rawConfig, base Config, requireComplete bool) (Config, error) {
	cfg := base
	if raw.AutoPingEnabled != nil {
		cfg.AutoPingEnabled = *raw.AutoPingEnabled
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set auto_ping_enabled", ErrInvalidConfig)
	}
	if raw.ScanInterval != "" {
		parsed, err := positiveDuration("scan_interval", raw.ScanInterval)
		if err != nil {
			return Config{}, err
		}
		cfg.ScanInterval = parsed
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set scan_interval", ErrInvalidConfig)
	}
	if raw.ActivationDelay != "" {
		parsed, err := nonNegativeDuration("activation_delay", raw.ActivationDelay)
		if err != nil {
			return Config{}, err
		}
		cfg.ActivationDelay = parsed
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set activation_delay", ErrInvalidConfig)
	}
	if raw.RetryCooldown != "" {
		parsed, err := positiveDuration("retry_cooldown", raw.RetryCooldown)
		if err != nil {
			return Config{}, err
		}
		cfg.RetryCooldown = parsed
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set retry_cooldown", ErrInvalidConfig)
	}
	if raw.RequestTimeout != "" {
		parsed, err := positiveDuration("request_timeout", raw.RequestTimeout)
		if err != nil {
			return Config{}, err
		}
		cfg.RequestTimeout = parsed
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set request_timeout", ErrInvalidConfig)
	}
	if raw.MaxConcurrency != nil {
		cfg.MaxConcurrency = *raw.MaxConcurrency
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set max_concurrency", ErrInvalidConfig)
	}
	if cfg.MaxConcurrency < 1 || cfg.MaxConcurrency > 64 {
		return Config{}, fmt.Errorf("%w: max_concurrency must be between 1 and 64", ErrInvalidConfig)
	}
	if raw.Prompt != nil {
		cfg.Prompt = strings.TrimSpace(*raw.Prompt)
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set prompt", ErrInvalidConfig)
	}
	if cfg.Prompt == "" {
		return Config{}, fmt.Errorf("%w: prompt must not be empty", ErrInvalidConfig)
	}
	if raw.MaxOutputTokens != nil {
		cfg.MaxOutputTokens = *raw.MaxOutputTokens
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set max_output_tokens", ErrInvalidConfig)
	}
	if cfg.MaxOutputTokens < 1 || cfg.MaxOutputTokens > 16 {
		return Config{}, fmt.Errorf("%w: max_output_tokens must be between 1 and 16", ErrInvalidConfig)
	}
	if raw.Model != nil {
		cfg.Model = strings.TrimSpace(*raw.Model)
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set model", ErrInvalidConfig)
	}
	if cfg.Model == "" {
		return Config{}, fmt.Errorf("%w: model must not be empty", ErrInvalidConfig)
	}
	if raw.ModelCandidates != nil {
		cfg.ModelCandidates = uniqueStrings(raw.ModelCandidates)
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set model_candidates", ErrInvalidConfig)
	}
	if cfg.Model == "auto" && len(cfg.ModelCandidates) == 0 {
		return Config{}, fmt.Errorf("%w: model_candidates must not be empty when model is auto", ErrInvalidConfig)
	}
	if raw.Transport != nil {
		cfg.Transport = strings.TrimSpace(*raw.Transport)
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set transport", ErrInvalidConfig)
	}
	if cfg.Transport != TransportDirectHTTP && cfg.Transport != TransportSchedulerBoost {
		return Config{}, fmt.Errorf("%w: transport must be %q or %q", ErrInvalidConfig, TransportDirectHTTP, TransportSchedulerBoost)
	}
	if raw.SchedulerBoostFallback != nil {
		cfg.SchedulerBoostFallback = *raw.SchedulerBoostFallback
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set scheduler_boost_fallback", ErrInvalidConfig)
	}
	if raw.ExcludeCredentials != nil {
		cfg.ExcludeCredentials = uniqueStrings(raw.ExcludeCredentials)
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set exclude_credentials", ErrInvalidConfig)
	}
	if raw.StatePath != nil {
		cfg.StatePath = strings.TrimSpace(*raw.StatePath)
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set state_path", ErrInvalidConfig)
	}
	if cfg.StatePath == "" {
		return Config{}, fmt.Errorf("%w: state_path must not be empty", ErrInvalidConfig)
	}
	return cfg, nil
}

func (r *Runtime) parseConfig(data []byte) (Config, error) {
	var raw rawConfig
	if len(strings.TrimSpace(string(data))) != 0 {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return Config{}, fmt.Errorf("%w: decode YAML: %v", ErrInvalidConfig, err)
		}
	}
	return configFromRaw(raw, r.manifest.Defaults, false)
}

func (c Config) Models() []string {
	if c.Model != "auto" {
		return []string{c.Model}
	}
	return slices.Clone(c.ModelCandidates)
}

func (c Config) fieldValue(name string) any {
	switch name {
	case "auto_ping_enabled":
		return c.AutoPingEnabled
	case "scan_interval":
		return c.ScanInterval.String()
	case "activation_delay":
		return c.ActivationDelay.String()
	case "retry_cooldown":
		return c.RetryCooldown.String()
	case "max_concurrency":
		return c.MaxConcurrency
	case "request_timeout":
		return c.RequestTimeout.String()
	case "prompt":
		return c.Prompt
	case "max_output_tokens":
		return c.MaxOutputTokens
	case "model":
		return c.Model
	case "model_candidates":
		return slices.Clone(c.ModelCandidates)
	case "transport":
		return c.Transport
	case "scheduler_boost_fallback":
		return c.SchedulerBoostFallback
	case "exclude_credentials":
		return slices.Clone(c.ExcludeCredentials)
	case "state_path":
		return c.StatePath
	}
	return nil
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
