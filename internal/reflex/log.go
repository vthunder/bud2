package reflex

import (
	"fmt"
	"sync"
	"time"
)

// LogEntry represents a single reflex interaction
type LogEntry struct {
	Timestamp time.Time
	Query     string // User's input
	Response  string // Reflex's response
	Intent    string // Classified intent (e.g., gtd_show_today)
	Reflex    string // Name of reflex that handled it
}

// Log maintains a short-term ordered log of reflex interactions
type Log struct {
	mu       sync.RWMutex
	entries  []LogEntry
	maxSize  int
	lastSent int // Index of last entry sent to executive (for deduplication)
}

// NewLog creates a new reflex log with the given capacity
func NewLog(maxSize int) *Log {
	return &Log{
		entries:  make([]LogEntry, 0, maxSize),
		maxSize:  maxSize,
		lastSent: -1,
	}
}

// Add records a reflex interaction
func (l *Log) Add(query, response, intent, reflexName string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry := LogEntry{
		Timestamp: time.Now(),
		Query:     query,
		Response:  response,
		Intent:    intent,
		Reflex:    reflexName,
	}

	l.entries = append(l.entries, entry)

	// Trim if over capacity
	if len(l.entries) > l.maxSize {
		// Remove oldest entries
		excess := len(l.entries) - l.maxSize
		l.entries = l.entries[excess:]
		// Adjust lastSent index
		l.lastSent -= excess
		if l.lastSent < -1 {
			l.lastSent = -1
		}
	}
}

// GetUnsent returns entries that haven't been sent to the executive yet
// and marks them as sent
func (l *Log) GetUnsent() []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.lastSent >= len(l.entries)-1 {
		return nil
	}

	start := l.lastSent + 1
	unsent := make([]LogEntry, len(l.entries)-start)
	copy(unsent, l.entries[start:])
	l.lastSent = len(l.entries) - 1

	return unsent
}

// GetRecent returns the N most recent entries (doesn't affect sent tracking)
func (l *Log) GetRecent(n int) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if n > len(l.entries) {
		n = len(l.entries)
	}

	result := make([]LogEntry, n)
	copy(result, l.entries[len(l.entries)-n:])
	return result
}

// ResetSentTracking resets the sent tracking (e.g., for new session)
func (l *Log) ResetSentTracking() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lastSent = -1
}

// Format returns a human-readable string of entries for context injection
func FormatEntries(entries []LogEntry) string {
	if len(entries) == 0 {
		return ""
	}

	var result string
	for _, e := range entries {
		result += fmt.Sprintf("- User: %s\n  Bud (reflex): %s\n", e.Query, e.Response)
	}
	return result
}

// IsMutation returns true if the intent represents a state change
func IsMutation(intent string) bool {
	switch intent {
	case "gtd_add_inbox", "gtd_complete":
		return true
	default:
		return false
	}
}
