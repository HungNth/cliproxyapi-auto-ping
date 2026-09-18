package autoping

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidCredential = errors.New("invalid Codex credential")

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
