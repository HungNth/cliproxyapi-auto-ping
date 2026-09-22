package autoping

import (
	"net/http"
	"testing"
)

func TestFetchUsageObservationClassifiesFailures(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantFailure FailureKind
	}{
		{name: "authentication", status: http.StatusUnauthorized, wantFailure: FailureAuth},
		{name: "rate limit", status: http.StatusTooManyRequests, wantFailure: FailureRetryable},
		{name: "server failure", status: http.StatusBadGateway, wantFailure: FailureRetryable},
		{name: "client failure", status: http.StatusBadRequest, wantFailure: FailureBusiness},
		{name: "missing five-hour window", status: http.StatusOK, body: `{"rate_limit":{"limit_window_seconds":3600,"reset_at":1790084449}}`, wantFailure: FailureBusiness},
		{name: "malformed payload", status: http.StatusOK, body: `{`, wantFailure: FailureBusiness},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host := newFakeHost()
			host.httpDoFunc = func(HTTPRequest) (HTTPResponse, error) {
				return HTTPResponse{StatusCode: tc.status, Body: []byte(tc.body)}, nil
			}
			runtime := newTestRuntime(t, host, Options{})
			cfg := testConfig(t, runtime, "")
			_, failure, _ := runtime.fetchUsageObservation(t.Context(), cfg, AuthMaterial{AccessToken: "token-a"})
			if failure != tc.wantFailure {
				t.Fatalf("failure = %q, want %q", failure, tc.wantFailure)
			}
		})
	}
}
