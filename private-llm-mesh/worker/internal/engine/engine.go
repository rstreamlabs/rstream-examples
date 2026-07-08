// Package engine embeds llama.cpp in-process (via a thin cgo shim around
// common_chat) and exposes robust text-or-tool chat completions to Go.
//
// A loaded model shares its weights across a pool of decoding contexts, so N
// requests run concurrently. The cgo include/link flags come from the build
// (CGO_CXXFLAGS / CGO_LDFLAGS, set by the Makefile), so this file carries no
// machine-specific paths — only the shim's stable C surface.
package engine

/*
#include <stdint.h>
#include <stdlib.h>
#include "shim.h"

// Defined in trampoline.c; forward llama.cpp's callbacks to the exported Go
// functions (goContentCallback, goStopCallback).
void pllm_content_trampoline(uintptr_t user, const char *delta);
int pllm_stop_trampoline(uintptr_t user);
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/cgo"
	"sync/atomic"
	"unsafe"

	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/llm"
)

// Engine is a model plus a pool of decoding contexts. It is safe for concurrent
// use: each in-flight request holds one context, and up to pool-size run at once.
type Engine struct {
	model    *C.pllm_model
	pool     chan *C.pllm_context
	contexts []*C.pllm_context
	parallel int
	inFlight atomic.Int64
	waiting  atomic.Int64
}

// Load reads a GGUF model and creates `parallel` decoding contexts of nCtx.
func Load(modelPath string, nCtx, parallel int) (*Engine, error) {
	if parallel < 1 {
		parallel = 1
	}
	cpath := C.CString(modelPath)
	defer C.free(unsafe.Pointer(cpath))
	m := C.pllm_model_load(cpath)
	if m == nil {
		return nil, fmt.Errorf("engine: failed to load model %q", modelPath)
	}
	e := &Engine{model: m, pool: make(chan *C.pllm_context, parallel), parallel: parallel}
	for i := 0; i < parallel; i++ {
		c := C.pllm_context_new(m, C.int(nCtx))
		if c == nil {
			e.Close()
			return nil, fmt.Errorf("engine: failed to create context %d/%d", i+1, parallel)
		}
		e.contexts = append(e.contexts, c)
		e.pool <- c
	}
	return e, nil
}

//export goContentCallback
func goContentCallback(user C.uintptr_t, delta *C.char) {
	if fn, ok := cgo.Handle(user).Value().(func(string)); ok && fn != nil {
		fn(C.GoString(delta))
	}
}

//export goStopCallback
func goStopCallback(user C.uintptr_t) C.int {
	if fn, ok := cgo.Handle(user).Value().(func() bool); ok && fn != nil && fn() {
		return 1
	}
	return 0
}

// Chat runs one stateless turn on a pooled context. It blocks until a context is
// free or ctx is cancelled. messagesJSON and toolsJSON are OpenAI-format JSON;
// temp <= 0 is greedy. onDelta (if non-nil) receives content deltas. Cancelling
// ctx (client gone or deadline) aborts generation. Parsing is llama.cpp's.
func (e *Engine) Chat(ctx context.Context, messagesJSON, toolsJSON string, maxTokens int, temp float32, onDelta func(string)) (*llm.Result, error) {
	e.waiting.Add(1)
	var c *C.pllm_context
	select {
	case c = <-e.pool:
		e.waiting.Add(-1)
	case <-ctx.Done():
		e.waiting.Add(-1)
		return nil, ctx.Err()
	}
	e.inFlight.Add(1)
	defer func() {
		e.inFlight.Add(-1)
		e.pool <- c
	}()
	cm := C.CString(messagesJSON)
	ct := C.CString(toolsJSON)
	defer C.free(unsafe.Pointer(cm))
	defer C.free(unsafe.Pointer(ct))
	var cb C.pllm_content_cb
	var cbUser C.uintptr_t
	if onDelta != nil {
		h := cgo.NewHandle(onDelta)
		defer h.Delete()
		cb = C.pllm_content_cb(C.pllm_content_trampoline)
		cbUser = C.uintptr_t(h)
	}
	hs := cgo.NewHandle(func() bool { return ctx.Err() != nil })
	defer hs.Delete()
	stop := C.pllm_stop_cb(C.pllm_stop_trampoline)
	stopUser := C.uintptr_t(hs)
	cres := C.pllm_chat(c, cm, ct, C.int(maxTokens), C.float(temp), cb, cbUser, stop, stopUser)
	if cres == nil {
		return nil, fmt.Errorf("engine: chat returned nil")
	}
	defer C.pllm_free(cres)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var r llm.Result
	if err := json.Unmarshal([]byte(C.GoString(cres)), &r); err != nil {
		return nil, fmt.Errorf("engine: decode shim result: %w", err)
	}
	if r.Error != "" {
		return nil, fmt.Errorf("engine: %s", r.Error)
	}
	return &r, nil
}

// Stats reports pool utilization for /healthz.
func (e *Engine) Stats() llm.Stats {
	return llm.Stats{Parallel: e.parallel, InFlight: int(e.inFlight.Load()), Waiting: int(e.waiting.Load())}
}

// Close frees every context and the model. Call after serving has stopped.
func (e *Engine) Close() {
	for _, c := range e.contexts {
		C.pllm_context_free(c)
	}
	e.contexts = nil
	if e.model != nil {
		C.pllm_model_free(e.model)
		e.model = nil
	}
}
