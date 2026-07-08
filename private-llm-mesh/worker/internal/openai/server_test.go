package openai

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/llm"
)

type fakeChatter struct{ result *llm.Result }

func (f fakeChatter) Chat(_ context.Context, _, _ string, _ int, _ float32, onDelta func(string)) (*llm.Result, error) {
	if onDelta != nil && len(f.result.ToolCalls) == 0 {
		onDelta(f.result.Content)
	}
	return f.result, nil
}

func (f fakeChatter) Stats() llm.Stats { return llm.Stats{Parallel: 2, InFlight: 1, Waiting: 0} }

func newServer(result *llm.Result, modelID string) *Server {
	return NewServer(fakeChatter{result}, modelID, 256, 0, time.Minute, nil)
}

func post(t *testing.T, srv *Server, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func firstChoice(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	choices, _ := resp["choices"].([]any)
	if len(choices) == 0 {
		t.Fatalf("no choices in %v", resp)
	}
	return choices[0].(map[string]any)
}

func TestChatCompletionToolCall(t *testing.T) {
	srv := newServer(&llm.Result{
		ToolCalls: []llm.ToolCall{{Name: "run_command", Arguments: `{"command":"df -h"}`}},
	}, "qwen")
	resp := post(t, srv, `{"model":"qwen","messages":[{"role":"user","content":"hi"}],"tools":[]}`)
	choice := firstChoice(t, resp)
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("want finish_reason tool_calls, got %v", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]any)
	calls := msg["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(calls))
	}
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "run_command" {
		t.Fatalf("want run_command, got %v", fn["name"])
	}
}

func TestChatCompletionText(t *testing.T) {
	srv := newServer(&llm.Result{Content: "Blue."}, "qwen")
	resp := post(t, srv, `{"model":"qwen","messages":[{"role":"user","content":"color?"}]}`)
	choice := firstChoice(t, resp)
	if choice["finish_reason"] != "stop" {
		t.Fatalf("want finish_reason stop, got %v", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]any)
	if msg["content"] != "Blue." {
		t.Fatalf("want content Blue., got %v", msg["content"])
	}
	if _, ok := msg["tool_calls"]; ok {
		t.Fatalf("did not expect tool_calls in a text answer")
	}
}

func TestModels(t *testing.T) {
	srv := newServer(&llm.Result{}, "qwen2.5")
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "qwen2.5") {
		t.Fatalf("unexpected /v1/models response: %d %s", w.Code, w.Body.String())
	}
}

func TestChatCompletionStream(t *testing.T) {
	srv := newServer(&llm.Result{Content: "Blue sky."}, "qwen")
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"qwen","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"content":"Blue sky."`) {
		t.Fatalf("missing content delta in stream: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("missing [DONE] terminator: %s", body)
	}
}

func TestHealthReportsLoad(t *testing.T) {
	srv := newServer(&llm.Result{}, "qwen")
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var health map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if health["status"] != "ok" || health["parallel"] != float64(2) || health["in_flight"] != float64(1) {
		t.Fatalf("unexpected /healthz: %v", health)
	}
}
