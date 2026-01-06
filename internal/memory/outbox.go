package memory

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/types"
)

// Outbox manages pending effector actions
type Outbox struct {
	mu         sync.RWMutex
	actions    map[string]*types.Action
	path       string
	lastOffset int64 // Track file position for incremental reads
}

// NewOutbox creates a new outbox
func NewOutbox(path string) *Outbox {
	return &Outbox{
		actions: make(map[string]*types.Action),
		path:    path,
	}
}

// Add queues a new action
func (o *Outbox) Add(action *types.Action) {
	o.mu.Lock()
	defer o.mu.Unlock()

	action.Status = "pending"
	action.Timestamp = time.Now()
	o.actions[action.ID] = action
}

// GetPending returns all pending actions
func (o *Outbox) GetPending() []*types.Action {
	o.mu.RLock()
	defer o.mu.RUnlock()

	result := make([]*types.Action, 0)
	for _, action := range o.actions {
		if action.Status == "pending" {
			result = append(result, action)
		}
	}
	return result
}

// MarkComplete marks an action as complete
func (o *Outbox) MarkComplete(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if action, ok := o.actions[id]; ok {
		action.Status = "complete"
	}
}

// MarkFailed marks an action as failed
func (o *Outbox) MarkFailed(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if action, ok := o.actions[id]; ok {
		action.Status = "failed"
	}
}

// CleanupCompleted removes completed actions older than maxAge
func (o *Outbox) CleanupCompleted(maxAge time.Duration) int {
	o.mu.Lock()
	defer o.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	cleaned := 0
	for id, action := range o.actions {
		if action.Status == "complete" && action.Timestamp.Before(cutoff) {
			delete(o.actions, id)
			cleaned++
		}
	}
	return cleaned
}

// Load reads outbox from JSONL file
func (o *Outbox) Load() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	file, err := os.Open(o.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	o.actions = make(map[string]*types.Action)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var action types.Action
		if err := json.Unmarshal(scanner.Bytes(), &action); err != nil {
			continue // skip malformed lines
		}
		// Later entries override earlier ones (for status updates)
		o.actions[action.ID] = &action
	}

	// Track file position for incremental polling
	o.lastOffset, _ = file.Seek(0, io.SeekEnd)

	return scanner.Err()
}

// Save writes outbox to JSONL file (append mode for new actions)
func (o *Outbox) Save() error {
	o.mu.RLock()
	defer o.mu.RUnlock()

	file, err := os.Create(o.path)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, action := range o.actions {
		data, err := json.Marshal(action)
		if err != nil {
			continue
		}
		file.Write(data)
		file.WriteString("\n")
	}

	return nil
}

// Append adds an action and appends to file (for real-time logging)
func (o *Outbox) Append(action *types.Action) error {
	o.Add(action)

	o.mu.RLock()
	defer o.mu.RUnlock()

	file, err := os.OpenFile(o.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	data, err := json.Marshal(action)
	if err != nil {
		return err
	}
	file.Write(data)
	file.WriteString("\n")

	return nil
}

// Poll checks for new entries in the file (written by external processes like MCP)
// Returns the number of new actions found
func (o *Outbox) Poll() (int, error) {
	file, err := os.Open(o.path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer file.Close()

	// Seek to where we left off
	o.mu.RLock()
	offset := o.lastOffset
	o.mu.RUnlock()

	if offset > 0 {
		_, err = file.Seek(offset, io.SeekStart)
		if err != nil {
			return 0, err
		}
	}

	// Read new entries
	scanner := bufio.NewScanner(file)
	newCount := 0

	o.mu.Lock()
	defer o.mu.Unlock()

	for scanner.Scan() {
		var action types.Action
		if err := json.Unmarshal(scanner.Bytes(), &action); err != nil {
			continue // skip malformed lines
		}
		// Only add if not already present (avoid duplicates)
		if _, exists := o.actions[action.ID]; !exists {
			o.actions[action.ID] = &action
			newCount++
		}
	}

	// Update offset to current position
	newOffset, _ := file.Seek(0, io.SeekCurrent)
	o.lastOffset = newOffset

	return newCount, scanner.Err()
}
