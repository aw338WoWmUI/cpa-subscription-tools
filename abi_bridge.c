#include "_cgo_export.h"

#if defined(__APPLE__) && defined(__x86_64__)
#include <pthread.h>
#endif

#ifdef _WIN32
#define CPA_EXPORT __declspec(dllexport)
#else
#define CPA_EXPORT __attribute__((visibility("default")))
#endif

static const cliproxy_host_api* host_api;

enum operation { INIT, CALL, FREE, SHUTDOWN, HOST_CALL, HOST_FREE };

typedef struct {
    enum operation operation;
    const cliproxy_host_api* host;
    cliproxy_plugin_api* plugin;
    char* method;
    uint8_t* request;
    size_t len;
    cliproxy_buffer* response;
    void* ptr;
    int result;
} invocation;

static void* invoke(void* context) {
    invocation* call = context;
    switch (call->operation) {
    case INIT:
        call->result = cliproxyPluginInit((cliproxy_host_api*)call->host, call->plugin);
        break;
    case CALL:
        call->result = cliproxyPluginCall(call->method, call->request, call->len, call->response);
        break;
    case FREE:
        cliproxyPluginFree(call->ptr, call->len);
        break;
    case SHUTDOWN:
        cliproxyPluginShutdown();
        break;
    case HOST_CALL:
        call->result = host_api->call(host_api->host_ctx, call->method, call->request, call->len, call->response);
        break;
    case HOST_FREE:
        host_api->free_buffer(call->ptr, call->len);
        break;
    }
    return NULL;
}

static int dispatch(invocation* call) {
#if defined(__APPLE__) && defined(__x86_64__)
    // Go runtimes on Intel macOS must not share a C callback thread's TLS.
    pthread_t thread;
    if (pthread_create(&thread, NULL, invoke, call) != 0) return 1;
    if (pthread_join(thread, NULL) != 0) abort();
#else
    invoke(call);
#endif
    return call->result;
}

CPA_EXPORT int cliproxy_plugin_init(const cliproxy_host_api* host, cliproxy_plugin_api* plugin) {
    invocation call = {.operation = INIT, .host = host, .plugin = plugin};
    return dispatch(&call);
}

static int plugin_call(char* method, uint8_t* request, size_t len, cliproxy_buffer* response) {
    invocation call = {.operation = CALL, .method = method, .request = request, .len = len, .response = response};
    return dispatch(&call);
}

static void plugin_free(void* ptr, size_t len) {
    invocation call = {.operation = FREE, .ptr = ptr, .len = len};
    dispatch(&call);
}

static void plugin_shutdown(void) {
    invocation call = {.operation = SHUTDOWN};
    dispatch(&call);
}

void cliproxy_set_host(const cliproxy_host_api* host) { host_api = host; }

void cliproxy_set_plugin(cliproxy_plugin_api* plugin) {
    plugin->call = plugin_call;
    plugin->free_buffer = plugin_free;
    plugin->shutdown = plugin_shutdown;
}

int cliproxy_call_host(const char* method, const uint8_t* request, size_t len, cliproxy_buffer* response) {
    if (host_api == NULL || host_api->call == NULL) return 1;
    invocation call = {.operation = HOST_CALL, .method = (char*)method, .request = (uint8_t*)request, .len = len, .response = response};
    return dispatch(&call);
}

void cliproxy_free_host(void* ptr, size_t len) {
    if (host_api == NULL || host_api->free_buffer == NULL || ptr == NULL) return;
    invocation call = {.operation = HOST_FREE, .ptr = ptr, .len = len};
    dispatch(&call);
}
