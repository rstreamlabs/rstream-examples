package logs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type Hub struct {
	mu      sync.RWMutex
	entries []Entry
	limit   int
	subs    map[chan Entry]struct{}
}

type Logger struct {
	logger *slog.Logger
}

func NewHub(limit int) *Hub {
	if limit <= 0 {
		limit = 128
	}
	return &Hub{
		limit: limit,
		subs:  make(map[chan Entry]struct{}),
	}
}

func NewLogger(hub *Hub, verbose bool) *Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	handler := &handler{
		hub:   hub,
		level: level,
		std:   os.Stdout,
	}
	return &Logger{
		logger: slog.New(handler),
	}
}

func (h *Hub) Recent() []Entry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Entry, len(h.entries))
	copy(out, h.entries)
	return out
}

func (h *Hub) Subscribe() (<-chan Entry, func()) {
	ch := make(chan Entry, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

func (h *Hub) Publish(level, message string) {
	entry := Entry{
		Time:    time.Now().UTC(),
		Level:   level,
		Message: message,
	}
	h.mu.Lock()
	h.entries = append(h.entries, entry)
	if len(h.entries) > h.limit {
		h.entries = append([]Entry(nil), h.entries[len(h.entries)-h.limit:]...)
	}
	for ch := range h.subs {
		select {
		case ch <- entry:
		default:
		}
	}
	h.mu.Unlock()
}

func (l *Logger) Debug(format string, args ...any) {
	l.logger.Debug(formatMessage(format, args...))
}

func (l *Logger) Info(format string, args ...any) {
	l.logger.Info(formatMessage(format, args...))
}

func (l *Logger) Warn(format string, args ...any) {
	l.logger.Warn(formatMessage(format, args...))
}

func (l *Logger) Error(format string, args ...any) {
	l.logger.Error(formatMessage(format, args...))
}

func formatMessage(format string, args ...any) string {
	message := format
	if len(args) > 0 {
		message = fmt.Sprintf(format, args...)
	}
	return message
}

type handler struct {
	hub   *Hub
	level slog.Level
	std   *os.File
	mu    sync.Mutex
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *handler) Handle(_ context.Context, record slog.Record) error {
	level := strings.ToLower(record.Level.String())
	line := fmt.Sprintf("%s %-5s %s\n", time.Now().UTC().Format(time.RFC3339), level, record.Message)
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.std.WriteString(line); err != nil {
		return err
	}
	if h.hub != nil {
		h.hub.Publish(level, record.Message)
	}
	return nil
}

func (h *handler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

func (h *handler) WithGroup(_ string) slog.Handler {
	return h
}
