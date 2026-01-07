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
	signalsPath := filepath.Join(statePath, "signals.jsonl")
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
	budget := NewThinkingBudget(tracker)
	budget.DailyMinutes = 1 // 1 minute for testing

	// Should be able to do autonomous work initially
	ok, reason := budget.CanDoAutonomousWork()
	if !ok {
		t.Errorf("Expected to be able to do autonomous work, got: %s", reason)
	}

	// Active sessions should NOT block autonomous work (attention handles priority)
	tracker.StartSession("session-1", "thread-1")
	ok, _ = budget.CanDoAutonomousWork()
	if !ok {
		t.Error("Expected autonomous work allowed even with active session")
	}

	// Complete the session
	tracker.CompleteSession("session-1")

	// Should still be allowed (budget not exhausted)
	ok, reason = budget.CanDoAutonomousWork()
	if !ok {
		t.Errorf("Expected autonomous work allowed, got: %s", reason)
	}

	t.Log("Budget test passed")
}
