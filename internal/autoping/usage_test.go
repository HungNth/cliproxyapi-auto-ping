package autoping

import (
	"errors"
	"testing"
	"time"
)

func TestParseAuthMaterialExtractsTokens(t *testing.T) {
	cases := []struct {
		name      string
		json      string
		wantToken string
		wantAcct  string
	}{
		{
			name:      "flat payload",
			json:      `{"access_token": "token-flat", "account_id": "acct-flat"}`,
			wantToken: "token-flat",
			wantAcct:  "acct-flat",
		},
		{
			name:      "nested tokens object",
			json:      `{"tokens": {"oauth_access_token": "token-nested", "chatgpt_account_id": "acct-nested"}}`,
			wantToken: "token-nested",
			wantAcct:  "acct-nested",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			material, err := ParseAuthMaterial([]byte(tc.json))
			if err != nil {
				t.Fatal(err)
			}
			if material.AccessToken != tc.wantToken {
				t.Fatalf("token = %q, want %q", material.AccessToken, tc.wantToken)
			}
			if material.AccountID != tc.wantAcct {
				t.Fatalf("account id = %q, want %q", material.AccountID, tc.wantAcct)
			}
		})
	}
}

func TestParseAuthMaterialRejectsMissingToken(t *testing.T) {
	_, err := ParseAuthMaterial([]byte(`{"account_id": "acct-only"}`))
	if !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("expected ErrInvalidCredential, got %v", err)
	}
}

func TestParseFiveHourObservationLossless(t *testing.T) {
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	expectedReset := time.Unix(1790084449, 0).UTC()

	cases := []struct {
		name      string
		payload   string
		wantReset time.Time
		wantErr   error
	}{
		{
			name: "production wire contract integer seconds",
			payload: `{
				"rate_limits_by_limit_id": {
					"codex": {
						"limit_window_seconds": 18000,
						"reset_at": 1790084449
					}
				}
			}`,
			wantReset: expectedReset,
		},
		{
			name: "flat rate_limit object integer seconds",
			payload: `{
				"rate_limit": {
					"limit_window_seconds": 18000,
					"reset_at": 1790084449
				}
			}`,
			wantReset: expectedReset,
		},
		{
			name: "millisecond epoch fallback",
			payload: `{
				"rate_limits": [
					{
						"limit_window_seconds": 18000,
						"reset_at": 1790084449000
					}
				]
			}`,
			wantReset: expectedReset,
		},
		{
			name: "rfc3339 string fallback",
			payload: `{
				"rate_limit": {
					"limit_window_seconds": 18000,
					"reset_at": "2026-09-22T13:40:49Z"
				}
			}`,
			wantReset: time.Date(2026, 9, 22, 13, 40, 49, 0, time.UTC),
		},
		{
			name: "strictly rejects float reset_at",
			payload: `{
				"rate_limit": {
					"limit_window_seconds": 18000,
					"reset_at": 1790084449.123
				}
			}`,
			wantErr: ErrInvalidUsage,
		},
		{
			name: "no 5-hour window returns ErrNoFiveHourWindow",
			payload: `{
				"rate_limit": {
					"limit_window_seconds": 3600,
					"reset_at": 1790084449
				}
			}`,
			wantErr: ErrNoFiveHourWindow,
		},
		{
			name:    "malformed JSON returns ErrInvalidUsage",
			payload: `{malformed`,
			wantErr: ErrInvalidUsage,
		},
		{
			name: "ignores unrelated deeply nested five-hour window",
			payload: `{
				"unrelated": {
					"nested": {
						"limit_window_seconds": 18000,
						"reset_at": 1790084449
					}
				}
			}`,
			wantErr: ErrNoFiveHourWindow,
		},
		{
			name: "rejects ambiguous direct five-hour windows",
			payload: `{
				"rate_limits_by_limit_id": {
					"codex": {
						"first": {"limit_window_seconds": 18000, "reset_at": 1790084449},
						"second": {"limit_window_seconds": 18000, "reset_at": 1790102449}
					}
				}
			}`,
			wantErr: ErrInvalidUsage,
		},
		{
			name:    "rejects trailing JSON value",
			payload: `{"rate_limit":{"limit_window_seconds":18000,"reset_at":1790084449}} {}`,
			wantErr: ErrInvalidUsage,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs, err := ParseFiveHourObservation([]byte(tc.payload), now)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got error %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !obs.ResetAt.Equal(tc.wantReset) {
				t.Fatalf("reset_at = %v, want %v", obs.ResetAt, tc.wantReset)
			}
			if obs.LimitWindowSeconds != 18000 {
				t.Fatalf("limit_window_seconds = %d, want 18000", obs.LimitWindowSeconds)
			}
		})
	}
}
