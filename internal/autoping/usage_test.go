package autoping

import (
	"errors"
	"testing"
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
