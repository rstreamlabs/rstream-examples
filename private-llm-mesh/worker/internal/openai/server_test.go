package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/llm"
)

type fakeChatter struct {
	result *llm.Result
	err    error
}

func (f fakeChatter) Chat(_ context.Context, _, _ string, _ int, _ float32, onDelta func(string)) (*llm.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	if onDelta != nil && len(f.result.ToolCalls) == 0 {
		onDelta(f.result.Content)
	}
	return f.result, nil
}

func (f fakeChatter) Stats() llm.Stats { return llm.Stats{Parallel: 2, InFlight: 1, Waiting: 0} }

func newServer(result *llm.Result, modelID string) *Server {
	return NewServer(fakeChatter{result: result}, modelID, 256, 0, time.Minute, nil)
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

func TestChatCompletionRejectsUnavailableModel(t *testing.T) {
	srv := newServer(&llm.Result{Content: "Blue."}, "qwen")
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"mistral","messages":[{"role":"user","content":"color?"}]}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 404 || !strings.Contains(w.Body.String(), `model \"mistral\" is not available`) {
		t.Fatalf("unexpected unavailable-model response: %d %s", w.Code, w.Body.String())
	}
}

func TestChatCompletionRejectsTrailingJSON(t *testing.T) {
	srv := newServer(&llm.Result{Content: "Blue."}, "qwen")
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"qwen","messages":[]} {}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "one JSON object") {
		t.Fatalf("trailing JSON response = %d %s", w.Code, w.Body.String())
	}
}

func TestChatCompletionValidatesMessagesAndTools(t *testing.T) {
	srv := newServer(&llm.Result{Content: "Blue."}, "qwen")
	for name, body := range map[string]string{
		"messages object": `{"model":"qwen","messages":{}}`,
		"empty messages":  `{"model":"qwen","messages":[]}`,
		"message scalar":  `{"model":"qwen","messages":["hi"]}`,
		"missing role":    `{"model":"qwen","messages":[{"content":"hi"}]}`,
		"tools object":    `{"model":"qwen","messages":[{"role":"user","content":"hi"}],"tools":{}}`,
		"tool scalar":     `{"model":"qwen","messages":[{"role":"user","content":"hi"}],"tools":["bad"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("validation response = %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestChatCompletionBoundsGenerationParameters(t *testing.T) {
	srv := newServer(&llm.Result{Content: "Blue."}, "qwen")
	for name, body := range map[string]string{
		"zero tokens":          `{"model":"qwen","messages":[{"role":"user"}],"max_tokens":0}`,
		"excess tokens":        `{"model":"qwen","messages":[{"role":"user"}],"max_tokens":1048577}`,
		"negative temperature": `{"model":"qwen","messages":[{"role":"user"}],"temperature":-0.1}`,
		"excess temperature":   `{"model":"qwen","messages":[{"role":"user"}],"temperature":2.1}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("bounds response = %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestChatCompletionBoundsRequestBody(t *testing.T) {
	srv := newServer(&llm.Result{Content: "Blue."}, "qwen")
	body := `{"model":"qwen","messages":[{"role":"user","content":"` + strings.Repeat("x", maxRequestBodyBytes) + `"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "too large") {
		t.Fatalf("oversized response = %d %s", w.Code, w.Body.String())
	}
}

func TestChatCompletionReportsCapacityWithoutStartingWork(t *testing.T) {
	srv := NewServer(fakeChatter{err: llm.ErrAtCapacity}, "qwen", 256, 0, time.Minute, nil)
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"qwen","messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "worker is at capacity") {
		t.Fatalf("capacity response = %d %s", w.Code, w.Body.String())
	}
}

func TestChatCompletionStreamDoesNotMaskWorkerFailureAsSuccess(t *testing.T) {
	srv := NewServer(fakeChatter{err: errors.New("inference failed")}, "qwen", 256, 0, time.Minute, nil)
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"qwen","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, `"error":{"code":500,"message":"inference failed"`) {
		t.Fatalf("stream error was masked: %s", body)
	}
	if strings.Contains(body, `"finish_reason":"stop"`) {
		t.Fatalf("failed stream reported a successful stop: %s", body)
	}
}

func TestChatCompletionStreamDoesNotReportClientCancellationAsWorkerFailure(t *testing.T) {
	srv := NewServer(fakeChatter{err: context.Canceled}, "qwen", 256, 0, time.Minute, nil)
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"qwen","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	body := w.Body.String()
	if strings.Contains(body, `"error"`) || strings.Contains(body, `"finish_reason":"stop"`) {
		t.Fatalf("canceled stream reported a terminal worker event: %s", body)
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
