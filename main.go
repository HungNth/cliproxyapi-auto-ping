//go:build cgo

package main

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct cliproxy_buffer {
    uint8_t* ptr;
    size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void* host_ctx, const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response);
typedef void (*cliproxy_host_free_buffer_fn)(void* ptr, size_t len);

typedef struct cliproxy_host_api {
    uint32_t abi_version;
    void* host_ctx;
    cliproxy_host_call_fn call;
    cliproxy_host_free_buffer_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char* method, uint8_t* request, size_t request_len, cliproxy_buffer* response);
typedef void (*cliproxy_plugin_free_buffer_fn)(void* ptr, size_t len);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct cliproxy_plugin_api {
    uint32_t abi_version;
    cliproxy_plugin_call_fn call;
    cliproxy_plugin_free_buffer_fn free_buffer;
    cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

#ifdef _WIN32
#define CPA_PLUGIN_EXPORT __declspec(dllexport)
#else
#define CPA_PLUGIN_EXPORT
#endif

extern CPA_PLUGIN_EXPORT int autoPingPluginCall(char* method, uint8_t* request, size_t request_len, cliproxy_buffer* response);
extern CPA_PLUGIN_EXPORT void autoPingPluginFreeBuffer(void* ptr, size_t len);
extern CPA_PLUGIN_EXPORT void autoPingPluginShutdown(void);

static inline void set_auto_ping_plugin_api(cliproxy_plugin_api* plugin) {
    plugin->abi_version = 1;
    plugin->call = autoPingPluginCall;
    plugin->free_buffer = autoPingPluginFreeBuffer;
    plugin->shutdown = autoPingPluginShutdown;
}

static const cliproxy_host_api* stored_host;

static inline void store_auto_ping_host_api(const cliproxy_host_api* host) {
    stored_host = host;
}

static inline int call_auto_ping_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
    if (stored_host == NULL || stored_host->call == NULL) {
        return 1;
    }
    return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static inline void free_auto_ping_host_buffer(void* ptr, size_t len) {
    if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
        stored_host->free_buffer(ptr, len);
    }
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/HungNth/cliproxyapi-auto-ping/internal/autoping"
)

var (
	runtimeMu  sync.RWMutex
	cpaRuntime *autoping.Runtime
)

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return -1
	}
	C.store_auto_ping_host_api(host)
	runtime, err := autoping.NewRuntime(hostCallbackAdapter{}, pluginManifest, autoping.Options{})
	if err != nil {
		return -1
	}
	runtimeMu.Lock()
	cpaRuntime = runtime
	runtimeMu.Unlock()
	C.set_auto_ping_plugin_api(plugin)
	return 0
}

//export autoPingPluginCall
func autoPingPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response == nil {
		return -1
	}
	methodName := ""
	if method != nil {
		methodName = C.GoString(method)
	}
	requestBytes, ok := copyRequestBytes(request, requestLen)
	if !ok {
		return writeResponse(response, []byte(`{"ok":false,"error":{"code":"invalid_request","message":"request length is invalid"}}`))
	}
	runtimeMu.RLock()
	current := cpaRuntime
	runtimeMu.RUnlock()
	if current == nil {
		return writeResponse(response, []byte(`{"ok":false,"error":{"code":"not_initialized","message":"plugin runtime is not initialized"}}`))
	}
	return writeResponse(response, current.Handle(context.Background(), methodName, requestBytes))
}

//export autoPingPluginFreeBuffer
func autoPingPluginFreeBuffer(ptr unsafe.Pointer, length C.size_t) {
	_ = length
	C.free(ptr)
}

//export autoPingPluginShutdown
func autoPingPluginShutdown() {
	runtimeMu.RLock()
	current := cpaRuntime
	runtimeMu.RUnlock()
	_ = current.Shutdown(context.Background())
}

func copyRequestBytes(request *C.uint8_t, requestLen C.size_t) ([]byte, bool) {
	length := int(requestLen)
	if length < 0 || C.size_t(length) != requestLen {
		return nil, false
	}
	if length == 0 {
		return nil, true
	}
	if request == nil {
		return nil, false
	}
	source := unsafe.Slice((*byte)(unsafe.Pointer(request)), length)
	return append([]byte(nil), source...), true
}

func writeResponse(response *C.cliproxy_buffer, data []byte) C.int {
	response.ptr = nil
	response.len = 0
	if len(data) == 0 {
		return 0
	}
	pointer := C.malloc(C.size_t(len(data)))
	if pointer == nil {
		return -1
	}
	C.memcpy(pointer, unsafe.Pointer(&data[0]), C.size_t(len(data)))
	response.ptr = (*C.uint8_t)(pointer)
	response.len = C.size_t(len(data))
	return 0
}

type hostCallbackAdapter struct{}

