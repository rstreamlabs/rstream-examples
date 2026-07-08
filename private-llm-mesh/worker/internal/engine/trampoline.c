#include <stdint.h>

// goContentCallback and goStopCallback are exported by engine.go (cgo //export).
// These trampolines give the function pointers external linkage so they can be
// passed to the shim. Both run on the calling goroutine's thread (never on a
// ggml worker thread), so calling back into Go is safe.
extern void goContentCallback(uintptr_t user, char *delta);
extern int goStopCallback(uintptr_t user);

void pllm_content_trampoline(uintptr_t user, const char *delta) {
	goContentCallback(user, (char *)delta);
}

int pllm_stop_trampoline(uintptr_t user) {
	return goStopCallback(user);
}
