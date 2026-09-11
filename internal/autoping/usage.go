package autoping

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	UsedPercent        float64   `json:"used_percent,omitzero"`
	HasUsage           bool      `json:"has_usage,omitzero"`
}

type PendingTransition struct {
	Boundary   time.Time `json:"boundary"`
	ResetAt    time.Time `json:"reset_at"`
	ObservedAt time.Time `json:"observed_at"`
}

type DecisionKind string

const (
	DecisionWaiting  DecisionKind = "waiting"
	DecisionReady    DecisionKind = "ready"
	DecisionExternal DecisionKind = "external"
)

type WindowDecision struct {
	Kind              DecisionKind
	Boundary          time.Time
	Reason            string
	PendingTransition *PendingTransition
	ClearPending      bool
	ClearStabilizing  bool
}

func ParseFiveHourObservation(data []byte, observedAt time.Time) (Observation, error) {
	var root map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return Observation{}, fmt.Errorf("%w: malformed JSON", ErrInvalidUsage)
	}

	candidates := normalRateLimitRoots(root)
	var best Observation
	bestRank := -1
	for _, candidate := range candidates {
		collectFiveHourWindows(candidate.value, candidate.path, observedAt.UTC(), &best, &bestRank)
	}
	if bestRank < 0 {
		return Observation{}, ErrNoFiveHourWindow
	}
	return best, nil
}

type usageRoot struct {
	path  string
	value any
}

func normalRateLimitRoots(root map[string]any) []usageRoot {
	roots := make([]usageRoot, 0, 4)
	for _, key := range []string{"rate_limit", "rate_limits"} {
		if value, ok := root[key]; ok {
			roots = append(roots, usageRoot{path: key, value: value})
		}
	}
	if byID, ok := root["rate_limits_by_limit_id"].(map[string]any); ok {
		if value, ok := byID["codex"]; ok {
			roots = append(roots, usageRoot{path: "rate_limits_by_limit_id.codex", value: value})
		}
	}
	roots = append(roots, usageRoot{path: "root", value: root})
	return roots
}

func collectFiveHourWindows(value any, path string, observedAt time.Time, best *Observation, bestRank *int) {
	switch node := value.(type) {
	case map[string]any:
		if seconds, ok := numberAsInt64(node["limit_window_seconds"]); ok && seconds == fiveHourSeconds {
			if resetAt, ok := resetTime(node); ok {
				rank := windowRank(path)
				if rank > *bestRank {
					used, hasUsage := usagePercent(node)
					*best = Observation{
						ResetAt: resetAt.UTC(), ObservedAt: observedAt,
						LimitWindowSeconds: seconds, UsedPercent: used, HasUsage: hasUsage,
					}
					*bestRank = rank
				}
			}
		}
		for key, child := range node {
			lowered := strings.ToLower(key)
			if strings.Contains(lowered, "review") || strings.Contains(lowered, "spark") {
				continue
			}
			collectFiveHourWindows(child, path+"."+key, observedAt, best, bestRank)
		}
	case []any:
		for index, child := range node {
			collectFiveHourWindows(child, fmt.Sprintf("%s[%d]", path, index), observedAt, best, bestRank)
		}
	}
}

func windowRank(path string) int {
	lowered := strings.ToLower(path)
	switch {
	case strings.Contains(lowered, "primary_window"), strings.Contains(lowered, "session"):
		return 4
	case strings.Contains(lowered, "primary"):
		return 3
	case strings.Contains(lowered, "secondary"):
		return 1
	default:
		return 2
	}
}

