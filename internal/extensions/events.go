package extensions

import (
	"strings"
	"sync"
)

// Event is an in-process pub/sub message emitted before or after a capability invocation.
type Event struct {
	// Topic is the canonical event name: "<ext>:<cap>:<phase>" (e.g. "bud-core:fetch:after").
	Topic   string
	Payload map[string]any
}

// EventHandler is a function invoked when a matching event is published.
type EventHandler func(e Event)

// subscription tracks a single handler registration.
type subscription struct {
	id      uint64
	pattern string // normalised pattern (may contain "*" segments)
	handler EventHandler
}

// EventBus is an in-process publish/subscribe bus for capability lifecycle events.
//
// Topic format:   <ext>:<cap>:<phase>    e.g. "bud-core:fetch:after"
// Wildcard rules: any segment may be replaced with "*", e.g. "bud-core:*:after" or "*:*:after".
//
// Indexing strategy:
//   - Subscriptions are indexed by a normalised key derived by replacing non-wildcard
//     segments with "_" — this gives O(1) bucket lookup for each incoming topic.
//   - On Publish, we compute all 8 possible bucket keys for a 3-segment topic and
//     iterate only the matching buckets.
type EventBus struct {
	mu      sync.RWMutex
	nextID  uint64
	// buckets maps a normalised pattern key → list of subscriptions.
	// Patterns are stored as-is; the bucket key is built by setting each segment to "*"
	// when it is a wildcard or to the literal value otherwise.
	buckets map[string][]*subscription
}

// NewEventBus creates a ready-to-use EventBus.
func NewEventBus() *EventBus {
	return &EventBus{buckets: make(map[string][]*subscription)}
}

// Subscribe registers handler for events matching pattern.
// Pattern must be a 3-segment colon-separated string; any segment may be "*".
// Returns an unsubscribe function that removes this handler.
func (b *EventBus) Subscribe(pattern string, handler EventHandler) func() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	id := b.nextID

	key := bucketKey(pattern)
	sub := &subscription{id: id, pattern: pattern, handler: handler}
	b.buckets[key] = append(b.buckets[key], sub)

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		subs := b.buckets[key]
		for i, s := range subs {
			if s.id == id {
				b.buckets[key] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
	}
}

// Publish delivers event to all matching subscribers.
// Matching is determined by segment-wise equality or wildcard "*" in the subscription pattern.
// Publish is synchronous — all handlers run on the caller's goroutine.
// Callers that want fire-and-forget semantics should wrap the call in a goroutine.
func (b *EventBus) Publish(event Event) {
	parts := strings.SplitN(event.Topic, ":", 3)
	if len(parts) != 3 {
		return // malformed topic; silently drop
	}

	// Build the 8 bucket keys that could match this topic.
	keys := make([]string, 0, 8)
	for _, s0 := range []string{parts[0], "*"} {
		for _, s1 := range []string{parts[1], "*"} {
			for _, s2 := range []string{parts[2], "*"} {
				keys = append(keys, s0+":"+s1+":"+s2)
			}
		}
	}

	b.mu.RLock()
	// Collect handlers to call (copy slice refs so we can release lock before calling).
	var toCall []EventHandler
	seen := make(map[uint64]bool)
	for _, k := range keys {
		for _, sub := range b.buckets[k] {
			if !seen[sub.id] {
				seen[sub.id] = true
				toCall = append(toCall, sub.handler)
			}
		}
	}
	b.mu.RUnlock()

	for _, h := range toCall {
		h(event)
	}
}

// bucketKey normalises a subscription pattern into a lookup key.
// Non-wildcard segments are kept as-is; wildcard "*" segments are kept as "*".
// This means the key IS the pattern — stored and looked up directly.
func bucketKey(pattern string) string {
	return pattern // the pattern itself is the bucket key
}
