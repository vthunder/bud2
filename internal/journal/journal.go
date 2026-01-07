package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EntryType identifies what kind of journal entry this is
type EntryType string

const (
	EntryDecision    EntryType = "decision"    // Executive made a decision
	EntryImpulse     EntryType = "impulse"     // Internal motivation triggered
	EntryReflex      EntryType = "reflex"      // Reflex fired (skipped executive)
	EntryExploration EntryType = "exploration" // Explored an idea
	EntryAction      EntryType = "action"      // Action taken
	EntryObservation EntryType = "observation" // Noticed something
)

// Entry represents a single journal entry
type Entry struct {
	Timestamp time.Time         `json:"ts"`
	Type      EntryType         `json:"type"`
	Summary   string            `json:"summary,omitempty"`   // Brief description
	Context   string            `json:"context,omitempty"`   // What prompted this
	Reasoning string            `json:"reasoning,omitempty"` // Why this decision
	Outcome   string            `json:"outcome,omitempty"`   // What resulted
	Data      map[string]any    `json:"data,omitempty"`      // Flexible extra data
}

// Journal writes observability entries to state/journal.jsonl
type Journal struct {
	path string
	mu   sync.Mutex
	file *os.File
}

// New creates a journal writer
func New(statePath string) *Journal {
	return &Journal{
		path: filepath.Join(statePath, "journal.jsonl"),
	}
}

// Log writes an entry to the journal
func (j *Journal) Log(entry Entry) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	// Set timestamp if not provided
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	// Open file for append
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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

// LogDecision logs a decision made by the executive
func (j *Journal) LogDecision(context, reasoning, action string) error {
	return j.Log(Entry{
		Type:      EntryDecision,
		Context:   context,
		Reasoning: reasoning,
		Summary:   action,
	})
}

// LogImpulse logs an internal motivation that triggered
func (j *Journal) LogImpulse(source, description, decision string) error {
	return j.Log(Entry{
		Type:    EntryImpulse,
		Summary: description,
		Context: source,
		Outcome: decision,
	})
}

// LogReflex logs a reflex that fired without executive
func (j *Journal) LogReflex(pattern, action string, data map[string]any) error {
	return j.Log(Entry{
		Type:    EntryReflex,
		Context: pattern,
		Summary: action,
		Data:    data,
	})
}

// LogExploration logs time spent exploring an idea
func (j *Journal) LogExploration(idea string, durationSec float64, outcome string) error {
	return j.Log(Entry{
		Type:    EntryExploration,
		Summary: idea,
		Outcome: outcome,
		Data: map[string]any{
			"duration_sec": durationSec,
		},
	})
}

// LogAction logs an action taken
func (j *Journal) LogAction(action, context string, data map[string]any) error {
	return j.Log(Entry{
		Type:    EntryAction,
		Summary: action,
		Context: context,
		Data:    data,
	})
}

// Recent returns the last n entries from the journal
func (j *Journal) Recent(n int) ([]Entry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := os.ReadFile(j.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Parse all lines
	var entries []Entry
	lines := splitLines(data)
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed entries
		}
		entries = append(entries, entry)
	}

	// Return last n
	if n >= len(entries) {
		return entries, nil
	}
	return entries[len(entries)-n:], nil
}

// Today returns entries from today
func (j *Journal) Today() ([]Entry, error) {
	entries, err := j.Recent(1000) // reasonable limit
	if err != nil {
		return nil, err
	}

	// Filter to today
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var todayEntries []Entry
	for _, e := range entries {
		if e.Timestamp.After(today) || e.Timestamp.Equal(today) {
			todayEntries = append(todayEntries, e)
		}
	}
	return todayEntries, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