func resetTime(window map[string]any) (time.Time, bool) {
	for _, key := range []string{"reset_at", "resets_at", "resetAt"} {
		if parsed, ok := parseTimeValue(window[key]); ok {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func parseTimeValue(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return time.Time{}, false
		}
		if number, err := strconv.ParseInt(text, 10, 64); err == nil {
			return unixTime(number), true
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		return parsed, err == nil
	case json.Number:
		number, err := typed.Int64()
		return unixTime(number), err == nil
	case float64:
		return unixTime(int64(typed)), true
	default:
		return time.Time{}, false
	}
}

func unixTime(value int64) time.Time {
	if value > 1_000_000_000_000 {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(value, 0).UTC()
}

func usagePercent(window map[string]any) (float64, bool) {
	for _, key := range []string{"used_percent", "percent_used"} {
		if value, ok := numberAsFloat64(window[key]); ok {
			return math.Max(0, math.Min(100, value)), true
		}
	}
	return 0, false
}

func numberAsInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case float64:
		return int64(typed), true
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func numberAsFloat64(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
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

func EvaluateObservation(state CredentialState, current Observation, now time.Time, activationDelay time.Duration) WindowDecision {
	now = now.UTC()
	if state.AwaitingStabilization {
		if !state.LastPingAt.IsZero() && now.Sub(state.LastPingAt) >= 10*time.Minute {
			return WindowDecision{Kind: DecisionWaiting, Reason: "stabilization_timeout", ClearStabilizing: true, ClearPending: true}
		}
		if state.LastObservation != nil && sameTime(state.LastObservation.ResetAt, current.ResetAt) && current.ResetAt.After(now) {
			return WindowDecision{Kind: DecisionWaiting, Reason: "window_stabilized", ClearStabilizing: true, ClearPending: true}
		}
		return WindowDecision{Kind: DecisionWaiting, Reason: "awaiting_window_stabilization"}
	}

	previous := state.LastObservation
	if previous == nil {
		return WindowDecision{Kind: DecisionWaiting, Reason: "initial_observation"}
	}

	if pending := state.PendingTransition; pending != nil {
		switch {
		case sameTime(current.ResetAt, pending.ResetAt):
			return WindowDecision{Kind: DecisionExternal, Boundary: pending.Boundary, Reason: "external_activity_confirmed", ClearPending: true}
		case current.ResetAt.After(pending.ResetAt) && current.ObservedAt.Sub(pending.ObservedAt) >= activationDelay && resetSlides(*pending, current) && zeroUsage(current):
			return WindowDecision{Kind: DecisionReady, Boundary: pending.Boundary, Reason: "inactive_window_sliding", ClearPending: true}
		case current.HasUsage && current.UsedPercent > 0:
			return WindowDecision{Kind: DecisionExternal, Boundary: pending.Boundary, Reason: "external_usage_detected", ClearPending: true}
		case current.ResetAt.Before(pending.ResetAt):
			return WindowDecision{Kind: DecisionWaiting, Reason: "reset_moved_backward", ClearPending: true}
		default:
			return WindowDecision{
				Kind: DecisionWaiting, Reason: "confirming_reset_transition",
				PendingTransition: &PendingTransition{Boundary: pending.Boundary, ResetAt: current.ResetAt, ObservedAt: current.ObservedAt},
			}
		}
	}

	switch {
	case sameTime(current.ResetAt, previous.ResetAt):
		if !now.Before(current.ResetAt.Add(activationDelay)) {
			return WindowDecision{Kind: DecisionReady, Boundary: current.ResetAt, Reason: "reset_reached"}
		}
		return WindowDecision{Kind: DecisionWaiting, Reason: "reset_not_reached"}
	case current.ResetAt.Before(previous.ResetAt):
		return WindowDecision{Kind: DecisionWaiting, Reason: "reset_moved_backward", ClearPending: true}
	case current.HasUsage && current.UsedPercent > 0:
		return WindowDecision{Kind: DecisionExternal, Boundary: previous.ResetAt, Reason: "external_usage_detected", ClearPending: true}
	case current.ObservedAt.Sub(previous.ObservedAt) >= activationDelay && resetSlides(PendingTransition{ResetAt: previous.ResetAt, ObservedAt: previous.ObservedAt}, current) && zeroUsage(current):
		return WindowDecision{Kind: DecisionReady, Boundary: previous.ResetAt, Reason: "inactive_window_sliding", ClearPending: true}
	default:
		return WindowDecision{
			Kind: DecisionWaiting, Reason: "confirming_reset_transition",
			PendingTransition: &PendingTransition{Boundary: previous.ResetAt, ResetAt: current.ResetAt, ObservedAt: current.ObservedAt},
		}
	}
}

func resetSlides(previous PendingTransition, current Observation) bool {
	resetDelta := current.ResetAt.Sub(previous.ResetAt)
	observedDelta := current.ObservedAt.Sub(previous.ObservedAt)
	if resetDelta <= 0 || observedDelta <= 0 {
		return false
	}
	tolerance := max(30*time.Second, observedDelta/2)
	return absDuration(resetDelta-observedDelta) <= tolerance
}

func zeroUsage(observation Observation) bool {
	return !observation.HasUsage || observation.UsedPercent <= 0.0001
}

func sameTime(left, right time.Time) bool {
	if left.IsZero() || right.IsZero() {
		return left.IsZero() && right.IsZero()
	}
	if left.Before(right) {
		return right.Sub(left) <= time.Second
	}
	return left.Sub(right) <= time.Second
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
