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
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-examples/private-llm-mesh/worker/internal/llm"
)

const (
	maxRequestBodyBytes = 4 << 20
	maxRequestedTokens  = 1 << 20
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
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}
	if err := validateMessages(req.Messages); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateTools(req.Tools); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if req.Model != s.modelID {
		writeError(w, http.StatusNotFound, fmt.Sprintf("model %q is not available", req.Model))
		return
	}
	maxTokens := s.maxTokens
	if req.MaxTokens != nil {
		if *req.MaxTokens < 1 || *req.MaxTokens > maxRequestedTokens {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("max_tokens must be between 1 and %d", maxRequestedTokens))
			return
		}
		maxTokens = *req.MaxTokens
	}
	temp := s.temp
	if req.Temperature != nil {
		if *req.Temperature < 0 || *req.Temperature > 2 {
			writeError(w, http.StatusBadRequest, "temperature must be between 0 and 2")
			return
		}
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

func validateMessages(raw json.RawMessage) error {
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil || len(messages) == 0 {
		return errors.New("messages must be a non-empty JSON array")
	}
	for index, rawMessage := range messages {
		var message map[string]json.RawMessage
		if err := json.Unmarshal(rawMessage, &message); err != nil || message == nil {
			return fmt.Errorf("messages[%d] must be a JSON object", index)
		}
		var role string
		if err := json.Unmarshal(message["role"], &role); err != nil || strings.TrimSpace(role) == "" {
			return fmt.Errorf("messages[%d].role must be a non-empty string", index)
		}
	}
	return nil
}

func validateTools(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(raw, &tools); err != nil {
		return errors.New("tools must be a JSON array")
	}
	for index, rawTool := range tools {
		var tool map[string]json.RawMessage
		if err := json.Unmarshal(rawTool, &tool); err != nil || tool == nil {
			return fmt.Errorf("tools[%d] must be a JSON object", index)
		}
	}
	return nil
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
		message, status := chatError(err)
		if status == 0 {
			return
		}
		b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": message, "type": "worker_error", "code": status}})
		fmt.Fprintf(w, "data: %s\n\n", b)
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
	message, status := chatError(err)
	if status == 0 {
		return
	}
	writeError(w, status, message)
}

func chatError(err error) (string, int) {
	switch {
	case errors.Is(err, llm.ErrAtCapacity):
		return "worker is at capacity", http.StatusTooManyRequests
	case errors.Is(err, llm.ErrClosed):
		return "worker is shutting down", http.StatusServiceUnavailable
	case errors.Is(err, context.DeadlineExceeded):
		return "generation timed out", http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		return "request canceled", 0
	default:
		return err.Error(), http.StatusInternalServerError
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
