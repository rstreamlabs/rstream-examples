package engine

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
)

// These tests exercise the real embedded model. They run only when MODEL points
// at a GGUF file, e.g.:
//   MODEL=/path/to/qwen2.5-1.5b-instruct-q4_k_m.gguf make test

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
