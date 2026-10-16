package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

extern int cliproxy_plugin_init(const cliproxy_host_api*, cliproxy_plugin_api*);
extern void cliproxy_set_host(const cliproxy_host_api*);
extern void cliproxy_set_plugin(cliproxy_plugin_api*);
extern int cliproxy_call_host(const char*, const uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxy_free_host(void*, size_t);
*/
import "C"

import (
	"encoding/json"
	"errors"
	"unsafe"
)

//export cliproxyPluginInit
func cliproxyPluginInit(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if host == nil || plugin == nil || uint32(host.abi_version) != 1 {
		return 1
	}
	C.cliproxy_set_host(host)
	plugin.abi_version = 1
	C.cliproxy_set_plugin(plugin)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, n C.size_t, response *C.cliproxy_buffer) (code C.int) {
	if response == nil {
		return 1
	}
	response.ptr = nil
	response.len = 0
	defer func() {
		if recover() != nil {
			writeABI(response, envelope{Error: &rpcError{Code: "plugin_error", Message: "plugin request failed"}})
			code = 1
		}
	}()
	if method == nil || n > 2<<20 {
		writeABI(response, envelope{Error: &rpcError{Code: "invalid_request", Message: "invalid plugin request"}})
		return 1
	}
	var raw []byte
	if request != nil && n > 0 {
		raw = C.GoBytes(unsafe.Pointer(request), C.int(n))
	}
	result, err := dispatch(C.GoString(method), raw)
	if err != nil {
		writeABI(response, envelope{Error: &rpcError{Code: "plugin_error", Message: err.Error()}})
		return 1
	}
	b, err := json.Marshal(result)
	if err != nil {
		return 1
	}
	writeABI(response, envelope{OK: true, Result: b})
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, n C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() { app.stop() }

func writeABI(out *C.cliproxy_buffer, v envelope) {
	b, _ := json.Marshal(v)
	out.ptr = C.CBytes(b)
	out.len = C.size_t(len(b))
}
func nativeHostCall(method string, payload any) (json.RawMessage, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	cm := C.CString(method)
	defer C.free(unsafe.Pointer(cm))
	p := C.CBytes(b)
	defer C.free(p)
	var out C.cliproxy_buffer
	code := C.cliproxy_call_host(cm, (*C.uint8_t)(p), C.size_t(len(b)), &out)
	if out.ptr == nil {
		return nil, errors.New("host callback unavailable")
	}
	defer C.cliproxy_free_host(out.ptr, out.len)
	if out.len > 10<<20 {
		return nil, errors.New("host response too large")
	}
	var env envelope
	if json.Unmarshal(C.GoBytes(out.ptr, C.int(out.len)), &env) != nil || code != 0 || !env.OK {
		return nil, errors.New("host callback failed")
	}
	return env.Result, nil
}
