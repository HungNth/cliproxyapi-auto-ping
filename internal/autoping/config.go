package autoping

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"gopkg.in/yaml.v3"
)

const (
	TransportDirectHTTP     = "direct_http"
	TransportSchedulerBoost = "scheduler_boost"
)

var ErrInvalidConfig = errors.New("invalid configuration")

type Config struct {
	AutoPingEnabled        bool
	Schedule               []string
	Timezone               string
	Location               *time.Location
	RetryCooldown          time.Duration
	MaxConcurrency         int
	RequestTimeout         time.Duration
	Prompt                 string
	Model                  string
	ModelCandidates        []string
	Transport              string
	SchedulerBoostFallback bool
	ExcludeCredentials     []string
	StatePath              string
}

type rawConfig struct {
	AutoPingDisabled       *bool    `yaml:"auto_ping_disabled"`
	Schedule               []string `yaml:"schedule"`
	Timezone               *string  `yaml:"timezone"`
	RetryCooldown          string   `yaml:"retry_cooldown"`
	MaxConcurrency         *int     `yaml:"max_concurrency"`
	RequestTimeout         string   `yaml:"request_timeout"`
	Prompt                 *string  `yaml:"prompt"`
	Model                  *string  `yaml:"model"`
	ModelCandidates        []string `yaml:"model_candidates"`
	Transport              *string  `yaml:"transport"`
	SchedulerBoostFallback *bool    `yaml:"scheduler_boost_fallback"`
	ExcludeCredentials     []string `yaml:"exclude_credentials"`
	StatePath              *string  `yaml:"state_path"`
}

func configFromRaw(raw rawConfig, base Config, requireComplete bool) (Config, error) {
	cfg := base
	if raw.AutoPingDisabled != nil {
		cfg.AutoPingEnabled = !*raw.AutoPingDisabled
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set auto_ping_disabled", ErrInvalidConfig)
	}
	if raw.Schedule != nil {
		parsed, err := parseSchedule(raw.Schedule)
		if err != nil {
			return Config{}, err
		}
		cfg.Schedule = parsed
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set schedule", ErrInvalidConfig)
	}
	if raw.Timezone != nil {
		tz, loc, err := parseTimezone(*raw.Timezone)
		if err != nil {
			return Config{}, err
		}
		cfg.Timezone = tz
		cfg.Location = loc
	} else if requireComplete {
		return Config{}, fmt.Errorf("%w: manifest defaults must set timezone", ErrInvalidConfig)
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
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) != 0 {
		decoder := yaml.NewDecoder(bytes.NewReader(trimmed))
		decoder.KnownFields(true)
		if err := decoder.Decode(&raw); err != nil {
			return Config{}, fmt.Errorf("%w: decode YAML: %v", ErrInvalidConfig, err)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return Config{}, fmt.Errorf("%w: multiple or invalid trailing YAML documents", ErrInvalidConfig)
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
	case "auto_ping_disabled":
		return !c.AutoPingEnabled
	case "schedule":
		return slices.Clone(c.Schedule)
	case "timezone":
		return c.Timezone
	case "retry_cooldown":
		return c.RetryCooldown.String()
	case "max_concurrency":
		return c.MaxConcurrency
	case "request_timeout":
		return c.RequestTimeout.String()
	case "prompt":
		return c.Prompt
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

func parseSchedule(items []string) ([]string, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: schedule must not be empty", ErrInvalidConfig)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		s := strings.TrimSpace(item)
		if len(s) != 5 || s[2] != ':' {
			return nil, fmt.Errorf("%w: schedule element %q must be HH:MM in 24-hour format", ErrInvalidConfig, item)
		}
		hh, errH := strconv.Atoi(s[:2])
		mm, errM := strconv.Atoi(s[3:])
		if errH != nil || errM != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
			return nil, fmt.Errorf("%w: schedule element %q must be HH:MM in 24-hour format (00:00-23:59)", ErrInvalidConfig, item)
		}
		formatted := fmt.Sprintf("%02d:%02d", hh, mm)
		if !slices.Contains(result, formatted) {
			result = append(result, formatted)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%w: schedule must not be empty", ErrInvalidConfig)
	}
	slices.Sort(result)
	return result, nil
}

func parseTimezone(tz string) (string, *time.Location, error) {
	s := strings.TrimSpace(tz)
	if s == "" {
		return "", nil, fmt.Errorf("%w: timezone must not be empty", ErrInvalidConfig)
	}
	if strings.EqualFold(s, "Local") {
		return "Local", time.Local, nil
	}
	if strings.EqualFold(s, "UTC") {
		return "UTC", time.UTC, nil
	}
	loc, err := time.LoadLocation(s)
	if err != nil {
		return "", nil, fmt.Errorf("%w: invalid timezone %q: %v", ErrInvalidConfig, s, err)
	}
	return s, loc, nil
}
