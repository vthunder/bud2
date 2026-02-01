package budget

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionTracking(t *testing.T) {
	// Setup temp directory
	statePath := t.TempDir()

	// Create tracker
	tracker := NewSessionTracker(statePath)

	// Initial state
	if tracker.HasActiveSessions() {
		t.Error("Expected no active sessions initially")
	}
	if tracker.TodayThinkingMinutes() != 0 {
		t.Error("Expected 0 thinking minutes initially")
	}

	// Start a session
	tracker.StartSession("session-1", "thread-1")
	if !tracker.HasActiveSessions() {
		t.Error("Expected active session after start")
	}

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Complete the session
	session := tracker.CompleteSession("session-1")
	if session == nil {
		t.Fatal("Expected session to be returned")
	}
	if session.DurationSec < 0.1 {
		t.Errorf("Expected duration > 0.1s, got %f", session.DurationSec)
	}

	// Check state after completion
	if tracker.HasActiveSessions() {
		t.Error("Expected no active sessions after completion")
	}
	if tracker.TodayThinkingMinutes() == 0 {
		t.Error("Expected some thinking minutes after completion")
	}

	t.Logf("Session completed in %.3f seconds", session.DurationSec)
	t.Logf("Today's thinking minutes: %.3f", tracker.TodayThinkingMinutes())
}

func TestSignalProcessor(t *testing.T) {
	statePath := t.TempDir()

	// Create tracker and processor
	tracker := NewSessionTracker(statePath)
	processor := NewSignalProcessor(statePath, tracker)

	// Start a session
	tracker.StartSession("session-1", "thread-1")

	// Write a signal file
	signalData := `{"type":"session_done","session_id":"session-1","summary":"Test done","timestamp":"2026-01-06T12:00:00Z"}`
	queuesPath := filepath.Join(statePath, "system", "queues")
	os.MkdirAll(queuesPath, 0755)
	signalsPath := filepath.Join(queuesPath, "signals.jsonl")
	if err := os.WriteFile(signalsPath, []byte(signalData+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Process signals
	n := processor.ProcessSignals()
	if n != 1 {
		t.Errorf("Expected 1 signal processed, got %d", n)
	}

	// Session should be completed
	if tracker.HasActiveSessions() {
		t.Error("Expected session to be completed after signal")
	}
}

func TestThinkingBudget(t *testing.T) {
	statePath := t.TempDir()

	tracker := NewSessionTracker(statePath)
	b := NewThinkingBudget(tracker)
	b.DailyOutputTokens = 1000 // 1000 output tokens for testing

	// Should be able to do autonomous work initially
	ok, reason := b.CanDoAutonomousWork()
	if !ok {
		t.Errorf("Expected to be able to do autonomous work, got: %s", reason)
	}

	// Active sessions should NOT block autonomous work (attention handles priority)
	tracker.StartSession("session-1", "thread-1")
	ok, _ = b.CanDoAutonomousWork()
	if !ok {
		t.Error("Expected autonomous work allowed even with active session")
	}

	// Complete session with some token usage (under budget)
	tracker.CompleteSession("session-1")
	tracker.SetSessionUsage("session-1", 5000, 500, 3000, 2000, 3)

	// Should still be allowed (500 < 1000 budget)
	ok, reason = b.CanDoAutonomousWork()
	if !ok {
		t.Errorf("Expected autonomous work allowed, got: %s", reason)
	}

	// Add another session that pushes over budget
	tracker.StartSession("session-2", "thread-2")
	tracker.CompleteSession("session-2")
	tracker.SetSessionUsage("session-2", 5000, 600, 3000, 2000, 2)

	// Should be blocked (500 + 600 = 1100 > 1000 budget)
	ok, reason = b.CanDoAutonomousWork()
	if ok {
		t.Error("Expected autonomous work blocked after exceeding token budget")
	}
	t.Logf("Budget correctly blocked: %s", reason)

	// Verify token usage aggregation
	usage := tracker.TodayTokenUsage()
	if usage.OutputTokens != 1100 {
		t.Errorf("Expected 1100 output tokens, got %d", usage.OutputTokens)
	}
	if usage.SessionCount != 2 {
		t.Errorf("Expected 2 sessions, got %d", usage.SessionCount)
	}
}
