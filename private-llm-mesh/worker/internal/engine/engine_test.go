package engine

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/llm"
)

// These tests exercise the real embedded model. They run only when MODEL points
// at a GGUF file, e.g.:
//   MODEL=/path/to/Qwen_Qwen3-4B-Instruct-2507-Q4_K_M.gguf make test

const toolsJSON = `[{"type":"function","function":{"name":"run_command",` +
	`"description":"Run a shell command on a remote machine and return its output.",` +
	`"parameters":{"type":"object","properties":{"command":{"type":"string"}},` +
	`"required":["command"]}}}]`

const sysMsg = `{"role":"system","content":"You can run commands on the user's machines with run_command."}`

func loadEngine(t *testing.T, parallel int) *Engine {
	t.Helper()
	model := os.Getenv("MODEL")
	if model == "" {
		t.Skip("set MODEL=/path/to/gguf to run engine tests")
	}
	e, err := Load(model, 4096, parallel)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return e
}

// A clearly actionable request must produce a structured tool call.
func TestChatToolCall(t *testing.T) {
	e := loadEngine(t, 1)
	defer e.Close()
	r, err := e.Chat(context.Background(),
		`[`+sysMsg+`,{"role":"user","content":"Use run_command to run exactly 'df -h' on server 1 now."}]`,
		toolsJSON, 256, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ToolCalls) == 0 || r.ToolCalls[0].Name != "run_command" {
		t.Fatalf("want run_command tool call, got content=%q tool_calls=%v", r.Content, r.ToolCalls)
	}
}

// A plain question must be answered in text, and the streamed deltas must
// reconstruct the full content.
func TestChatPlainText(t *testing.T) {
	e := loadEngine(t, 1)
	defer e.Close()
	var streamed string
	r, err := e.Chat(context.Background(),
		`[`+sysMsg+`,{"role":"user","content":"In one word, what color is the sky?"}]`,
		toolsJSON, 256, 0, func(d string) { streamed += d })
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ToolCalls) != 0 || r.Content == "" {
		t.Fatalf("want plain text, got content=%q tool_calls=%v", r.Content, r.ToolCalls)
	}
	if streamed != r.Content {
		t.Fatalf("streamed deltas %q != final content %q", streamed, r.Content)
	}
}

// Concurrent requests on a pool must all succeed (exercises multi-context decode
// and the queue when requests exceed the pool size).
func TestChatConcurrent(t *testing.T) {
	e := loadEngine(t, 2)
	defer e.Close()
	const n = 4
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = e.Chat(context.Background(),
				`[{"role":"user","content":"In one word, what color is the sky?"}]`, "", 64, 0, nil)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
}

// A cancelled context must abort the turn rather than generate to completion.
func TestChatCancel(t *testing.T) {
	e := loadEngine(t, 1)
	defer e.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := e.Chat(ctx, `[{"role":"user","content":"Tell me a very long story."}]`, "", 512, 0, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestChatCancelDuringGeneration(t *testing.T) {
	e := loadEngine(t, 1)
	defer e.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	started := time.Now()
	_, err := e.Chat(ctx, `[{"role":"user","content":"Write a detailed thousand-word story."}]`, "", 2048, 0, func(string) {
		once.Do(cancel)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if time.Since(started) > 10*time.Second {
		t.Fatalf("cancellation took %s, want at most 10s", time.Since(started))
	}
}

func TestCloseCancelsActiveGenerationBeforeFreeingModel(t *testing.T) {
	e := loadEngine(t, 1)
	started := make(chan struct{})
	result := make(chan error, 1)
	var once sync.Once
	go func() {
		_, err := e.Chat(context.Background(), `[{"role":"user","content":"Write a detailed thousand-word story."}]`, "", 2048, 0, func(string) {
			once.Do(func() { close(started) })
		})
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("generation did not emit its first token")
	}
	closed := make(chan struct{})
	go func() {
		e.Close()
		close(closed)
	}()
	select {
	case err := <-result:
		if !errors.Is(err, llm.ErrClosed) {
			t.Fatalf("Chat() error = %v, want ErrClosed", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("active generation did not stop during Close")
	}
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close() did not release the model after generation stopped")
	}
}

func TestChatAdmissionRemainsBoundedUnderSaturation(t *testing.T) {
	model := os.Getenv("MODEL")
	if model == "" {
		t.Skip("set MODEL=/path/to/gguf to run engine tests")
	}
	e, err := LoadWithOptions(model, 4096, Options{Parallel: 1, MaxQueue: 1})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer e.Close()
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	firstStarted := make(chan struct{})
	firstDone := make(chan error, 1)
	var once sync.Once
	go func() {
		_, err := e.Chat(firstCtx, `[{"role":"user","content":"Write a detailed thousand-word story."}]`, "", 2048, 0, func(string) {
			once.Do(func() { close(firstStarted) })
		})
		firstDone <- err
	}()
	select {
	case <-firstStarted:
	case <-time.After(30 * time.Second):
		t.Fatal("first generation did not start")
	}
	secondDone := make(chan error, 1)
	go func() {
		_, err := e.Chat(secondCtx, `[{"role":"user","content":"Reply with ok."}]`, "", 16, 0, nil)
		secondDone <- err
	}()
	deadline := time.Now().Add(time.Second)
	for e.Stats().Waiting != 1 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if stats := e.Stats(); stats.InFlight != 1 || stats.Waiting != 1 {
		t.Fatalf("saturated stats = %+v, want one active and one waiting", stats)
	}
	_, err = e.Chat(context.Background(), `[]`, "", 1, 0, nil)
	if !errors.Is(err, llm.ErrAtCapacity) {
		t.Fatalf("overflow Chat() error = %v, want ErrAtCapacity", err)
	}
	cancelFirst()
	cancelSecond()
	for name, done := range map[string]<-chan error{"first": firstDone, "second": secondDone} {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s Chat() error = %v, want context.Canceled", name, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s Chat() did not stop after cancellation", name)
		}
	}
}
