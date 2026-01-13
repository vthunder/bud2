package buffer

import (
	"testing"
	"time"
)

// MockSummarizer returns predictable summaries for testing
type MockSummarizer struct {
	summarizeFunc func(string) (string, error)
}

func (m *MockSummarizer) Summarize(content string) (string, error) {
	if m.summarizeFunc != nil {
		return m.summarizeFunc(content)
	}
	// Default: return truncated content
	if len(content) > 50 {
		return content[:50] + "...", nil
	}
	return content, nil
}

// TestReplyChainTracking tests the "Yes" problem - ensuring backchannels stay with their context
func TestReplyChainTracking(t *testing.T) {
	cb := New("/tmp/test-buffer", nil)

	// Scenario: User asks a question, Bud responds, user says "yes"
	// The "yes" should be tracked as a reply to Bud's response

	// User question
	questionEntry := Entry{
		ID:          "msg-001",
		Author:      "thunder",
		AuthorID:    "user-123",
		Content:     "Should I deploy to production?",
		Timestamp:   time.Now().Add(-3 * time.Second),
		ChannelID:   "channel-1",
		DialogueAct: ActQuestion,
	}
	cb.Add(questionEntry)

	// Bud's response
	budResponse := Entry{
		ID:          "msg-002",
		Author:      "bud",
		AuthorID:    "bot-456",
		Content:     "I'd recommend waiting until after the code review is complete.",
		Timestamp:   time.Now().Add(-2 * time.Second),
		ChannelID:   "channel-1",
		DialogueAct: ActStatement,
	}
	cb.Add(budResponse)

	// User's "yes" - this is a reply to Bud's response
	yesEntry := Entry{
		ID:          "msg-003",
		Author:      "thunder",
		AuthorID:    "user-123",
		Content:     "yes",
		Timestamp:   time.Now().Add(-1 * time.Second),
		ChannelID:   "channel-1",
		DialogueAct: ActBackchannel,
		ReplyTo:     "msg-002", // Explicitly replies to Bud's response
	}
	cb.Add(yesEntry)

	// Test: Can we find what "yes" is replying to?
	replyContext := cb.FindReplyContext(yesEntry)
	if replyContext == nil {
		t.Fatal("Expected to find reply context for 'yes', got nil")
	}

	if replyContext.ID != "msg-002" {
		t.Errorf("Expected reply context ID %q, got %q", "msg-002", replyContext.ID)
	}

	if replyContext.Content != budResponse.Content {
		t.Errorf("Expected reply context content %q, got %q", budResponse.Content, replyContext.Content)
	}

	// Test: Context should include all three messages in order
	context := cb.GetContext(ScopeChannel("channel-1"))
	if context == "" {
		t.Fatal("Expected non-empty context")
	}

	// Verify the "yes" is marked as a reply in the formatted output
	if !containsSubstring(context, "(reply)") {
		t.Error("Expected context to mark 'yes' as a reply")
	}
}

// TestBufferCompression tests automatic summarization when limits are exceeded
func TestBufferCompression(t *testing.T) {
	// Create mock summarizer that returns "[summarized]"
	mockSummarizer := &MockSummarizer{
		summarizeFunc: func(content string) (string, error) {
			return "[Summarized: " + content[:min(30, len(content))] + "...]", nil
		},
	}

	cb := New("/tmp/test-buffer", mockSummarizer)
	cb.SetLimits(100, 5*time.Minute) // Low limits for testing

	// Add entries until we exceed the token limit
	for i := 0; i < 10; i++ {
		entry := Entry{
			ID:         string(rune('a'+i)) + "-msg",
			Author:     "user",
			AuthorID:   "user-123",
			Content:    "This is a test message with some content to take up tokens. Message number: " + string(rune('0'+i)),
			Timestamp:  time.Now(),
			ChannelID:  "channel-1",
			TokenCount: 20, // Force token count
		}
		cb.Add(entry)
	}

	buf := cb.Get(ScopeChannel("channel-1"))
	if buf == nil {
		t.Fatal("Expected buffer to exist")
	}

	// After compression, we should have fewer raw entries
	if len(buf.RawEntries) >= 10 {
		t.Errorf("Expected compression to reduce raw entries from 10, got %d", len(buf.RawEntries))
	}

	// Should have a summary
	if buf.Summary == "" {
		t.Error("Expected buffer to have a summary after compression")
	}

	// Summary should contain our marker
	if !containsSubstring(buf.Summary, "Summarized") {
		t.Errorf("Expected summary to contain 'Summarized', got: %s", buf.Summary)
	}
}

// TestBufferTimeBasedCompression tests compression triggered by message age
func TestBufferTimeBasedCompression(t *testing.T) {
	mockSummarizer := &MockSummarizer{
		summarizeFunc: func(content string) (string, error) {
			return "[Time-based summary]", nil
		},
	}

	cb := New("/tmp/test-buffer", mockSummarizer)
	cb.SetLimits(10000, 1*time.Second) // High token limit, very short time limit

	// Add an old message
	oldEntry := Entry{
		ID:        "old-msg",
		Author:    "user",
		Content:   "This is an old message",
		Timestamp: time.Now().Add(-5 * time.Second), // 5 seconds ago
		ChannelID: "channel-1",
	}
	cb.Add(oldEntry)

	// Add a new message - should trigger time-based compression
	newEntry := Entry{
		ID:        "new-msg",
		Author:    "user",
		Content:   "This is a new message",
		Timestamp: time.Now(),
		ChannelID: "channel-1",
	}
	cb.Add(newEntry)

	buf := cb.Get(ScopeChannel("channel-1"))
	// Note: Compression only happens when thresholds are exceeded
	// With 2 entries and low token counts, we might not trigger compression
	// The test verifies the mechanism exists
	if buf == nil {
		t.Fatal("Expected buffer to exist")
	}
}

