package autoping

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const fiveHourSeconds = int64(5 * 60 * 60)

var (
	ErrInvalidCredential = errors.New("invalid Codex credential")
	ErrInvalidUsage      = errors.New("invalid Codex usage payload")
	ErrNoFiveHourWindow  = errors.New("five-hour window not found")
)

type AuthMaterial struct {
	AccessToken string
	AccountID   string
}

func ParseAuthMaterial(data []byte) (AuthMaterial, error) {
	var root map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return AuthMaterial{}, fmt.Errorf("%w: malformed JSON", ErrInvalidCredential)
	}

	objects := []map[string]any{root}
	for _, key := range []string{"tokens", "credentials", "auth", "oauth", "session"} {
		if nested, ok := root[key].(map[string]any); ok {
			objects = append(objects, nested)
		}
	}
	material := AuthMaterial{}
	for _, object := range objects {
		if material.AccessToken == "" {
			material.AccessToken = firstString(object,
				"access_token", "accessToken", "oauth_access_token", "oauthAccessToken",
				"session_token", "sessionToken", "token", "id_token", "idToken",
			)
		}
		if material.AccountID == "" {
			material.AccountID = firstString(object, "account_id", "accountId", "chatgpt_account_id", "chatgptAccountId", "workspace_id", "workspaceId")
		}
	}
	if material.AccessToken == "" {
		return AuthMaterial{}, fmt.Errorf("%w: access token missing", ErrInvalidCredential)
	}
	return material, nil
}

type Observation struct {
	ResetAt            time.Time `json:"reset_at"`
	ObservedAt         time.Time `json:"observed_at"`
	LimitWindowSeconds int64     `json:"limit_window_seconds"`
}

func ParseFiveHourObservation(data []byte, observedAt time.Time) (Observation, error) {
	var root map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return Observation{}, fmt.Errorf("%w: malformed JSON", ErrInvalidUsage)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Observation{}, fmt.Errorf("%w: trailing JSON data", ErrInvalidUsage)
	}

	for _, candidate := range rateLimitCandidates(root) {
		observation, found, err := fiveHourObservation(candidate, observedAt.UTC())
		if err != nil {
			return Observation{}, err
		}
		if found {
			return observation, nil
		}
	}
	return Observation{}, ErrNoFiveHourWindow
}

func rateLimitCandidates(root map[string]any) []any {
	candidates := make([]any, 0, 4)
	if byID, ok := root["rate_limits_by_limit_id"].(map[string]any); ok {
		if codex, ok := byID["codex"]; ok {
			candidates = append(candidates, codex)
		}
	}
	if rateLimit, ok := root["rate_limit"]; ok {
		candidates = append(candidates, rateLimit)
	}
	if rateLimits, ok := root["rate_limits"]; ok {
		candidates = append(candidates, rateLimits)
	}
	candidates = append(candidates, root)
	return candidates
}

func fiveHourObservation(candidate any, observedAt time.Time) (Observation, bool, error) {
	if window, ok := candidate.(map[string]any); ok {
		if observation, found, err := observationFromWindow(window, observedAt); found || err != nil {
			return observation, found, err
		}
	}

	var found Observation
	count := 0
	visit := func(value any) error {
		window, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		observation, matches, err := observationFromWindow(window, observedAt)
		if err != nil {
			return err
		}
		if matches {
			found = observation
			count++
		}
		return nil
	}

	switch value := candidate.(type) {
	case map[string]any:
		for _, child := range value {
			if err := visit(child); err != nil {
				return Observation{}, false, err
			}
		}
	case []any:
		for _, child := range value {
			if err := visit(child); err != nil {
				return Observation{}, false, err
			}
		}
	}
	if count > 1 {
		return Observation{}, false, fmt.Errorf("%w: multiple five-hour windows in one usage candidate", ErrInvalidUsage)
	}
	return found, count == 1, nil
}

func observationFromWindow(window map[string]any, observedAt time.Time) (Observation, bool, error) {
	seconds, ok := numberAsInt64(window["limit_window_seconds"])
	if !ok || seconds != fiveHourSeconds {
		return Observation{}, false, nil
	}
	resetAt, err := parseResetTime(window)
	if err != nil {
		return Observation{}, false, err
	}
	if resetAt.IsZero() {
		return Observation{}, false, fmt.Errorf("%w: reset_at missing", ErrInvalidUsage)
	}
	return Observation{
		ResetAt:            resetAt.UTC(),
		ObservedAt:         observedAt,
		LimitWindowSeconds: seconds,
	}, true, nil
}

func parseResetTime(window map[string]any) (time.Time, error) {
	raw, ok := window["reset_at"]
	if !ok || raw == nil {
		return time.Time{}, nil
	}
	switch value := raw.(type) {
	case json.Number:
		seconds, err := value.Int64()
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: reset_at must be an integer", ErrInvalidUsage)
		}
		if seconds >= 1_000_000_000_000 {
			return time.UnixMilli(seconds).UTC(), nil
		}
		return time.Unix(seconds, 0).UTC(), nil
	case string:
		text := strings.TrimSpace(value)
		if integer, err := strconv.ParseInt(text, 10, 64); err == nil {
			if integer >= 1_000_000_000_000 {
				return time.UnixMilli(integer).UTC(), nil
			}
			return time.Unix(integer, 0).UTC(), nil
		}
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return parsed.UTC(), nil
		}
		return time.Time{}, fmt.Errorf("%w: invalid reset_at %q", ErrInvalidUsage, text)
	default:
		return time.Time{}, fmt.Errorf("%w: reset_at must be an integer or RFC3339 string", ErrInvalidUsage)
	}
}

func numberAsInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}
