package focus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Queue manages the pending items queue with persistence
type Queue struct {
	mu       sync.RWMutex
	items    []*PendingItem
	path     string
	maxSize  int
	notifyCh chan struct{} // Signal when new items are added
}

// NewQueue creates a new pending items queue
func NewQueue(statePath string, maxSize int) *Queue {
	return &Queue{
		items:    make([]*PendingItem, 0),
		path:     statePath,
		maxSize:  maxSize,
		notifyCh: make(chan struct{}, 1), // Buffered to prevent blocking
	}
}

// NotifyChannel returns the channel that signals when new items are added
func (q *Queue) NotifyChannel() <-chan struct{} {
	return q.notifyCh
}

// Add adds an item to the queue
func (q *Queue) Add(item *PendingItem) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if item.Timestamp.IsZero() {
		item.Timestamp = time.Now()
	}

	q.items = append(q.items, item)

	// Trim if over capacity (remove oldest, lowest priority)
	if len(q.items) > q.maxSize {
		q.trim()
	}

	// Signal that a new item is available (non-blocking)
	select {
	case q.notifyCh <- struct{}{}:
		// Notification sent
	default:
		// Channel already has a pending signal, no need to add another
	}

	return nil
}

// Get retrieves and removes an item by ID
func (q *Queue) Get(id string) *PendingItem {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, item := range q.items {
		if item.ID == id {
			q.items = append(q.items[:i], q.items[i+1:]...)
			return item
		}
	}
	return nil
}

// Peek returns the highest priority item without removing it
func (q *Queue) Peek() *PendingItem {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if len(q.items) == 0 {
		return nil
	}

	// Find highest priority (lowest number)
	best := q.items[0]
	for _, item := range q.items[1:] {
		if item.Priority < best.Priority {
			best = item
		} else if item.Priority == best.Priority && item.Salience > best.Salience {
			best = item
		}
	}

	return best
}

// PopHighest removes and returns the highest priority item
func (q *Queue) PopHighest() *PendingItem {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		return nil
	}

	// Find highest priority
	bestIdx := 0
	for i, item := range q.items[1:] {
		if item.Priority < q.items[bestIdx].Priority {
			bestIdx = i + 1
		} else if item.Priority == q.items[bestIdx].Priority && item.Salience > q.items[bestIdx].Salience {
			bestIdx = i + 1
		}
	}

	item := q.items[bestIdx]
	q.items = append(q.items[:bestIdx], q.items[bestIdx+1:]...)
	return item
}

// All returns all items (copy)
func (q *Queue) All() []*PendingItem {
	q.mu.RLock()
	defer q.mu.RUnlock()

	result := make([]*PendingItem, len(q.items))
	copy(result, q.items)
	return result
}

// FilterByPriority returns items at or above the given priority
func (q *Queue) FilterByPriority(maxPriority Priority) []*PendingItem {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var result []*PendingItem
	for _, item := range q.items {
		if item.Priority <= maxPriority {
			result = append(result, item)
		}
	}
	return result
}

// FilterByType returns items of the given type
func (q *Queue) FilterByType(itemType string) []*PendingItem {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var result []*PendingItem
	for _, item := range q.items {
		if item.Type == itemType {
			result = append(result, item)
		}
	}
	return result
}

// Count returns the number of items in the queue
func (q *Queue) Count() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.items)
}

// Clear removes all items from the queue
func (q *Queue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = make([]*PendingItem, 0)
}

// trim removes excess items (oldest, lowest priority first)
func (q *Queue) trim() {
	// Sort by priority (desc) then age (desc)
	// Remove from end (oldest, lowest priority)
	excess := len(q.items) - q.maxSize
	if excess <= 0 {
		return
	}

	// Simple approach: remove oldest items that aren't high priority
	var keep []*PendingItem
	removed := 0

	for _, item := range q.items {
		if removed < excess && item.Priority > P1UserInput {
			removed++
			continue
		}
		keep = append(keep, item)
	}

	q.items = keep
}

// ExpireOld removes items older than the given duration
func (q *Queue) ExpireOld(maxAge time.Duration) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	var keep []*PendingItem
	removed := 0

	for _, item := range q.items {
		if item.Timestamp.Before(cutoff) {
			removed++
			continue
		}
		keep = append(keep, item)
	}

	q.items = keep
	return removed
}

// Load loads queue state from disk
func (q *Queue) Load() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	filePath := filepath.Join(q.path, "pending_queue.json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read queue: %w", err)
	}

	var items []*PendingItem
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("failed to unmarshal queue: %w", err)
	}

	q.items = items
	return nil
}

// Save persists queue state to disk
func (q *Queue) Save() error {
	q.mu.RLock()
	defer q.mu.RUnlock()

	data, err := json.MarshalIndent(q.items, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal queue: %w", err)
	}

	filePath := filepath.Join(q.path, "pending_queue.json")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write queue: %w", err)
	}

	return nil
}
