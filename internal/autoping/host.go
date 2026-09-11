package autoping

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Host interface {
	ListAuth(context.Context) ([]AuthFile, error)
	GetAuth(context.Context, string) (AuthDocument, error)
	GetRuntimeAuth(context.Context, string) (AuthFile, error)
	SaveAuth(context.Context, string, []byte) error
	HTTPDo(context.Context, HTTPRequest) (HTTPResponse, error)
	HTTPDoStream(context.Context, HTTPRequest) (HTTPStreamResponse, error)
	HTTPStreamRead(context.Context, string) (HTTPStreamChunk, error)
	HTTPStreamClose(context.Context, string) error
	ModelExecute(context.Context, ModelExecuteRequest) (ModelExecuteResponse, error)
	Log(context.Context, LogRequest) error
}

type AuthFile struct {
	ID            string    `json:"id,omitempty"`
	AuthIndex     string    `json:"auth_index,omitempty"`
	Name          string    `json:"name"`
	Type          string    `json:"type,omitempty"`
	Provider      string    `json:"provider,omitempty"`
	Label         string    `json:"label,omitempty"`
	Status        string    `json:"status,omitempty"`
	StatusMessage string    `json:"status_message,omitempty"`
	Disabled      bool      `json:"disabled,omitempty"`
	Unavailable   bool      `json:"unavailable,omitempty"`
	RuntimeOnly   bool      `json:"runtime_only,omitempty"`
	ModTime       time.Time `json:"modtime,omitzero"`
	UpdatedAt     time.Time `json:"updated_at,omitzero"`
	LastRefresh   time.Time `json:"last_refresh,omitzero"`
	Account       string    `json:"account,omitempty"`
	Email         string    `json:"email,omitempty"`
	Priority      int       `json:"priority,omitzero"`
}

func (a AuthFile) CredentialID() string {
	return firstNonBlank(a.ID, a.Name, a.AuthIndex)
}

func (a AuthFile) VersionKey() string {
	return fmt.Sprintf("%s|%s|%s|%s|%s", a.ModTime.UTC().Format(time.RFC3339Nano), a.UpdatedAt.UTC().Format(time.RFC3339Nano), a.LastRefresh.UTC().Format(time.RFC3339Nano), a.Status, a.StatusMessage)
}

type AuthDocument struct {
	AuthIndex string          `json:"auth_index"`
	Name      string          `json:"name,omitempty"`
	Path      string          `json:"path,omitempty"`
	JSON      json.RawMessage `json:"json"`
}

type HTTPRequest struct {
	Method  string      `json:"Method"`
	URL     string      `json:"URL"`
	Headers http.Header `json:"Headers,omitempty"`
	Body    []byte      `json:"Body,omitempty"`
}

type HTTPResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

func (r *HTTPResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		StatusCode      *int            `json:"StatusCode"`
		StatusCodeSnake *int            `json:"status_code"`
		Headers         http.Header     `json:"Headers"`
		HeadersLower    http.Header     `json:"headers"`
		Body            json.RawMessage `json:"Body"`
		BodyLower       json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.StatusCode != nil {
		r.StatusCode = *raw.StatusCode
	} else if raw.StatusCodeSnake != nil {
		r.StatusCode = *raw.StatusCodeSnake
	}
	r.Headers = raw.Headers
	if r.Headers == nil {
		r.Headers = raw.HeadersLower
	}
	body := raw.Body
	if len(body) == 0 {
		body = raw.BodyLower
	}
	decoded, err := decodeByteField(body)
	if err != nil {
		return fmt.Errorf("decode HTTP body: %w", err)
	}
	r.Body = decoded
	return nil
}

type HTTPStreamResponse struct {
	StatusCode int
	Headers    http.Header
	StreamID   string
}

func (r *HTTPStreamResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		StatusCode      *int        `json:"StatusCode"`
		StatusCodeSnake *int        `json:"status_code"`
		Headers         http.Header `json:"Headers"`
		HeadersLower    http.Header `json:"headers"`
		StreamID        string      `json:"StreamID"`
		StreamIDSnake   string      `json:"stream_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.StatusCode != nil {
		r.StatusCode = *raw.StatusCode
	} else if raw.StatusCodeSnake != nil {
		r.StatusCode = *raw.StatusCodeSnake
	}
	r.Headers = raw.Headers
	if r.Headers == nil {
		r.Headers = raw.HeadersLower
	}
	r.StreamID = firstNonBlank(raw.StreamID, raw.StreamIDSnake)
	return nil
}

type HTTPStreamChunk struct {
	Payload []byte
	Error   string
	Done    bool
}

func (c *HTTPStreamChunk) UnmarshalJSON(data []byte) error {
	var raw struct {
		Payload      json.RawMessage `json:"Payload"`
		PayloadLower json.RawMessage `json:"payload"`
		Error        string          `json:"Error"`
		ErrorLower   string          `json:"error"`
		Done         *bool           `json:"Done"`
		DoneLower    *bool           `json:"done"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	payload := raw.Payload
	if len(payload) == 0 {
		payload = raw.PayloadLower
	}
	decoded, err := decodeByteField(payload)
	if err != nil {
		return fmt.Errorf("decode stream payload: %w", err)
	}
	c.Payload = decoded
	c.Error = firstNonBlank(raw.Error, raw.ErrorLower)
	if raw.Done != nil {
		c.Done = *raw.Done
	} else if raw.DoneLower != nil {
		c.Done = *raw.DoneLower
	}
	return nil
}

type ModelExecuteRequest struct {
	EntryProtocol string      `json:"entry_protocol"`
	ExitProtocol  string      `json:"exit_protocol"`
	Model         string      `json:"model"`
	Stream        bool        `json:"stream"`
	Body          []byte      `json:"body"`
	Headers       http.Header `json:"headers,omitempty"`
}

type ModelExecuteResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

func (r *ModelExecuteResponse) UnmarshalJSON(data []byte) error {
	var response HTTPResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	r.StatusCode = response.StatusCode
	r.Headers = response.Headers
	r.Body = response.Body
	return nil
}

type LogRequest struct {
	Level   string         `json:"level,omitempty"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

func decodeByteField(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		if encoded == "" {
			return nil, nil
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err == nil {
			return decoded, nil
		}
		return []byte(encoded), nil
	}
	var bytesValue []byte
	if err := json.Unmarshal(raw, &bytesValue); err != nil {
		return nil, err
	}
	return bytesValue, nil
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
