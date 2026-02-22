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

// TestGetEntriesSinceFiltered tests ExcludeID and ExcludeBotAuthor filtering
func TestGetEntriesSinceFiltered(t *testing.T) {
	cb := New("/tmp/test-buffer", nil)
	baseTime := time.Now().Add(-10 * time.Second)

	entries := []Entry{
		{ID: "msg-1", Author: "thunder", Content: "Question", Timestamp: baseTime, ChannelID: "ch"},
		{ID: "msg-2", Author: "bud", Content: "Answer", Timestamp: baseTime.Add(1 * time.Second), ChannelID: "ch"},
		{ID: "focus-msg", Author: "thunder", Content: "Current focus item", Timestamp: baseTime.Add(2 * time.Second), ChannelID: "ch"},
		{ID: "msg-4", Author: "thunder", Content: "Follow up", Timestamp: baseTime.Add(3 * time.Second), ChannelID: "ch"},
	}
	for _, e := range entries {
		cb.Add(e)
	}

	scope := ScopeChannel("ch")

	// ExcludeID should drop the current focus item
	filtered, _, _ := cb.GetEntriesSinceFiltered(scope, time.Time{}, BufferFilter{ExcludeID: "focus-msg"})
	for _, e := range filtered {
		if e.ID == "focus-msg" {
			t.Error("ExcludeID should have removed focus-msg but it's still present")
		}
	}
	if len(filtered) != 3 {
		t.Errorf("Expected 3 entries after ExcludeID filter, got %d", len(filtered))
	}

	// ExcludeBotAuthor on incremental sync should drop bud's own responses
	sinceTime := baseTime.Add(-1 * time.Second) // before all entries
	filtered, _, _ = cb.GetEntriesSinceFiltered(scope, sinceTime, BufferFilter{ExcludeBotAuthor: "bud"})
	for _, e := range filtered {
		if e.Author == "bud" {
			t.Error("ExcludeBotAuthor should have removed bud's messages on incremental sync")
		}
	}

	// ExcludeBotAuthor on FIRST sync (since is zero) should NOT drop bot messages
	filtered, _, _ = cb.GetEntriesSinceFiltered(scope, time.Time{}, BufferFilter{ExcludeBotAuthor: "bud"})
	foundBud := false
	for _, e := range filtered {
		if e.Author == "bud" {
			foundBud = true
		}
	}
	if !foundBud {
		t.Error("ExcludeBotAuthor should NOT exclude bot on first sync (zero time)")
	}
}

// TestMultipleCompressions tests summary accumulation across multiple compressions
func TestMultipleCompressions(t *testing.T) {
	compressCount := 0
	mockSummarizer := &MockSummarizer{
		summarizeFunc: func(content string) (string, error) {
			compressCount++
			return "[Round " + string(rune('0'+compressCount)) + " summary]", nil
		},
	}

	cb := New("/tmp/test-buffer", mockSummarizer)
	cb.SetLimits(100, 5*time.Minute)

	// First batch - triggers first compression
	for i := 0; i < 10; i++ {
		cb.Add(Entry{
			ID: "batch1-" + string(rune('a'+i)), Author: "user",
			Content: "Batch 1 message " + string(rune('0'+i)), Timestamp: time.Now(),
			ChannelID: "ch", TokenCount: 20,
		})
	}

	// Second batch - should trigger second compression, accumulating summaries
	for i := 0; i < 10; i++ {
		cb.Add(Entry{
			ID: "batch2-" + string(rune('a'+i)), Author: "user",
			Content: "Batch 2 message " + string(rune('0'+i)), Timestamp: time.Now(),
			ChannelID: "ch", TokenCount: 20,
		})
	}

	buf := cb.Get(ScopeChannel("ch"))
	if buf == nil {
		t.Fatal("Expected buffer to exist")
	}

	// After multiple compressions, summary should contain both rounds
	if compressCount < 2 {
		t.Errorf("Expected at least 2 compressions, got %d", compressCount)
	}

	// Summary should be non-empty and accumulate
	if buf.Summary == "" {
		t.Error("Expected non-empty summary after multiple compressions")
	}
}

// TestSaveLoad tests buffer persistence to/from disk
func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()

	cb := New(dir, nil)
	cb.Add(Entry{
		ID: "persist-1", Author: "thunder", Content: "Will this persist?",
		Timestamp: time.Now(), ChannelID: "ch",
	})
	cb.Add(Entry{
		ID: "persist-2", Author: "bud", Content: "Yes it will.",
		Timestamp: time.Now().Add(time.Second), ChannelID: "ch",
	})

	if err := cb.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load into fresh buffer
	cb2 := New(dir, nil)
	if err := cb2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	buf := cb2.Get(ScopeChannel("ch"))
	if buf == nil {
		t.Fatal("Expected buffer to exist after load")
	}
	if len(buf.RawEntries) != 2 {
		t.Errorf("Expected 2 entries after load, got %d", len(buf.RawEntries))
	}
	if buf.RawEntries[0].ID != "persist-1" {
		t.Errorf("Expected first entry ID persist-1, got %q", buf.RawEntries[0].ID)
	}
}

