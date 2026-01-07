package executive

import (
	"testing"
	"time"

	"github.com/vthunder/bud2/internal/memory"
	"github.com/vthunder/bud2/internal/types"
)

func TestSessionManager_Focus(t *testing.T) {
	threads := memory.NewThreadPool("")
	tmux := &Tmux{} // mock tmux
	mgr := NewSessionManager(threads, tmux)

	// Create and add a thread
	thread1 := &types.Thread{
		ID:     "thread-1",
		Status: types.StatusActive,
	}
	threads.Add(thread1)

	// Focus the thread
	session, err := mgr.Focus(thread1)
	if err != nil {
		t.Fatalf("Focus failed: %v", err)
	}
	if session == nil {
		t.Fatal("Expected session, got nil")
	}
	if thread1.SessionState != types.SessionFocused {
		t.Errorf("Expected SessionFocused, got %s", thread1.SessionState)
	}
	if thread1.SessionID == "" {
		t.Error("Expected SessionID to be set")
	}
}

func TestSessionManager_FocusSwitchesOldFocusToActive(t *testing.T) {
	threads := memory.NewThreadPool("")
	tmux := &Tmux{}
	mgr := NewSessionManager(threads, tmux)

	thread1 := &types.Thread{ID: "thread-1", Status: types.StatusActive}
	thread2 := &types.Thread{ID: "thread-2", Status: types.StatusActive}
	threads.Add(thread1)
	threads.Add(thread2)

	// Focus thread1
	mgr.Focus(thread1)
	if thread1.SessionState != types.SessionFocused {
		t.Errorf("thread1: Expected SessionFocused, got %s", thread1.SessionState)
	}

	// Focus thread2 - thread1 should become active
	mgr.Focus(thread2)
	if thread2.SessionState != types.SessionFocused {
		t.Errorf("thread2: Expected SessionFocused, got %s", thread2.SessionState)
	}
	if thread1.SessionState != types.SessionActive {
		t.Errorf("thread1: Expected SessionActive, got %s", thread1.SessionState)
	}
}

func TestSessionManager_FreezesOldestWhenAtLimit(t *testing.T) {
	threads := memory.NewThreadPool("")
	tmux := &Tmux{}
	mgr := NewSessionManager(threads, tmux)

	// Create MaxActiveSessions + 1 threads
	threadList := make([]*types.Thread, MaxActiveSessions+1)
	for i := range threadList {
		threadList[i] = &types.Thread{
			ID:     "thread-" + string(rune('a'+i)),
			Status: types.StatusActive,
		}
		threads.Add(threadList[i])
	}

	// Focus each thread with small delays for LastActive ordering
	for i, thread := range threadList {
		mgr.Focus(thread)
		if i < len(threadList)-1 {
			time.Sleep(10 * time.Millisecond) // ensure different LastActive times
		}
	}

	// Check states: first should be frozen, last should be focused
	// Middle ones should be active
	if threadList[0].SessionState != types.SessionFrozen {
		t.Errorf("thread-a: Expected SessionFrozen (oldest), got %s", threadList[0].SessionState)
	}
	if threadList[len(threadList)-1].SessionState != types.SessionFocused {
		t.Errorf("last thread: Expected SessionFocused, got %s", threadList[len(threadList)-1].SessionState)
	}

	// Count active sessions (focused + active)
	stats := mgr.Stats()
	activeCount := stats.Focused + stats.Active
	if activeCount > MaxActiveSessions {
		t.Errorf("Expected at most %d active sessions, got %d", MaxActiveSessions, activeCount)
	}
}

func TestSessionManager_Resume(t *testing.T) {
	threads := memory.NewThreadPool("")
	tmux := &Tmux{}
	mgr := NewSessionManager(threads, tmux)

	thread1 := &types.Thread{ID: "thread-1", Status: types.StatusActive}
	threads.Add(thread1)

	// Focus and then freeze
	mgr.Focus(thread1)
	originalSessionID := thread1.SessionID
	mgr.Freeze(thread1)

	if thread1.SessionState != types.SessionFrozen {
		t.Errorf("Expected SessionFrozen, got %s", thread1.SessionState)
	}
	// Session ID should be preserved for resume
	if thread1.SessionID != originalSessionID {
		t.Errorf("SessionID changed after freeze: %s -> %s", originalSessionID, thread1.SessionID)
	}

	// Resume
	session, err := mgr.Resume(thread1)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if session == nil {
		t.Fatal("Expected session, got nil")
	}
	if thread1.SessionState != types.SessionActive {
		t.Errorf("Expected SessionActive after resume, got %s", thread1.SessionState)
	}
	// Session ID should still be the same
	if thread1.SessionID != originalSessionID {
		t.Errorf("SessionID changed after resume: %s -> %s", originalSessionID, thread1.SessionID)
	}
}

func TestSessionManager_Stats(t *testing.T) {
	threads := memory.NewThreadPool("")
	tmux := &Tmux{}
	mgr := NewSessionManager(threads, tmux)

	// Create threads with different states
	focused := &types.Thread{ID: "focused", Status: types.StatusActive}
	active1 := &types.Thread{ID: "active1", Status: types.StatusActive}
	active2 := &types.Thread{ID: "active2", Status: types.StatusActive}
	threads.Add(focused)
	threads.Add(active1)
	threads.Add(active2)

	mgr.Focus(focused)
	mgr.Focus(active1)
	mgr.Focus(active2)
	mgr.Focus(focused) // bring focused back to front

	stats := mgr.Stats()
	if stats.Focused != 1 {
		t.Errorf("Expected 1 focused, got %d", stats.Focused)
	}
	if stats.Active != 2 {
		t.Errorf("Expected 2 active, got %d", stats.Active)
	}
}

func TestSessionManager_PruneFrozen(t *testing.T) {
	threads := memory.NewThreadPool("")
	tmux := &Tmux{}
	mgr := NewSessionManager(threads, tmux)

	// Create a frozen thread with old LastActive
	oldThread := &types.Thread{
		ID:           "old-thread",
		Status:       types.StatusPaused,
		SessionState: types.SessionFrozen,
		SessionID:    "old-session-id",
		LastActive:   time.Now().Add(-MaxFrozenAge - time.Hour), // older than limit
	}
	threads.Add(oldThread)

	// Create a recent frozen thread
	recentThread := &types.Thread{
		ID:           "recent-thread",
		Status:       types.StatusPaused,
		SessionState: types.SessionFrozen,
		SessionID:    "recent-session-id",
		LastActive:   time.Now().Add(-time.Hour), // recent
	}
	threads.Add(recentThread)

	pruned := mgr.PruneFrozen()
	if pruned != 1 {
		t.Errorf("Expected 1 pruned, got %d", pruned)
	}
	if oldThread.SessionState != types.SessionNone {
		t.Errorf("Old thread should be SessionNone, got %s", oldThread.SessionState)
	}
	if oldThread.SessionID != "" {
		t.Errorf("Old thread SessionID should be cleared, got %s", oldThread.SessionID)
	}
	if recentThread.SessionState != types.SessionFrozen {
		t.Errorf("Recent thread should still be frozen, got %s", recentThread.SessionState)
	}
}
