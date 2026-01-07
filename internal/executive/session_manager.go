package executive

import (
	"crypto/rand"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/memory"
	"github.com/vthunder/bud2/internal/types"
)

const (
	// MaxActiveSessions is the max number of threads with running Claude processes
	// (including the focused one)
	MaxActiveSessions = 3

	// MaxFrozenAge is how long to keep frozen sessions before pruning
	MaxFrozenAge = 7 * 24 * time.Hour // 7 days
)

// SessionManager manages thread session states (focused/active/frozen)
type SessionManager struct {
	mu      sync.RWMutex
	threads *memory.ThreadPool
	tmux    *Tmux

	// Track active Claude sessions by thread ID
	sessions map[string]*ClaudeSession
}

// NewSessionManager creates a new session manager
func NewSessionManager(threads *memory.ThreadPool, tmux *Tmux) *SessionManager {
	return &SessionManager{
		threads:  threads,
		sessions: make(map[string]*ClaudeSession),
		tmux:     tmux,
	}
}

// Focus makes a thread the focused one (limit: 1)
// Returns the session for the thread
func (m *SessionManager) Focus(thread *types.Thread) (*ClaudeSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If already focused, just return the session
	if thread.SessionState == types.SessionFocused {
		return m.sessions[thread.ID], nil
	}

	// Find current focused thread (if any) and move it to active
	for _, t := range m.threads.All() {
		if t.SessionState == types.SessionFocused && t.ID != thread.ID {
			t.SessionState = types.SessionActive
			t.LastActive = time.Now()
			log.Printf("[session] Unfocused thread %s -> active", t.ID)
		}
	}

	// Count active sessions (focused + active)
	activeCount := m.countActiveSessions()

	// If we're at limit and this thread doesn't have a session, freeze oldest
	if activeCount >= MaxActiveSessions && thread.SessionState != types.SessionActive {
		if err := m.freezeOldestActive(); err != nil {
			log.Printf("[session] Warning: failed to freeze oldest: %v", err)
		}
	}

	// Ensure thread has a session ID
	if thread.SessionID == "" {
		thread.SessionID = m.generateSessionID()
	}

	// Get or create Claude session
	session, exists := m.sessions[thread.ID]
	if !exists {
		session = NewClaudeSession(thread.ID, m.tmux)
		session.sessionID = thread.SessionID // use thread's session ID for resume
		m.sessions[thread.ID] = session
	}

	// Mark as focused
	thread.SessionState = types.SessionFocused
	thread.LastActive = time.Now()
	log.Printf("[session] Focused thread %s (session: %s)", thread.ID, thread.SessionID)

	return session, nil
}

// Unfocus moves the focused thread to active state
func (m *SessionManager) Unfocus(thread *types.Thread) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if thread.SessionState == types.SessionFocused {
		thread.SessionState = types.SessionActive
		thread.LastActive = time.Now()
		log.Printf("[session] Unfocused thread %s -> active", thread.ID)
	}
}

// Freeze stops the Claude process for a thread but keeps session on disk
func (m *SessionManager) Freeze(thread *types.Thread) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.freezeThread(thread)
}

// freezeThread internal implementation (must hold lock)
func (m *SessionManager) freezeThread(thread *types.Thread) error {
	if thread.SessionState == types.SessionFrozen || thread.SessionState == types.SessionNone {
		return nil // already frozen or no session
	}

	// Close the tmux window (but keep session ID for later resume)
	if session, ok := m.sessions[thread.ID]; ok {
		windowName := fmt.Sprintf("thread-%s", thread.ID)
		if m.tmux.WindowExists(windowName) {
			if err := m.tmux.KillWindow(windowName); err != nil {
				log.Printf("[session] Warning: failed to kill window %s: %v", windowName, err)
			}
		}
		// Keep the session ID but mark as closed
		session.firstMessageSent = false // will need to reinitialize on resume
		delete(m.sessions, thread.ID)
	}

	thread.SessionState = types.SessionFrozen
	log.Printf("[session] Froze thread %s (session %s preserved)", thread.ID, thread.SessionID)
	return nil
}