func (hostCallbackAdapter) ListAuth(ctx context.Context) ([]autoping.AuthFile, error) {
	var response struct {
		Files []autoping.AuthFile `json:"files"`
	}
	if err := callHost(ctx, "host.auth.list", map[string]any{}, &response); err != nil {
		return nil, err
	}
	return response.Files, nil
}

func (hostCallbackAdapter) GetAuth(ctx context.Context, authIndex string) (autoping.AuthDocument, error) {
	var response autoping.AuthDocument
	if err := callHost(ctx, "host.auth.get", map[string]string{"auth_index": authIndex}, &response); err != nil {
		return autoping.AuthDocument{}, err
	}
	return response, nil
}

func (hostCallbackAdapter) GetRuntimeAuth(ctx context.Context, authIndex string) (autoping.AuthFile, error) {
	var response struct {
		Auth autoping.AuthFile `json:"auth"`
	}
	if err := callHost(ctx, "host.auth.get_runtime", map[string]string{"auth_index": authIndex}, &response); err != nil {
		return autoping.AuthFile{}, err
	}
	return response.Auth, nil
}

func (hostCallbackAdapter) SaveAuth(ctx context.Context, name string, data []byte) error {
	return callHost(ctx, "host.auth.save", map[string]any{"name": name, "json": json.RawMessage(data)}, nil)
}

func (hostCallbackAdapter) HTTPDo(ctx context.Context, request autoping.HTTPRequest) (autoping.HTTPResponse, error) {
	var response autoping.HTTPResponse
	if err := callHost(ctx, "host.http.do", request, &response); err != nil {
		return autoping.HTTPResponse{}, err
	}
	return response, nil
}

func (hostCallbackAdapter) HTTPDoStream(ctx context.Context, request autoping.HTTPRequest) (autoping.HTTPStreamResponse, error) {
	var response autoping.HTTPStreamResponse
	if err := callHost(ctx, "host.http.do_stream", request, &response); err != nil {
		return autoping.HTTPStreamResponse{}, err
	}
	return response, nil
}

func (hostCallbackAdapter) HTTPStreamRead(ctx context.Context, streamID string) (autoping.HTTPStreamChunk, error) {
	var response autoping.HTTPStreamChunk
	if err := callHost(ctx, "host.http.stream_read", map[string]string{"stream_id": streamID}, &response); err != nil {
		return autoping.HTTPStreamChunk{}, err
	}
	return response, nil
}

func (hostCallbackAdapter) HTTPStreamClose(ctx context.Context, streamID string) error {
	return callHost(ctx, "host.http.stream_close", map[string]string{"stream_id": streamID}, nil)
}

func (hostCallbackAdapter) ModelExecute(ctx context.Context, request autoping.ModelExecuteRequest) (autoping.ModelExecuteResponse, error) {
	var response autoping.ModelExecuteResponse
	if err := callHost(ctx, "host.model.execute", request, &response); err != nil {
		return autoping.ModelExecuteResponse{}, err
	}
	return response, nil
}

func (hostCallbackAdapter) Log(ctx context.Context, request autoping.LogRequest) error {
	return callHost(ctx, "host.log", request, nil)
}

func callHost(ctx context.Context, method string, payload any, target any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	request, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal host callback %s: %w", method, err)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var requestPointer *C.uint8_t
	if len(request) > 0 {
		cRequest := C.CBytes(request)
		if cRequest == nil {
			return errors.New("allocate host callback request")
		}
		defer C.free(cRequest)
		requestPointer = (*C.uint8_t)(cRequest)
	}
	var response C.cliproxy_buffer
	callCode := C.call_auto_ping_host_api(cMethod, requestPointer, C.size_t(len(request)), &response)
	var responseBytes []byte
	if response.ptr != nil && response.len > 0 {
		responseBytes = C.GoBytes(unsafe.Pointer(response.ptr), C.int(response.len))
	}
	if response.ptr != nil {
		C.free_auto_ping_host_buffer(unsafe.Pointer(response.ptr), response.len)
	}
	if len(responseBytes) == 0 {
		return fmt.Errorf("host callback %s returned no response", method)
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(responseBytes, &envelope); err != nil {
		return fmt.Errorf("decode host callback %s: %w", method, err)
	}
	if !envelope.OK {
		if envelope.Error != nil {
			return fmt.Errorf("host callback %s: %s", method, envelope.Error.Code)
		}
		return fmt.Errorf("host callback %s failed", method)
	}
	if callCode != 0 {
		return fmt.Errorf("host callback %s returned code %d", method, int(callCode))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if target == nil || len(envelope.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, target); err != nil {
		return fmt.Errorf("decode host callback result %s: %w", method, err)
	}
	return nil
}

var _ autoping.Host = hostCallbackAdapter{}