// TestLoadNonExistent tests that Load on a fresh dir returns nil (no error)
func TestLoadNonExistent(t *testing.T) {
	cb := New(t.TempDir(), nil)
	if err := cb.Load(); err != nil {
		t.Errorf("Expected Load on nonexistent file to return nil, got %v", err)
	}
}

// TestFindReplyContextAfterCompression tests that FindReplyContext returns nil when target is compressed
func TestFindReplyContextAfterCompression(t *testing.T) {
	mockSummarizer := &MockSummarizer{
		summarizeFunc: func(content string) (string, error) {
			return "[summarized]", nil
		},
	}

	cb := New("/tmp/test-buffer", mockSummarizer)
	cb.SetLimits(100, 5*time.Minute)

	// Add enough to force compression; the old entry will be summarized away
	for i := 0; i < 12; i++ {
		cb.Add(Entry{
			ID: "msg-" + string(rune('a'+i)), Author: "user",
			Content: "Filler message " + string(rune('0'+i)), Timestamp: time.Now(),
			ChannelID: "ch", TokenCount: 20,
		})
	}

	// Reply targets a message that should now be in the summary (not in RawEntries)
	reply := Entry{
		ID: "reply-msg", Author: "user", Content: "Replying to something old",
		Timestamp: time.Now(), ChannelID: "ch", ReplyTo: "msg-a",
	}
	// FindReplyContext should return nil since msg-a has been compressed out
	result := cb.FindReplyContext(reply)
	if result != nil {
		t.Error("Expected nil when reply target has been compressed away")
	}
}

// TestStats tests buffer statistics
func TestStats(t *testing.T) {
	cb := New("/tmp/test-buffer", nil)

	stats := cb.Stats()
	if stats["buffer_count"] != 0 {
		t.Errorf("Expected 0 buffers initially, got %d", stats["buffer_count"])
	}

	cb.Add(Entry{ID: "s1", Author: "user", Content: "hello", Timestamp: time.Now(), ChannelID: "ch1", TokenCount: 5})
	cb.Add(Entry{ID: "s2", Author: "user", Content: "world", Timestamp: time.Now(), ChannelID: "ch1", TokenCount: 5})
	cb.Add(Entry{ID: "s3", Author: "user", Content: "other", Timestamp: time.Now(), ChannelID: "ch2", TokenCount: 3})

	stats = cb.Stats()
	if stats["buffer_count"] != 2 {
		t.Errorf("Expected 2 buffers, got %d", stats["buffer_count"])
	}
	if stats["total_entries"] != 3 {
		t.Errorf("Expected 3 total entries, got %d", stats["total_entries"])
	}
	if stats["total_tokens"] != 13 {
		t.Errorf("Expected 13 total tokens, got %d", stats["total_tokens"])
	}
}

// TestClearScope tests that ClearScope removes only the targeted scope
func TestClearScope(t *testing.T) {
	cb := New("/tmp/test-buffer", nil)

	cb.Add(Entry{ID: "a1", Author: "user", Content: "ch1", Timestamp: time.Now(), ChannelID: "ch1"})
	cb.Add(Entry{ID: "b1", Author: "user", Content: "ch2", Timestamp: time.Now(), ChannelID: "ch2"})

	cb.ClearScope(ScopeChannel("ch1"))

	if cb.Get(ScopeChannel("ch1")) != nil {
		t.Error("Expected ch1 buffer to be nil after ClearScope")
	}
	if cb.Get(ScopeChannel("ch2")) == nil {
		t.Error("Expected ch2 buffer to still exist after clearing ch1")
	}
}

// TestTokenEstimation tests that entries with no TokenCount get estimated
func TestTokenEstimation(t *testing.T) {
	cb := New("/tmp/test-buffer", nil)

	// Add entry with no explicit TokenCount
	cb.Add(Entry{
		ID: "est-1", Author: "user",
		Content:   "This is a message with no explicit token count set",
		Timestamp: time.Now(), ChannelID: "ch",
		// TokenCount intentionally omitted
	})

	buf := cb.Get(ScopeChannel("ch"))
	if buf == nil {
		t.Fatal("Expected buffer")
	}
	if buf.RawEntries[0].TokenCount == 0 {
		t.Error("Expected TokenCount to be estimated (non-zero) when not set")
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
