package autoping

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

type fakeHost struct {
	mu sync.Mutex

	auths   []AuthFile
	docs    map[string]AuthDocument
	listErr error

	httpDoFunc       func(HTTPRequest) (HTTPResponse, error)
	httpStreamFunc   func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error)
	modelExecuteFunc func(ModelExecuteRequest) (ModelExecuteResponse, error)

	streams        map[string][]HTTPStreamChunk
	nextStreamID   int
	httpRequests   []HTTPRequest
	streamRequests []HTTPRequest
	modelRequests  []ModelExecuteRequest
	saveCalls      int
	logs           []LogRequest
}

func newFakeHost() *fakeHost {
	return &fakeHost{docs: map[string]AuthDocument{}, streams: map[string][]HTTPStreamChunk{}}
}

func (h *fakeHost) ListAuth(context.Context) ([]AuthFile, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.listErr != nil {
		return nil, h.listErr
	}
	return slices.Clone(h.auths), nil
}

func (h *fakeHost) GetAuth(_ context.Context, authIndex string) (AuthDocument, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	document, ok := h.docs[authIndex]
	if !ok {
		return AuthDocument{}, errors.New("auth not found")
	}
	document.JSON = slices.Clone(document.JSON)
	return document, nil
}

func (h *fakeHost) GetRuntimeAuth(_ context.Context, authIndex string) (AuthFile, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, file := range h.auths {
		if file.AuthIndex == authIndex {
			return file, nil
		}
	}
	return AuthFile{}, errors.New("runtime auth not found")
}

func (h *fakeHost) SaveAuth(_ context.Context, name string, data []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.saveCalls++
	for authIndex, document := range h.docs {
		if document.Name != name {
			continue
		}
		document.JSON = slices.Clone(data)
		h.docs[authIndex] = document
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(data, &raw)
		var priority int
		_ = json.Unmarshal(raw["priority"], &priority)
		for index := range h.auths {
			if h.auths[index].AuthIndex == authIndex {
				h.auths[index].Priority = priority
			}
		}
		return nil
	}
	return errors.New("auth document not found")
}

func (h *fakeHost) HTTPDo(_ context.Context, request HTTPRequest) (HTTPResponse, error) {
	h.mu.Lock()
	h.httpRequests = append(h.httpRequests, request)
	function := h.httpDoFunc
	h.mu.Unlock()
	if function == nil {
		return HTTPResponse{}, errors.New("HTTPDo not configured")
	}
	return function(request)
}

func (h *fakeHost) streamRequestCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.streamRequests)
}

func (h *fakeHost) HTTPDoStream(_ context.Context, request HTTPRequest) (HTTPStreamResponse, error) {
	h.mu.Lock()
	h.streamRequests = append(h.streamRequests, request)
	function := h.httpStreamFunc
	h.mu.Unlock()
	if function == nil {
		return HTTPStreamResponse{}, errors.New("HTTPDoStream not configured")
	}
	response, chunks, err := function(request)
	if err != nil {
		return HTTPStreamResponse{}, err
	}
	h.mu.Lock()
	h.nextStreamID++
	if response.StreamID == "" {
		response.StreamID = "stream-" + strconv.Itoa(h.nextStreamID)
	}
	h.streams[response.StreamID] = slices.Clone(chunks)
	h.mu.Unlock()
	return response, nil
}

func (h *fakeHost) HTTPStreamRead(_ context.Context, streamID string) (HTTPStreamChunk, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	chunks := h.streams[streamID]
	if len(chunks) == 0 {
		return HTTPStreamChunk{Done: true}, nil
	}
	chunk := chunks[0]
	h.streams[streamID] = chunks[1:]
	return chunk, nil
}

func (h *fakeHost) HTTPStreamClose(_ context.Context, streamID string) error {
	h.mu.Lock()
	delete(h.streams, streamID)
	h.mu.Unlock()
	return nil
}

func (h *fakeHost) ModelExecute(_ context.Context, request ModelExecuteRequest) (ModelExecuteResponse, error) {
	h.mu.Lock()
	h.modelRequests = append(h.modelRequests, request)
	function := h.modelExecuteFunc
	h.mu.Unlock()
	if function == nil {
		return ModelExecuteResponse{}, errors.New("ModelExecute not configured")
	}
	return function(request)
}

func (h *fakeHost) Log(_ context.Context, request LogRequest) error {
	h.mu.Lock()
	h.logs = append(h.logs, request)
	h.mu.Unlock()
	return nil
}

func successStream() (HTTPStreamResponse, []HTTPStreamChunk, error) {
	return HTTPStreamResponse{StatusCode: http.StatusOK}, []HTTPStreamChunk{{
		Payload: []byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\"}}\n\n"),
		Done:    true,
	}}, nil
}

func credential(id, token string, priority int) (AuthFile, AuthDocument) {
	authIndex := "index-" + id
	file := AuthFile{ID: id, AuthIndex: authIndex, Name: id + ".json", Provider: "codex", Status: "active", Priority: priority}
	data, _ := json.Marshal(map[string]any{"type": "codex", "access_token": token, "account_id": "account-" + id, "priority": priority})
	return file, AuthDocument{AuthIndex: authIndex, Name: file.Name, JSON: data}
}

func newTestRuntime(t *testing.T, host Host, options Options) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(host, []byte(testManifestYAML), options)
	if err != nil {
		t.Fatalf("construct runtime: %v", err)
	}
	return runtime
}

func testConfig(t *testing.T, runtime *Runtime, data string) Config {
	t.Helper()
	cfg, err := runtime.parseConfig([]byte(data))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}
