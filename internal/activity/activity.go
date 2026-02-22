package activity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Type identifies what kind of activity this is
type Type string

const (
	TypeInput        Type = "input"         // Message/event received
	TypeReflex       Type = "reflex"        // Reflex handled something
	TypeReflexPass   Type = "reflex_pass"   // Reflex passed to executive
	TypeExecWake     Type = "executive_wake" // Executive started processing
	TypeExecDone     Type = "executive_done" // Executive finished
	TypeAction       Type = "action"        // Action taken (message sent, etc.)
	TypeDecision     Type = "decision"      // Explicit decision logged
	TypeError        Type = "error"         // Something went wrong
)

// Entry represents a single activity log entry
type Entry struct {
	Timestamp time.Time      `json:"ts"`
	Type      Type           `json:"type"`
	Summary   string         `json:"summary"`
	Source    string         `json:"source,omitempty"`    // What triggered this
	Channel   string         `json:"channel,omitempty"`   // Discord channel if applicable
	ThreadID  string         `json:"thread_id,omitempty"` // Executive thread if applicable
	Intent    string         `json:"intent,omitempty"`    // Reflex intent if applicable
	Reasoning string         `json:"reasoning,omitempty"` // Why this happened
	Data      map[string]any `json:"data,omitempty"`      // Structured details
}

// Log is the activity logger
type Log struct {
	path string
	mu   sync.Mutex
}

// New creates an activity logger
func New(statePath string) *Log {
	return &Log{
		path: filepath.Join(statePath, "system", "activity.jsonl"),
	}
}

// Log appends an entry to the activity log
func (l *Log) Log(entry Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Set timestamp if not provided
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	// Open file for append
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write JSON line
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// Helper methods for common event types

// LogInput logs an incoming message/event
func (l *Log) LogInput(summary, source, channel string) error {
	return l.Log(Entry{
		Type:    TypeInput,
		Summary: summary,
		Source:  source,
		Channel: channel,
	})
}

// LogReflex logs a reflex handling an event
func (l *Log) LogReflex(summary, intent, query, response string) error {
	return l.Log(Entry{
		Type:    TypeReflex,
		Summary: summary,
		Intent:  intent,
		Data: map[string]any{
			"query":    query,
			"response": response,
		},
	})
}

// LogReflexPass logs a reflex passing to executive
func (l *Log) LogReflexPass(summary, intent, query string) error {
	return l.Log(Entry{
		Type:    TypeReflexPass,
		Summary: summary,
		Intent:  intent,
		Data: map[string]any{
			"query": query,
		},
	})
}

// LogExecWake logs executive starting to process
func (l *Log) LogExecWake(summary, threadID, context string) error {
	return l.Log(Entry{
		Type:     TypeExecWake,
		Summary:  summary,
		ThreadID: threadID,
		Data: map[string]any{
			"context": context,
		},
	})
}

// LogExecDone logs executive completing processing
func (l *Log) LogExecDone(summary, threadID string, durationSec float64, completion string, extraData map[string]any) error {
	data := map[string]any{
		"duration_sec": durationSec,
		"completion":   completion,
	}
	for k, v := range extraData {
		data[k] = v
	}
	return l.Log(Entry{
		Type:     TypeExecDone,
		Summary:  summary,
		ThreadID: threadID,
		Data:     data,
	})
}

// LogAction logs an action taken
func (l *Log) LogAction(summary, source, channel, content string) error {
	return l.Log(Entry{
		Type:    TypeAction,
		Summary: summary,
		Source:  source,
		Channel: channel,
		Data: map[string]any{
			"content": content,
		},
	})
}

// LogDecision logs an explicit decision with reasoning
func (l *Log) LogDecision(summary, reasoning, context, outcome string) error {
	return l.Log(Entry{
		Type:      TypeDecision,
		Summary:   summary,
		Reasoning: reasoning,
		Data: map[string]any{
			"context": context,
			"outcome": outcome,
		},
	})
}

// LogError logs an error
func (l *Log) LogError(summary string, err error, data map[string]any) error {
	if data == nil {
		data = make(map[string]any)
	}
	data["error"] = err.Error()
	return l.Log(Entry{
		Type:    TypeError,
		Summary: summary,
		Data:    data,
	})
}

// Query methods

// Recent returns the last n entries
func (l *Log) Recent(n int) ([]Entry, error) {
	entries, err := l.readAll()
	if err != nil {
		return nil, err
	}

	if n >= len(entries) {
		return entries, nil
	}
	return entries[len(entries)-n:], nil
}

// Today returns entries from today
func (l *Log) Today() ([]Entry, error) {
	entries, err := l.readAll()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var result []Entry
	for _, e := range entries {
		if !e.Timestamp.Before(today) {
			result = append(result, e)
		}
	}
	return result, nil
}

// Search searches entries by text (in summary and data)
func (l *Log) Search(query string, limit int) ([]Entry, error) {
	entries, err := l.readAll()
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(query)
	var result []Entry

	// Search from most recent
	for i := len(entries) - 1; i >= 0 && len(result) < limit; i-- {
		e := entries[i]
		// Check summary
		if strings.Contains(strings.ToLower(e.Summary), query) {
			result = append(result, e)
			continue
		}
		// Check data as JSON string
		if e.Data != nil {
			dataJSON, _ := json.Marshal(e.Data)
			if strings.Contains(strings.ToLower(string(dataJSON)), query) {
				result = append(result, e)
				continue
			}
		}
		// Check reasoning
		if strings.Contains(strings.ToLower(e.Reasoning), query) {
			result = append(result, e)
		}
	}

	return result, nil
}

// ByType returns entries of a specific type
func (l *Log) ByType(t Type, limit int) ([]Entry, error) {
	entries, err := l.readAll()
	if err != nil {
		return nil, err
	}

	var result []Entry
	// From most recent
	for i := len(entries) - 1; i >= 0 && len(result) < limit; i-- {
		if entries[i].Type == t {
			result = append(result, entries[i])
		}
	}
	return result, nil
}

// Range returns entries in a time range
func (l *Log) Range(start, end time.Time) ([]Entry, error) {
	entries, err := l.readAll()
	if err != nil {
		return nil, err
	}

	var result []Entry
	for _, e := range entries {
		if !e.Timestamp.Before(start) && !e.Timestamp.After(end) {
			result = append(result, e)
		}
	}
	return result, nil
}

// LastUserInputTime returns the timestamp of the most recent user-initiated input
// (TypeInput entries that are not from impulse:system). Returns zero time if none found.
func (l *Log) LastUserInputTime() time.Time {
	entries, err := l.readAll()
	if err != nil {
		return time.Time{}
	}
	// Walk backwards for efficiency
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Type == TypeInput && e.Source != "impulse:system" {
			return e.Timestamp
		}
	}
	return time.Time{}
}

// readAll reads all entries from the log file
func (l *Log) readAll() ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []Entry
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // skip malformed entries
		}
		entries = append(entries, entry)
	}

	return entries, nil
}
