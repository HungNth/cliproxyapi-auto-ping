package autoping

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	antigravityQuotaURL       = "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"
	antigravityCodeAssistURL  = "https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	defaultAntigravityProject = "aicode-consumers"
)

var (
	ErrInvalidAntigravityUsage = errors.New("invalid Antigravity usage payload")
	ErrAmbiguousAntigravity5h  = errors.New("ambiguous Antigravity five-hour quota bucket")
)

type AntigravityQuotaObservation struct {
	Gemini5hReset time.Time
	Claude5hReset time.Time
}

func (o AntigravityQuotaObservation) ResetForTarget(targetKey string) (time.Time, bool) {
	switch targetKey {
	case TargetKeyGemini5h:
		return o.Gemini5hReset, !o.Gemini5hReset.IsZero()
	case TargetKeyClaude5h:
		return o.Claude5hReset, !o.Claude5hReset.IsZero()
	default:
		return time.Time{}, false
	}
}

func ParseAntigravityQuotaSummary(data []byte) (AntigravityQuotaObservation, error) {
	if len(bytesTrim(data)) == 0 {
		return AntigravityQuotaObservation{}, fmt.Errorf("%w: empty payload", ErrInvalidAntigravityUsage)
	}

	var root map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return AntigravityQuotaObservation{}, fmt.Errorf("%w: decode JSON: %v", ErrInvalidAntigravityUsage, err)
	}

	rawGroups, ok := root["groups"].([]any)
	if !ok || len(rawGroups) == 0 {
		return AntigravityQuotaObservation{}, fmt.Errorf("%w: missing groups array", ErrInvalidAntigravityUsage)
	}

	var obs AntigravityQuotaObservation

	for _, rawGroup := range rawGroups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		name := strings.ToLower(firstString(group, "displayName", "display_name"))
		desc := strings.ToLower(firstString(group, "description"))
		isGemini := strings.Contains(name, "gemini") || strings.Contains(desc, "gemini")
		isClaude := strings.Contains(name, "claude") || strings.Contains(name, "gpt") || strings.Contains(name, "3p") ||
			strings.Contains(desc, "claude") || strings.Contains(desc, "gpt") || strings.Contains(desc, "3p")

		rawBuckets, ok := group["buckets"].([]any)
		if !ok {
			continue
		}

		for _, rawBucket := range rawBuckets {
			bucket, ok := rawBucket.(map[string]any)
			if !ok {
				continue
			}
			window := strings.ToLower(firstString(bucket, "window"))
			bucketID := strings.ToLower(firstString(bucket, "bucketId", "bucket_id"))

			isWeekly := window == "weekly" || strings.Contains(bucketID, "weekly")
			if isWeekly {
				continue
			}
			is5h := window == "5h" || window == "five-hour" || window == "five_hour" || strings.Contains(bucketID, "5h")
			if !is5h {
				continue
			}

			rawReset := firstString(bucket, "resetTime", "reset_time")
			if rawReset == "" {
				continue
			}
			parsedReset, err := parseRFC3339Time(rawReset)
			if err != nil {
				return AntigravityQuotaObservation{}, fmt.Errorf("%w: invalid resetTime %q: %v", ErrInvalidAntigravityUsage, rawReset, err)
			}

			if isGemini {
				if !obs.Gemini5hReset.IsZero() && !obs.Gemini5hReset.Equal(parsedReset) {
					return AntigravityQuotaObservation{}, ErrAmbiguousAntigravity5h
				}
				obs.Gemini5hReset = parsedReset
			}
			if isClaude {
				if !obs.Claude5hReset.IsZero() && !obs.Claude5hReset.Equal(parsedReset) {
					return AntigravityQuotaObservation{}, ErrAmbiguousAntigravity5h
				}
				obs.Claude5hReset = parsedReset
			}
		}
	}

	return obs, nil
}

func parseRFC3339Time(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, errors.New("unrecognized RFC3339 layout")
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func extractAntigravityProjectID(jsonData []byte) string {
	var root map[string]any
	if err := json.Unmarshal(jsonData, &root); err != nil {
		return ""
	}
	if pid := firstString(root, "project_id", "projectId"); pid != "" {
		return pid
	}
	if metadata, ok := root["metadata"].(map[string]any); ok {
		if pid := firstString(metadata, "project_id", "projectId"); pid != "" {
			return pid
		}
	}
	return ""
}

func (r *Runtime) resolveAntigravityProjectID(ctx context.Context, cfg Config, authData []byte, token string) (string, error) {
	if projectID := extractAntigravityProjectID(authData); projectID != "" {
		return projectID, nil
	}

	// Fallback to loadCodeAssist for project discovery
	codeAssistReqBody, _ := json.Marshal(map[string]any{
		"metadata": map[string]string{
			"ideType":    "ANTIGRAVITY",
			"platform":   "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
		},
	})
	headers := http.Header{
		"Authorization": {"Bearer " + token},
		"Content-Type":  {"application/json"},
		"User-Agent":    {"antigravity/cli/1.0.13 (aidev_client; os_type=darwin; arch=arm64)"},
	}
	requestCtx, cancel := context.WithTimeoutCause(ctx, cfg.RequestTimeout, errors.New("project discovery timeout"))
	defer cancel()

	resp, err := r.host.HTTPDo(requestCtx, HTTPRequest{
		Method:  http.MethodPost,
		URL:     antigravityCodeAssistURL,
		Headers: headers,
		Body:    codeAssistReqBody,
	})
	if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var assistResp map[string]any
		if err := json.Unmarshal(resp.Body, &assistResp); err == nil {
			if proj, ok := assistResp["cloudaicompanionProject"].(map[string]any); ok {
				if id, ok := proj["id"].(string); ok && strings.TrimSpace(id) != "" {
					return strings.TrimSpace(id), nil
				}
			}
		}
	}

	return defaultAntigravityProject, nil
}

