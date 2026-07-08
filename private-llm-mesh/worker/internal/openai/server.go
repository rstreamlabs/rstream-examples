// Package openai serves an OpenAI-compatible chat-completions API backed by the
// embedded engine. It depends only on llm.Chatter, so it builds and tests
// without linking llama.cpp.
package openai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/llm"
)

// Server adapts an llm.Chatter to the OpenAI HTTP surface. Concurrency is bounded
// by the engine's context pool; this layer stays stateless.
type Server struct {
	chatter    llm.Chatter
	modelID    string
	maxTokens  int
	temp       float32
	maxGenTime time.Duration
	started    time.Time
	logger     *slog.Logger
}

func NewServer(chatter llm.Chatter, modelID string, maxTokens int, temp float32, maxGenTime time.Duration, logger *slog.Logger) *Server {
	if maxTokens < 0 {
		maxTokens = 0
	}
	if maxGenTime <= 0 {
		maxGenTime = 5 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{chatter: chatter, modelID: modelID, maxTokens: maxTokens, temp: temp, maxGenTime: maxGenTime, started: time.Now(), logger: logger}
}

// logCompletion reports per-request timing so a standalone worker's logs are as
// informative as a managed server's. Prompt processing and generation are split
// (as Ollama does): on CPU a large prompt dominates, and a single blended
// tokens/second would understate real generation speed. `firstToken` is when the
// first content token was produced; when it is zero (a non-streaming request or a
// tool-only turn) the whole request is attributed to prompt processing.
func (s *Server) logCompletion(start, firstToken time.Time, result *llm.Result) {
	total := time.Since(start)
	promptMS := total.Milliseconds()
	var genTPS float64
	if !firstToken.IsZero() {
		promptMS = firstToken.Sub(start).Milliseconds()
		if gen := time.Since(firstToken).Seconds(); gen > 0 {
			genTPS = math.Round(float64(result.Usage.CompletionTokens)/gen*10) / 10
		}
	}
	s.logger.Info("completion",
		"model", s.modelID,
		"prompt_tokens", result.Usage.PromptTokens,
		"completion_tokens", result.Usage.CompletionTokens,
		"prompt_ms", promptMS,
		"gen_tok_per_s", genTPS,
		"total_ms", total.Milliseconds(),
		"tool_calls", len(result.ToolCalls),
	)
}

// Handler returns the routed HTTP handler for the worker tunnel.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	health := map[string]any{
		"status":         "ok",
		"model":          s.modelID,
		"uptime_seconds": int(time.Since(s.started).Seconds()),
	}
	if st, ok := s.chatter.(llm.Stater); ok {
		stats := st.Stats()
		health["active"] = stats.InFlight
		health["parallel"] = stats.Parallel
		health["in_flight"] = stats.InFlight
		health["waiting"] = stats.Waiting
	}
	writeJSON(w, http.StatusOK, health)
}

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data": []map[string]any{{
			"id":       s.modelID,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": "private-llm-mesh",
		}},
	})
}

type chatRequest struct {
	Model       string          `json:"model"`
	Messages    json.RawMessage `json:"messages"`
	Tools       json.RawMessage `json:"tools"`
	Temperature *float32        `json:"temperature"`
	MaxTokens   *int            `json:"max_tokens"`
	Stream      bool            `json:"stream"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}
	maxTokens := s.maxTokens
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}
	temp := s.temp
	if req.Temperature != nil {
		temp = *req.Temperature
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.maxGenTime)
	defer cancel()
	if req.Stream {
		s.streamChat(ctx, w, req, maxTokens, temp)
		return
	}
	start := time.Now()
	result, err := s.chatter.Chat(ctx, string(req.Messages), string(req.Tools), maxTokens, temp, nil)
	if err != nil {
		s.writeChatError(w, err)
		return
	}
	s.logCompletion(start, time.Time{}, result)
	writeJSON(w, http.StatusOK, s.completion(result))
}

// streamChat streams the answer as Server-Sent Events: content tokens flush as
// they generate (tool-call markup never appears in the stream); tool calls are
// delivered as a final structured delta.
func (s *Server) streamChat(ctx context.Context, w http.ResponseWriter, req chatRequest, maxTokens int, temp float32) {
	start := time.Now()
	flusher, ok := w.(http.Flusher)
	if !ok {
		result, err := s.chatter.Chat(ctx, string(req.Messages), string(req.Tools), maxTokens, temp, nil)
		if err != nil {
			s.writeChatError(w, err)
			return
		}
		s.logCompletion(start, time.Time{}, result)
		writeJSON(w, http.StatusOK, s.completion(result))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	id, created := "chatcmpl-"+randID(), time.Now().Unix()
	send := func(delta map[string]any, finish any) {
		b, _ := json.Marshal(map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   s.modelID,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	send(map[string]any{"role": "assistant"}, nil)
	var firstToken time.Time
	onDelta := func(text string) {
		if text != "" {
			if firstToken.IsZero() {
				firstToken = time.Now()
			}
			send(map[string]any{"content": text}, nil)
		}
	}
	result, err := s.chatter.Chat(ctx, string(req.Messages), string(req.Tools), maxTokens, temp, onDelta)
	if err != nil {
		send(map[string]any{}, "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	s.logCompletion(start, firstToken, result)
	if len(result.ToolCalls) > 0 {
		send(map[string]any{"tool_calls": toolCalls(result)}, nil)
		send(map[string]any{}, "tool_calls")
	} else {
		send(map[string]any{}, "stop")
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// writeChatError maps a turn failure to a status: a deadline is 504, a client
// cancel needs no body (the client is gone), anything else is 500.
func (s *Server) writeChatError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "generation timed out")
	case errors.Is(err, context.Canceled):
		return
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

type respFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type respToolCall struct {
	Index    int          `json:"index"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function respFunction `json:"function"`
}

func toolCalls(result *llm.Result) []respToolCall {
	out := make([]respToolCall, 0, len(result.ToolCalls))
	for i, tc := range result.ToolCalls {
		args := tc.Arguments
		if args == "" {
			args = "{}"
		}
		out = append(out, respToolCall{Index: i, ID: "call_" + randID(), Type: "function",
			Function: respFunction{Name: tc.Name, Arguments: args}})
	}
	return out
}

func (s *Server) completion(result *llm.Result) map[string]any {
	message := map[string]any{"role": "assistant"}
	finish := "stop"
	if len(result.ToolCalls) > 0 {
		finish = "tool_calls"
		message["content"] = nil
		message["tool_calls"] = toolCalls(result)
	} else {
		message["content"] = result.Content
	}
	return map[string]any{
		"id":      "chatcmpl-" + randID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   s.modelID,
		"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": finish}},
		"usage": map[string]any{
			"prompt_tokens":     result.Usage.PromptTokens,
			"completion_tokens": result.Usage.CompletionTokens,
			"total_tokens":      result.Usage.PromptTokens + result.Usage.CompletionTokens,
		},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"message": msg, "type": "invalid_request_error"},
	})
}

func randID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