// Resume restores a frozen thread to active state
func (m *SessionManager) Resume(thread *types.Thread) (*ClaudeSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if thread.SessionState != types.SessionFrozen {
		// Not frozen, just get the session
		if session, ok := m.sessions[thread.ID]; ok {
			return session, nil
		}
	}

	// Count active sessions
	activeCount := m.countActiveSessions()

	// If at limit, freeze oldest
	if activeCount >= MaxActiveSessions {
		if err := m.freezeOldestActive(); err != nil {
			log.Printf("[session] Warning: failed to freeze oldest: %v", err)
		}
	}

	// Create new session with preserved session ID
	session := NewClaudeSession(thread.ID, m.tmux)
	if thread.SessionID != "" {
		session.sessionID = thread.SessionID // resume with same ID
	} else {
		thread.SessionID = session.sessionID // new session
	}
	m.sessions[thread.ID] = session

	thread.SessionState = types.SessionActive
	thread.LastActive = time.Now()
	log.Printf("[session] Resumed thread %s (session: %s)", thread.ID, thread.SessionID)

	return session, nil
}

// GetSession returns the session for a thread (nil if frozen/none)
func (m *SessionManager) GetSession(threadID string) *ClaudeSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[threadID]
}

// countActiveSessions returns count of focused + active sessions (must hold lock)
func (m *SessionManager) countActiveSessions() int {
	count := 0
	for _, t := range m.threads.All() {
		if t.SessionState == types.SessionFocused || t.SessionState == types.SessionActive {
			count++
		}
	}
	return count
}

// freezeOldestActive freezes the oldest active (non-focused) thread (must hold lock)
func (m *SessionManager) freezeOldestActive() error {
	// Find active threads sorted by last active time
	var activeThreads []*types.Thread
	for _, t := range m.threads.All() {
		if t.SessionState == types.SessionActive {
			activeThreads = append(activeThreads, t)
		}
	}

	if len(activeThreads) == 0 {
		return nil // nothing to freeze
	}

	// Sort by LastActive (oldest first)
	sort.Slice(activeThreads, func(i, j int) bool {
		return activeThreads[i].LastActive.Before(activeThreads[j].LastActive)
	})

	// Freeze the oldest
	oldest := activeThreads[0]
	return m.freezeThread(oldest)
}

// generateSessionID creates a unique session ID
func (m *SessionManager) generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Stats returns session statistics
func (m *SessionManager) Stats() SessionStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := SessionStats{}
	for _, t := range m.threads.All() {
		switch t.SessionState {
		case types.SessionFocused:
			stats.Focused++
		case types.SessionActive:
			stats.Active++
		case types.SessionFrozen:
			stats.Frozen++
		}
	}
	stats.TotalSessions = len(m.sessions)
	return stats
}

// SessionStats holds session statistics
type SessionStats struct {
	Focused       int
	Active        int
	Frozen        int
	TotalSessions int
}

// PruneFrozen removes frozen threads older than MaxFrozenAge
func (m *SessionManager) PruneFrozen() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-MaxFrozenAge)
	pruned := 0

	for _, t := range m.threads.All() {
		if t.SessionState == types.SessionFrozen && t.LastActive.Before(cutoff) {
			// Clear session ID (can't resume anymore)
			t.SessionID = ""
			t.SessionState = types.SessionNone
			pruned++
			log.Printf("[session] Pruned old frozen thread %s", t.ID)
		}
	}

	return pruned
}

// CloseAll closes all active sessions (for shutdown)
func (m *SessionManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, t := range m.threads.All() {
		if t.SessionState == types.SessionFocused || t.SessionState == types.SessionActive {
			m.freezeThread(t)
		}
	}
}
