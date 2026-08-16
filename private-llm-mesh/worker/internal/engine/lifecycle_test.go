package engine

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/llm"
)

func newLifecycleTestEngine(capacity int) *Engine {
	shutdown, cancel := context.WithCancelCause(context.Background())
	return &Engine{admission: make(chan struct{}, capacity), shutdown: shutdown, cancel: cancel}
}

func TestEngineRejectsBoundedAdmissionOverflow(t *testing.T) {
	engine := newLifecycleTestEngine(1)
	firstDone := make(chan error, 1)
	go func() {
		_, err := engine.Chat(t.Context(), "[]", "", 1, 0, nil)
		firstDone <- err
	}()
	deadline := time.Now().Add(time.Second)
	for len(engine.admission) != 1 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if len(engine.admission) != 1 {
		t.Fatal("first Chat() did not enter the bounded admission queue")
	}
	_, err := engine.Chat(t.Context(), "[]", "", 1, 0, nil)
	if !errors.Is(err, llm.ErrAtCapacity) {
		t.Fatalf("Chat() error = %v, want ErrAtCapacity", err)
	}
	if len(engine.admission) != 1 {
		t.Fatalf("admission depth = %d, want 1", len(engine.admission))
	}
	engine.Close()
	select {
	case err := <-firstDone:
		if !errors.Is(err, llm.ErrClosed) {
			t.Fatalf("queued Chat() error = %v, want ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued Chat() did not stop when the engine closed")
	}
}

func TestEngineCloseWaitsForRegisteredChatsAndRejectsNewOnes(t *testing.T) {
	engine := newLifecycleTestEngine(1)
	if err := engine.beginChat(); err != nil {
		t.Fatalf("beginChat() error = %v", err)
	}
	closed := make(chan struct{})
	go func() {
		engine.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close() returned while a chat was registered")
	case <-time.After(20 * time.Millisecond):
	}
	engine.endChat()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close() did not finish after the chat completed")
	}
	if err := engine.beginChat(); !errors.Is(err, llm.ErrClosed) {
		t.Fatalf("beginChat() after Close error = %v, want ErrClosed", err)
	}
	if !errors.Is(context.Cause(engine.shutdown), llm.ErrClosed) {
		t.Fatalf("shutdown cause = %v, want ErrClosed", context.Cause(engine.shutdown))
	}
	engine.Close()
}
