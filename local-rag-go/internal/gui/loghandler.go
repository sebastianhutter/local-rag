// Package gui implements the Fyne-based GUI for local-rag.
package gui

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

const ringBufferSize = 5000

// ringState holds shared mutable state for the ring buffer. It is shared
// across WithAttrs/WithGroup clones so that all loggers write to the same buffer.
type ringState struct {
	mu      sync.RWMutex
	buf     []string
	head    int // next write position
	count   int // total lines stored (capped at ringBufferSize)
	subs    map[int]chan string
	nextSub int
}

// RingBufferHandler is an slog.Handler that stores formatted log lines in a
// fixed-size ring buffer and broadcasts new lines to subscribers.
type RingBufferHandler struct {
	inner slog.Handler
	state *ringState
}

// NewRingBufferHandler wraps an inner handler and adds ring-buffer storage.
func NewRingBufferHandler(inner slog.Handler) *RingBufferHandler {
	return &RingBufferHandler{
		inner: inner,
		state: &ringState{
			buf:  make([]string, ringBufferSize),
			subs: make(map[int]chan string),
		},
	}
}

// Enabled delegates to the inner handler.
func (h *RingBufferHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle formats the record and stores it in the ring buffer.
func (h *RingBufferHandler) Handle(ctx context.Context, r slog.Record) error {
	// Format: "15:04:05 LEVEL message key=value ..."
	line := fmt.Sprintf("%s %s %s",
		r.Time.Format("15:04:05"),
		r.Level.String(),
		r.Message,
	)
	r.Attrs(func(a slog.Attr) bool {
		line += fmt.Sprintf(" %s=%v", a.Key, a.Value)
		return true
	})

	s := h.state
	s.mu.Lock()
	s.buf[s.head] = line
	s.head = (s.head + 1) % ringBufferSize
	if s.count < ringBufferSize {
		s.count++
	}
	// Copy subscribers to avoid holding lock during send.
	subs := make(map[int]chan string, len(s.subs))
	for k, v := range s.subs {
		subs[k] = v
	}
	s.mu.Unlock()

	// Non-blocking send to all subscribers.
	for _, ch := range subs {
		select {
		case ch <- line:
		default:
			// Subscriber is slow; drop the line.
		}
	}

	// Also delegate to the inner handler.
	return h.inner.Handle(ctx, r)
}

// WithAttrs delegates to the inner handler; the clone shares the ring buffer.
func (h *RingBufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &RingBufferHandler{
		inner: h.inner.WithAttrs(attrs),
		state: h.state,
	}
}

// WithGroup delegates to the inner handler; the clone shares the ring buffer.
func (h *RingBufferHandler) WithGroup(name string) slog.Handler {
	return &RingBufferHandler{
		inner: h.inner.WithGroup(name),
		state: h.state,
	}
}

// GetHistory returns all buffered lines in chronological order.
func (h *RingBufferHandler) GetHistory() []string {
	s := h.state
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.count == 0 {
		return nil
	}

	lines := make([]string, 0, s.count)
	if s.count < ringBufferSize {
		// Buffer hasn't wrapped yet.
		lines = append(lines, s.buf[:s.count]...)
	} else {
		// Buffer has wrapped: read from head to end, then start to head.
		lines = append(lines, s.buf[s.head:]...)
		lines = append(lines, s.buf[:s.head]...)
	}
	return lines
}

// Subscribe returns a channel that receives new log lines.
// The channel has a buffer of 100 to reduce dropped lines.
func (h *RingBufferHandler) Subscribe() (int, chan string) {
	s := h.state
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextSub
	s.nextSub++
	ch := make(chan string, 100)
	s.subs[id] = ch
	return id, ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (h *RingBufferHandler) Unsubscribe(id int) {
	s := h.state
	s.mu.Lock()
	defer s.mu.Unlock()

	if ch, ok := s.subs[id]; ok {
		close(ch)
		delete(s.subs, id)
	}
}

// Clear empties the ring buffer.
func (h *RingBufferHandler) Clear() {
	s := h.state
	s.mu.Lock()
	defer s.mu.Unlock()
	s.head = 0
	s.count = 0
}
