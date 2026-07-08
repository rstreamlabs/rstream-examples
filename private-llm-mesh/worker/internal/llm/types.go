// Package llm holds the engine-agnostic chat types shared between the inference
// engine (cgo) and the OpenAI-compatible HTTP layer (pure Go). Keeping them here
// lets the HTTP layer be built and tested without linking llama.cpp.
package llm

import "context"

// ToolCall is one structured tool invocation parsed from the model output.
type ToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Usage reports token counts for one turn.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Result is the outcome of one chat turn: Content xor ToolCalls.
type Result struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls"`
	Usage     Usage      `json:"usage"`
	Error     string     `json:"error,omitempty"`
}

// Chatter runs one stateless chat turn. It blocks until a decoding slot is free
// or ctx is cancelled. messagesJSON and toolsJSON are OpenAI-format JSON
// (toolsJSON may be empty); temp <= 0 is greedy. onDelta (if non-nil) receives
// content deltas; cancelling ctx aborts generation.
type Chatter interface {
	Chat(ctx context.Context, messagesJSON, toolsJSON string, maxTokens int, temp float32, onDelta func(string)) (*Result, error)
}

// Stats reports decoding-pool utilization.
type Stats struct {
	Parallel int `json:"parallel"`
	InFlight int `json:"in_flight"`
	Waiting  int `json:"waiting"`
}

// Stater exposes pool utilization for /healthz. A Chatter may also implement it.
type Stater interface {
	Stats() Stats
}