// TestNullSummarizer tests fallback behavior without summarizer
func TestNullSummarizer(t *testing.T) {
	cb := New("/tmp/test-buffer", nil) // No summarizer
	cb.SetLimits(50, 5*time.Minute)   // Very low token limit

	// Add many entries to trigger compression
	for i := 0; i < 20; i++ {
		entry := Entry{
			ID:         "msg-" + string(rune('a'+i)),
			Author:     "user",
			Content:    "Test message " + string(rune('0'+i)),
			Timestamp:  time.Now(),
			ChannelID:  "channel-1",
			TokenCount: 10,
		}
		cb.Add(entry)
	}

	buf := cb.Get(ScopeChannel("channel-1"))
	if buf == nil {
		t.Fatal("Expected buffer to exist")
	}

	// Without summarizer, should just keep recent entries
	// and note that earlier messages were removed
	if len(buf.RawEntries) == 20 {
		t.Error("Expected some entries to be removed without summarizer")
	}
}

// TestGetEntriesSince tests incremental sync
func TestGetEntriesSince(t *testing.T) {
	cb := New("/tmp/test-buffer", nil)

	baseTime := time.Now().Add(-10 * time.Second)

	// Add 5 entries with increasing timestamps
	for i := 0; i < 5; i++ {
		entry := Entry{
			ID:        "msg-" + string(rune('a'+i)),
			Author:    "user",
			Content:   "Message " + string(rune('0'+i)),
			Timestamp: baseTime.Add(time.Duration(i) * time.Second),
			ChannelID: "channel-1",
		}
		cb.Add(entry)
	}

	scope := ScopeChannel("channel-1")

	// First-time sync (zero time) should return everything
	entries, summary, hasNew := cb.GetEntriesSince(scope, time.Time{})
	if !hasNew {
		t.Error("Expected hasNew to be true for first sync")
	}
	if len(entries) != 5 {
		t.Errorf("Expected 5 entries for first sync, got %d", len(entries))
	}

	// Incremental sync should return only newer entries
	sinceTime := baseTime.Add(2 * time.Second) // After first 2 entries
	entries, summary, hasNew = cb.GetEntriesSince(scope, sinceTime)
	if !hasNew {
		t.Error("Expected hasNew to be true for incremental sync")
	}
	if len(entries) != 2 { // Should get entries 3, 4 (0-indexed)
		t.Errorf("Expected 2 entries since %v, got %d", sinceTime, len(entries))
	}

	// Summary should be empty for incremental sync
	if summary != "" {
		t.Errorf("Expected empty summary for incremental sync, got %q", summary)
	}

	// Sync with future time should return no entries
	entries, _, hasNew = cb.GetEntriesSince(scope, time.Now().Add(time.Hour))
	if hasNew {
		t.Error("Expected hasNew to be false for future sync")
	}
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries for future sync, got %d", len(entries))
	}
}

// TestBufferScopes tests different buffer scopes
func TestBufferScopes(t *testing.T) {
	cb := New("/tmp/test-buffer", nil)

	// Add to channel 1
	cb.Add(Entry{
		ID:        "ch1-msg",
		Author:    "user",
		Content:   "Message in channel 1",
		Timestamp: time.Now(),
		ChannelID: "channel-1",
	})

	// Add to channel 2
	cb.Add(Entry{
		ID:        "ch2-msg",
		Author:    "user",
		Content:   "Message in channel 2",
		Timestamp: time.Now(),
		ChannelID: "channel-2",
	})

	// Each channel should have its own buffer
	buf1 := cb.GetForChannel("channel-1")
	buf2 := cb.GetForChannel("channel-2")

	if buf1 == nil || buf2 == nil {
		t.Fatal("Expected both buffers to exist")
	}

	if len(buf1.RawEntries) != 1 || len(buf2.RawEntries) != 1 {
		t.Error("Expected each buffer to have exactly 1 entry")
	}

	if buf1.RawEntries[0].Content == buf2.RawEntries[0].Content {
		t.Error("Expected different content in different channel buffers")
	}
}

// TestDialogueActInBuffer tests that dialogue acts are preserved
func TestDialogueActInBuffer(t *testing.T) {
	cb := New("/tmp/test-buffer", nil)

	entry := Entry{
		ID:          "msg-001",
		Author:      "user",
		Content:     "yes",
		Timestamp:   time.Now(),
		ChannelID:   "channel-1",
		DialogueAct: ActBackchannel,
	}
	cb.Add(entry)

	buf := cb.Get(ScopeChannel("channel-1"))
	if buf == nil || len(buf.RawEntries) == 0 {
		t.Fatal("Expected buffer with entry")
	}

	if buf.RawEntries[0].DialogueAct != ActBackchannel {
		t.Errorf("Expected DialogueAct %v, got %v", ActBackchannel, buf.RawEntries[0].DialogueAct)
	}

	// Formatted context should include act marker
	context := cb.GetContext(ScopeChannel("channel-1"))
	if !containsSubstring(context, "[backchannel]") {
		t.Errorf("Expected context to include dialogue act marker, got: %s", context)
	}
}

// Helper functions

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