func (r *Runtime) fetchAntigravityQuotaObservation(ctx context.Context, cfg Config, file AuthFile) (AntigravityQuotaObservation, FailureKind, string, *time.Duration) {
	doc, err := r.host.GetAuth(ctx, file.AuthIndex)
	if err != nil {
		return AntigravityQuotaObservation{}, FailureRetryable, fmt.Sprintf("read auth document: %v", err), nil
	}
	material, err := ParseAuthMaterial(doc.JSON)
	if err != nil || material.AccessToken == "" {
		return AntigravityQuotaObservation{}, FailureAuth, "missing access token in auth document", nil
	}

	projectID, err := r.resolveAntigravityProjectID(ctx, cfg, doc.JSON, material.AccessToken)
	if err != nil {
		return AntigravityQuotaObservation{}, FailureRetryable, fmt.Sprintf("resolve project ID: %v", err), nil
	}

	quotaReqBody, err := json.Marshal(map[string]string{"project": projectID})
	if err != nil {
		return AntigravityQuotaObservation{}, FailureBusiness, fmt.Sprintf("encode quota request: %v", err), nil
	}
	headers := http.Header{
		"Authorization": {"Bearer " + material.AccessToken},
		"Content-Type":  {"application/json"},
		"User-Agent":    {"antigravity/cli/1.0.13 (aidev_client; os_type=darwin; arch=arm64)"},
	}
	requestCtx, cancel := context.WithTimeoutCause(ctx, cfg.RequestTimeout, errors.New("quota request timeout"))
	defer cancel()

	resp, err := r.host.HTTPDo(requestCtx, HTTPRequest{
		Method:  http.MethodPost,
		URL:     antigravityQuotaURL,
		Headers: headers,
		Body:    quotaReqBody,
	})
	if err != nil {
		failure := FailureTransport
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			failure = FailureTimeout
		}
		return AntigravityQuotaObservation{}, failure, fmt.Sprintf("quota transport error: %v", err), nil
	}

	var retryAfter *time.Duration
	if resp.Headers != nil {
		retryAfter = parseRetryAfter(resp.Headers.Get("Retry-After"), r.now())
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// Re-read credential material to allow host persistence/refresh lag to settle
		freshDoc, freshErr := r.host.GetAuth(ctx, file.AuthIndex)
		if freshErr == nil {
			freshMaterial, parseErr := ParseAuthMaterial(freshDoc.JSON)
			if parseErr == nil && freshMaterial.AccessToken != "" {
				freshHeaders := http.Header{
					"Authorization": {"Bearer " + freshMaterial.AccessToken},
					"Content-Type":  {"application/json"},
					"User-Agent":    {"antigravity/cli/1.0.13 (aidev_client; os_type=darwin; arch=arm64)"},
				}
				freshCtx, freshCancel := context.WithTimeoutCause(ctx, cfg.RequestTimeout, errors.New("quota retry request timeout"))
				defer freshCancel()
				freshResp, freshDoErr := r.host.HTTPDo(freshCtx, HTTPRequest{
					Method:  http.MethodPost,
					URL:     antigravityQuotaURL,
					Headers: freshHeaders,
					Body:    quotaReqBody,
				})
				if freshDoErr == nil {
					if freshResp.StatusCode >= 200 && freshResp.StatusCode < 300 {
						obs, err := ParseAntigravityQuotaSummary(freshResp.Body)
						if err != nil {
							return AntigravityQuotaObservation{}, FailureBusiness, fmt.Sprintf("parse quota summary: %v", err), nil
						}
						return obs, FailureNone, "", nil
					}
					if freshResp.StatusCode == http.StatusUnauthorized || freshResp.StatusCode == http.StatusForbidden {
						return AntigravityQuotaObservation{}, FailureAuth, fmt.Sprintf("quota authorization error with fresh auth: HTTP %d", freshResp.StatusCode), nil
					}
				}
			}
		}
		return AntigravityQuotaObservation{}, FailureAuth, fmt.Sprintf("quota authorization error: HTTP %d", resp.StatusCode), retryAfter
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		failure := FailureBusiness
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			failure = FailureRetryable
		}
		return AntigravityQuotaObservation{}, failure, fmt.Sprintf("quota HTTP %d", resp.StatusCode), retryAfter
	}

	obs, err := ParseAntigravityQuotaSummary(resp.Body)
	if err != nil {
		return AntigravityQuotaObservation{}, FailureBusiness, fmt.Sprintf("parse quota summary: %v", err), nil
	}

	return obs, FailureNone, "", retryAfter
}
