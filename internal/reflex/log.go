package reflex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogEntry represents a single reflex interaction
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Query     string    `json:"query"`    // User's input
	Response  string    `json:"response"` // Reflex's response
	Intent    string    `json:"intent"`   // Classified intent (e.g., gtd_show_today)
	Reflex    string    `json:"reflex"`   // Name of reflex that handled it

	// Enhanced fields for v2 observability
	Source       string        `json:"source,omitempty"`        // Input source (discord, calendar, etc.)
	Type         string        `json:"type,omitempty"`          // Input type (message, notification, etc.)
	ChannelID    string        `json:"channel_id,omitempty"`    // Discord channel if applicable
	MatchedBy    string        `json:"matched_by,omitempty"`    // What matched (pattern, intent, classifier)
	Actions      []string      `json:"actions,omitempty"`       // Actions executed in pipeline
	Success      bool          `json:"success"`                 // Whether reflex succeeded
	Escalated    bool          `json:"escalated,omitempty"`     // Whether escalated to executive
	Duration     time.Duration `json:"duration,omitempty"`      // How long execution took
	ErrorMessage string        `json:"error,omitempty"`         // Error if failed
}

// Log maintains a short-term ordered log of reflex interactions
type Log struct {
	mu       sync.RWMutex
	entries  []LogEntry
	maxSize  int
	lastSent int    // Index of last entry sent to executive (for deduplication)
	path     string // Path for persistent storage
}

// NewLog creates a new reflex log with the given capacity
func NewLog(maxSize int) *Log {
	return &Log{
		entries:  make([]LogEntry, 0, maxSize),
		maxSize:  maxSize,
		lastSent: -1,
	}
}

// NewLogWithPath creates a new reflex log with persistent storage
func NewLogWithPath(maxSize int, statePath string) *Log {
	return &Log{
		entries:  make([]LogEntry, 0, maxSize),
		maxSize:  maxSize,
		lastSent: -1,
		path:     statePath,
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

// AddEntry records a full reflex log entry with enhanced metadata
func (l *Log) AddEntry(entry LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	l.entries = append(l.entries, entry)

	// Trim if over capacity
	if len(l.entries) > l.maxSize {
		excess := len(l.entries) - l.maxSize
		l.entries = l.entries[excess:]
		l.lastSent -= excess
		if l.lastSent < -1 {
			l.lastSent = -1
		}
	}

	// Persist to disk if path is set
	if l.path != "" {
		l.appendToDisk(entry)
	}
}

// appendToDisk appends a single entry to the JSONL log file
func (l *Log) appendToDisk(entry LogEntry) {
	filePath := filepath.Join(l.path, "reflex_log.jsonl")

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return // Silent fail - logging shouldn't break the system
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	f.Write(data)
	f.WriteString("\n")
}

// Load loads recent entries from disk
func (l *Log) Load() error {
	if l.path == "" {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	filePath := filepath.Join(l.path, "reflex_log.jsonl")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read reflex log: %w", err)
	}

	// Parse JSONL (only load recent entries up to maxSize)
	var entries []LogEntry
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var entry LogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // Skip malformed lines
		}
		entries = append(entries, entry)
	}

	// Keep only last maxSize entries
	if len(entries) > l.maxSize {
		entries = entries[len(entries)-l.maxSize:]
	}

	l.entries = entries
	l.lastSent = len(entries) - 1 // Mark all loaded as "sent"
	return nil
}

// splitLines splits byte data into lines (handles both \n and \r\n)
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			end := i
			if end > start && data[end-1] == '\r' {
				end--
			}
			lines = append(lines, data[start:end])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// GetAll returns all entries (for debugging/inspection)
func (l *Log) GetAll() []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]LogEntry, len(l.entries))
	copy(result, l.entries)
	return result
}

// Stats returns log statistics
func (l *Log) Stats() map[string]int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	stats := map[string]int{
		"total_entries": len(l.entries),
		"unsent":        len(l.entries) - l.lastSent - 1,
		"successes":     0,
		"failures":      0,
		"escalations":   0,
	}

	for _, e := range l.entries {
		if e.Success {
			stats["successes"]++
		} else {
			stats["failures"]++
		}
		if e.Escalated {
			stats["escalations"]++
		}
	}

	return stats
}
