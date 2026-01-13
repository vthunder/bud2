package buffer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMaxTokens is the token threshold before summarization
	DefaultMaxTokens = 3000
	// DefaultMaxAge is the age threshold before summarization
	DefaultMaxAge = 10 * time.Minute
	// TokensPerChar is a rough estimate for token counting
	TokensPerChar = 0.25
)

// Summarizer is an interface for summarizing conversation content
type Summarizer interface {
	Summarize(content string) (string, error)
}

// ConversationBuffer manages per-scope conversation context
type ConversationBuffer struct {
	mu         sync.RWMutex
	buffers    map[string]*BufferState
	path       string
	maxTokens  int
	maxAge     time.Duration
	summarizer Summarizer
}

// New creates a new conversation buffer manager
func New(statePath string, summarizer Summarizer) *ConversationBuffer {
	return &ConversationBuffer{
		buffers:    make(map[string]*BufferState),
		path:       statePath,
		maxTokens:  DefaultMaxTokens,
		maxAge:     DefaultMaxAge,
		summarizer: summarizer,
	}
}

// SetLimits configures the buffer limits
func (cb *ConversationBuffer) SetLimits(maxTokens int, maxAge time.Duration) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.maxTokens = maxTokens
	cb.maxAge = maxAge
}

// Add adds a new entry to the appropriate buffer
func (cb *ConversationBuffer) Add(entry Entry) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Estimate tokens if not set
	if entry.TokenCount == 0 {
		entry.TokenCount = estimateTokens(entry.Content)
	}

	scope := ScopeChannel(entry.ChannelID)
	key := scope.String()

	// Get or create buffer
	buf, ok := cb.buffers[key]
	if !ok {
		buf = &BufferState{
			Scope:      scope,
			RawEntries: make([]Entry, 0),
			UpdatedAt:  time.Now(),
		}
		cb.buffers[key] = buf
	}

	// Add entry
	buf.RawEntries = append(buf.RawEntries, entry)
	buf.TokenCount += entry.TokenCount
	buf.UpdatedAt = time.Now()

	// Check if we need to compress
	if cb.shouldCompress(buf) {
		if err := cb.compress(buf); err != nil {
			// Log but don't fail - buffer still usable
			fmt.Printf("[buffer] compression failed: %v\n", err)
		}
	}

	return nil
}

// Get retrieves the buffer for a scope
func (cb *ConversationBuffer) Get(scope Scope) *BufferState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.buffers[scope.String()]
}

// GetForChannel retrieves the buffer for a channel
func (cb *ConversationBuffer) GetForChannel(channelID string) *BufferState {
	return cb.Get(ScopeChannel(channelID))
}

// BufferFilter specifies what to exclude when fetching buffer entries
type BufferFilter struct {
	// ExcludeID excludes the entry with this ID (e.g., current focus item)
	ExcludeID string
	// ExcludeBotAuthor excludes entries from this author on incremental syncs
	// (bot's own responses are already in Claude's context)
	ExcludeBotAuthor string
}

// GetEntriesSince returns entries after the given time
// If since is zero, returns all entries (for first-time sync)
// Also returns the summary if this is a first-time sync (since is zero)
func (cb *ConversationBuffer) GetEntriesSince(scope Scope, since time.Time) (entries []Entry, summary string, hasNew bool) {
	return cb.GetEntriesSinceFiltered(scope, since, BufferFilter{})
}

// GetEntriesSinceFiltered returns entries after the given time with filtering
// Filters:
// - ExcludeID: skip entry matching this ID (current focus, avoids duplication)
// - ExcludeBotAuthor: on incremental sync, skip bot's own responses (already in Claude context)
func (cb *ConversationBuffer) GetEntriesSinceFiltered(scope Scope, since time.Time, filter BufferFilter) (entries []Entry, summary string, hasNew bool) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	buf := cb.buffers[scope.String()]
	if buf == nil {
		return nil, "", false
	}

	isFirstSync := since.IsZero()

	// First-time sync: include summary
	if isFirstSync {
		summary = buf.Summary
	}

	// Filter entries
	for _, e := range buf.RawEntries {
		// Skip entries before the sync time (unless first sync)
		if !isFirstSync && !e.Timestamp.After(since) {
			continue
		}

		// Skip the current focus item (it's already in Current Focus section)
		if filter.ExcludeID != "" && e.ID == filter.ExcludeID {
			continue
		}

		// On incremental sync, skip bot's own responses (Claude already knows what it said)
		// On first sync, include them (Claude has amnesia after reset)
		if !isFirstSync && filter.ExcludeBotAuthor != "" && e.Author == filter.ExcludeBotAuthor {
			continue
		}

		entries = append(entries, e)
	}

	hasNew = len(entries) > 0 || summary != ""
	return entries, summary, hasNew
}

// GetContext returns the full context string for a scope (summary + raw)
func (cb *ConversationBuffer) GetContext(scope Scope) string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	buf := cb.buffers[scope.String()]
	if buf == nil {
		return ""
	}

	var parts []string

	// Add summary if present
	if buf.Summary != "" {
		parts = append(parts, fmt.Sprintf("[Earlier context summary]\n%s", buf.Summary))
	}

	// Add raw entries
	if len(buf.RawEntries) > 0 {
		parts = append(parts, "[Recent messages]")
		for _, e := range buf.RawEntries {
			line := formatEntry(e)
			parts = append(parts, line)
		}
	}

	return strings.Join(parts, "\n")
}

