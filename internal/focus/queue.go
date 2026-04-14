package focus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// PopAllMaxPriority removes and returns all items with priority <= maxPriority,
// sorted by priority ascending (most urgent first), then salience descending.
// Returns nil if no qualifying items exist.
func (q *Queue) PopAllMaxPriority(maxPriority Priority) []*PendingItem {
	q.mu.Lock()
	defer q.mu.Unlock()

	var result []*PendingItem
	var remaining []*PendingItem

	for _, item := range q.items {
		if item.Priority <= maxPriority {
			result = append(result, item)
		} else {
			remaining = append(remaining, item)
		}
	}
	q.items = remaining

	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority < result[j].Priority
		}
		return result[i].Salience > result[j].Salience
	})

	return result
}

// PopHighestMinPriority removes and returns the highest priority item
// with priority >= minPriority (i.e., items no more urgent than minPriority).
// Returns nil if no qualifying item exists.
func (q *Queue) PopHighestMinPriority(minPriority Priority) *PendingItem {
	q.mu.Lock()
	defer q.mu.Unlock()

	bestIdx := -1
	for i, item := range q.items {
		if item.Priority < minPriority {
			continue
		}
		if bestIdx == -1 || item.Priority < q.items[bestIdx].Priority {
			bestIdx = i
		} else if item.Priority == q.items[bestIdx].Priority && item.Salience > q.items[bestIdx].Salience {
			bestIdx = i
		}
	}

	if bestIdx == -1 {
		return nil
	}

	item := q.items[bestIdx]
	q.items = append(q.items[:bestIdx], q.items[bestIdx+1:]...)
	return item
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
