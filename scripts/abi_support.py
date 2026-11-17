"""Shared CPA native ABI v1 test client; mock credentials, no network calls."""

import base64
import ctypes as C
import json

NATIVE_IDENTITY = "You are Antigravity, a powerful agentic AI assistant designed by the Google Deepmind team."
NATIVE_SYSTEM = (
    "<identity>\n" + NATIVE_IDENTITY
    + "\nYou are working with a USER to solve their task.\n"
    "The USER will send you requests, which you must always prioritize addressing. "
    "User requests are enclosed within <USER_REQUEST> tags.\n</identity>"
)


class Buffer(C.Structure):
    _fields_ = [("ptr", C.c_void_p), ("len", C.c_size_t)]


Call = C.CFUNCTYPE(C.c_int, C.c_char_p, C.POINTER(C.c_uint8), C.c_size_t, C.POINTER(Buffer))
Free = C.CFUNCTYPE(None, C.c_void_p, C.c_size_t)
Shutdown = C.CFUNCTYPE(None)
HostCall = C.CFUNCTYPE(C.c_int, C.c_void_p, C.c_char_p, C.POINTER(C.c_uint8), C.c_size_t, C.POINTER(Buffer))


class Host(C.Structure):
    _fields_ = [("abi", C.c_uint32), ("ctx", C.c_void_p), ("call", HostCall), ("free", Free)]


class API(C.Structure):
    _fields_ = [("abi", C.c_uint32), ("call", Call), ("free", Free), ("shutdown", Shutdown)]


class Plugin:
    def __init__(self, library):
        self.provider = "agy"
        self.libc = C.CDLL(None)
        self.libc.malloc.argtypes = [C.c_size_t]
        self.libc.malloc.restype = C.c_void_p
        self.libc.free.argtypes = [C.c_void_p]
        self.host_call = HostCall(self._host_call)
        self.host_free = Free(lambda ptr, size: self.libc.free(ptr))
        self.host = Host(1, None, self.host_call, self.host_free)
        self.api = API()
        self.library = C.CDLL(str(library))
        self.library.cliproxy_plugin_init.argtypes = [C.POINTER(Host), C.POINTER(API)]
        assert self.library.cliproxy_plugin_init(C.byref(self.host), C.byref(self.api)) == 0
        assert self.api.abi == 1

    def _host_call(self, ctx, method, request, length, output):
        if method == b"host.auth.get_runtime":
            envelope = {"ok": True, "result": {"auth": {"provider": self.provider}}}
        elif method == b"host.log":
            envelope = {"ok": True, "result": {}}
        else:
            envelope = {"ok": False, "error": {"code": "unsupported", "message": "unsupported mock callback"}}
        data = json.dumps(envelope).encode()
        ptr = self.libc.malloc(len(data))
        C.memmove(ptr, data, len(data))
        output[0].ptr, output[0].len = ptr, len(data)
        return 0

    def call(self, method, payload):
        data = json.dumps(payload).encode()
        array = (C.c_uint8 * len(data)).from_buffer_copy(data)
        output = Buffer()
        rc = self.api.call(method.encode(), array, len(data), C.byref(output))
        try:
            envelope = json.loads(C.string_at(output.ptr, output.len))
        finally:
            self.api.free(output.ptr, output.len)
        assert rc == 0 and envelope["ok"], envelope
        return envelope["result"]

    def configure(self, yaml="", *, heuristic=True):
        config = ("heuristic_identity: true\n" if heuristic else "") + yaml
        return self.call("plugin.reconfigure", {"config_yaml": base64.b64encode(config.encode()).decode()})

    def transform(self, body):
        result = self.call("request.intercept_after", {
            "RequestID": "abi-regression", "Model": "alias", "ToFormat": "openai",
            "Metadata": {"selected_auth_index": "mock"},
            "Body": base64.b64encode(json.dumps(body).encode()).decode(),
        })
        return json.loads(base64.b64decode(result["Body"])) if result.get("Body") else body

    def close(self):
        self.api.shutdown()