// FindReplyContext finds the message that an entry is replying to
func (cb *ConversationBuffer) FindReplyContext(entry Entry) *Entry {
	if entry.ReplyTo == "" {
		return nil
	}

	cb.mu.RLock()
	defer cb.mu.RUnlock()

	scope := ScopeChannel(entry.ChannelID)
	buf := cb.buffers[scope.String()]
	if buf == nil {
		return nil
	}

	// Search in reverse order (most recent first)
	for i := len(buf.RawEntries) - 1; i >= 0; i-- {
		if buf.RawEntries[i].ID == entry.ReplyTo {
			result := buf.RawEntries[i]
			return &result
		}
	}

	return nil
}

// shouldCompress checks if the buffer needs compression
func (cb *ConversationBuffer) shouldCompress(buf *BufferState) bool {
	// Check token limit
	if buf.TokenCount > cb.maxTokens {
		return true
	}

	// Check age limit
	if len(buf.RawEntries) > 0 {
		oldest := buf.RawEntries[0].Timestamp
		if time.Since(oldest) > cb.maxAge {
			return true
		}
	}

	return false
}

// compress summarizes older entries to make room
func (cb *ConversationBuffer) compress(buf *BufferState) error {
	if cb.summarizer == nil {
		// No summarizer - just trim oldest entries
		if len(buf.RawEntries) > 10 {
			// Keep last 10 entries
			removed := buf.RawEntries[:len(buf.RawEntries)-10]
			buf.RawEntries = buf.RawEntries[len(buf.RawEntries)-10:]

			// Recalculate token count
			buf.TokenCount = 0
			for _, e := range buf.RawEntries {
				buf.TokenCount += e.TokenCount
			}

			// Add note about removed content
			if buf.Summary == "" {
				buf.Summary = fmt.Sprintf("[%d earlier messages not shown]", len(removed))
			}
		}
		return nil
	}

	// Find split point (approximately half)
	splitIdx := len(buf.RawEntries) / 2
	if splitIdx == 0 {
		return nil // Nothing to compress
	}

	// Build content to summarize
	toSummarize := buf.RawEntries[:splitIdx]
	var content strings.Builder
	for _, e := range toSummarize {
		content.WriteString(formatEntry(e))
		content.WriteString("\n")
	}

	// Summarize
	summary, err := cb.summarizer.Summarize(content.String())
	if err != nil {
		return fmt.Errorf("summarization failed: %w", err)
	}

	// Update buffer
	if buf.Summary != "" {
		buf.Summary = buf.Summary + "\n\n" + summary
	} else {
		buf.Summary = summary
	}

	buf.RawEntries = buf.RawEntries[splitIdx:]

	// Recalculate token count
	buf.TokenCount = 0
	for _, e := range buf.RawEntries {
		buf.TokenCount += e.TokenCount
	}

	return nil
}

// formatEntry formats an entry for display
func formatEntry(e Entry) string {
	timestamp := e.Timestamp.Format("15:04")
	replyMarker := ""
	if e.ReplyTo != "" {
		replyMarker = " (reply)"
	}
	actMarker := ""
	if e.DialogueAct != "" && e.DialogueAct != ActUnknown {
		actMarker = fmt.Sprintf(" [%s]", e.DialogueAct)
	}
	return fmt.Sprintf("[%s] %s%s%s: %s", timestamp, e.Author, replyMarker, actMarker, e.Content)
}

// estimateTokens provides a rough token count estimate
func estimateTokens(content string) int {
	// Rough estimate: ~4 chars per token for English
	return int(float64(len(content)) * TokensPerChar)
}

// Load loads buffer state from disk
func (cb *ConversationBuffer) Load() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	filePath := filepath.Join(cb.path, "buffers.json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No saved state
		}
		return fmt.Errorf("failed to read buffers: %w", err)
	}

	var states map[string]*BufferState
	if err := json.Unmarshal(data, &states); err != nil {
		return fmt.Errorf("failed to unmarshal buffers: %w", err)
	}

	cb.buffers = states
	return nil
}

// Save persists buffer state to disk
func (cb *ConversationBuffer) Save() error {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	data, err := json.MarshalIndent(cb.buffers, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal buffers: %w", err)
	}

	filePath := filepath.Join(cb.path, "buffers.json")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write buffers: %w", err)
	}

	return nil
}

// Clear removes all buffers
func (cb *ConversationBuffer) Clear() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.buffers = make(map[string]*BufferState)
}

// ClearScope removes a specific scope's buffer
func (cb *ConversationBuffer) ClearScope(scope Scope) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	delete(cb.buffers, scope.String())
}

// Stats returns buffer statistics
func (cb *ConversationBuffer) Stats() map[string]int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	stats := make(map[string]int)
	stats["buffer_count"] = len(cb.buffers)

	totalEntries := 0
	totalTokens := 0
	for _, buf := range cb.buffers {
		totalEntries += len(buf.RawEntries)
		totalTokens += buf.TokenCount
	}
	stats["total_entries"] = totalEntries
	stats["total_tokens"] = totalTokens

	return stats
}
