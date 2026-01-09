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

	log.Printf("[session] Focus called: thread=%s, windowName=%s, sessionID=%s, state=%s",
		thread.ID, thread.WindowName, thread.SessionID, thread.SessionState)

	// If already focused and session exists, return it
	if thread.SessionState == types.SessionFocused {
		if session, ok := m.sessions[thread.ID]; ok {
			log.Printf("[session] EARLY RETURN: Returning existing session for focused thread")
			log.Printf("[session]   thread.SessionID=%s", thread.SessionID)
			log.Printf("[session]   session.sessionID=%s", session.sessionID)
			log.Printf("[session]   session.windowName=%s", session.windowName)
			if thread.SessionID != session.sessionID {
				log.Printf("[session]   WARNING: thread.SessionID != session.sessionID!")
			}
			return session, nil
		}
		// Session doesn't exist (e.g., after restart) - fall through to create one
		log.Printf("[session] Thread is focused but no session in memory, creating new one")
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

	// Track if we're generating a fresh session ID (vs reusing existing)
	isNewSessionID := thread.SessionID == ""
	log.Printf("[session] Session ID check: thread.SessionID='%s', isNewSessionID=%v", thread.SessionID, isNewSessionID)

	// Ensure thread has a session ID
	if isNewSessionID {
		thread.SessionID = m.generateSessionID()
		log.Printf("[session] GENERATED new session ID %s for thread %s", thread.SessionID, thread.ID)
	} else {
		log.Printf("[session] REUSING existing session ID %s for thread %s", thread.SessionID, thread.ID)
	}

	// Ensure thread has a window name
	if thread.WindowName == "" {
		thread.WindowName = m.generateWindowName()
	}

	// Get or create Claude session
	session, exists := m.sessions[thread.ID]
	if !exists {
		log.Printf("[session] CREATING new ClaudeSession for thread %s", thread.ID)
		session = NewClaudeSession(thread.ID, m.tmux)
		log.Printf("[session]   NewClaudeSession generated sessionID=%s (will be overwritten)", session.sessionID)
		session.sessionID = thread.SessionID
		session.windowName = thread.WindowName
		// If thread already had a session ID (not newly generated), the Claude Code
		// session likely exists on disk. Use --continue to be safe.
		// Claude Code creates the session file on startup, even if it dies later.
		session.sessionInitialized = !isNewSessionID
		m.sessions[thread.ID] = session
		log.Printf("[session]   Final session: sessionID=%s, windowName=%s, initialized=%v",
			session.sessionID, session.windowName, session.sessionInitialized)
	} else {
		log.Printf("[session] EXISTING session found in memory for thread %s", thread.ID)
		log.Printf("[session]   existing session.sessionID=%s", session.sessionID)
		log.Printf("[session]   thread.SessionID=%s", thread.SessionID)

		// Check if session ID changed (was regenerated due to errors)
		sessionIDChanged := session.sessionID != thread.SessionID
		log.Printf("[session]   sessionIDChanged=%v", sessionIDChanged)

		// Sync session ID and window name
		session.sessionID = thread.SessionID
		session.windowName = thread.WindowName

		// If session ID changed, this is a fresh session for Claude Code
		if sessionIDChanged {
			session.sessionInitialized = false
			session.firstMessageSent = false
			log.Printf("[session] Session ID CHANGED for thread %s: %s -> %s, resetting session state",
				thread.ID, session.sessionID, thread.SessionID)
		} else if !isNewSessionID {
			// Session ID didn't change and wasn't newly generated - session exists on disk
			session.sessionInitialized = true
		}
		log.Printf("[session]   After sync: sessionID=%s, initialized=%v", session.sessionID, session.sessionInitialized)
	}

	// Mark as focused
	thread.SessionState = types.SessionFocused
	thread.LastActive = time.Now()
	log.Printf("[session] Focused thread %s (session: %s, window: %s)", thread.ID, thread.SessionID, thread.WindowName)

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

	// Close the tmux window (but keep session ID and window name for later resume)
	if session, ok := m.sessions[thread.ID]; ok {
		// Use thread's window name
		windowName := thread.WindowName
		if windowName == "" {
			windowName = session.windowName
		}
		if windowName != "" && m.tmux.WindowExists(windowName) {
			if err := m.tmux.KillWindow(windowName); err != nil {
				log.Printf("[session] Warning: failed to kill window %s: %v", windowName, err)
			}
		}
		// Keep the session ID but mark as closed
		session.firstMessageSent = false // will need to reinitialize on resume
		session.pid = 0                  // clear PID
		session.processState = ProcessNone
		delete(m.sessions, thread.ID)
	}

	thread.SessionState = types.SessionFrozen
	log.Printf("[session] Froze thread %s (session %s, window %s preserved)", thread.ID, thread.SessionID, thread.WindowName)
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

	// Track if we're generating a fresh session ID
	isNewSessionID := thread.SessionID == ""

	// Create new session with preserved session ID and window name
	session := NewClaudeSession(thread.ID, m.tmux)
	if !isNewSessionID {
		session.sessionID = thread.SessionID // resume with same ID
		// If thread already had a session ID, the Claude Code session likely exists on disk
		session.sessionInitialized = true
	} else {
		thread.SessionID = session.sessionID // new session
		session.sessionInitialized = false
	}

	// Ensure thread has a window name
	if thread.WindowName == "" {
		thread.WindowName = m.generateWindowName()
	}
	session.windowName = thread.WindowName
	m.sessions[thread.ID] = session

	thread.SessionState = types.SessionActive
	thread.LastActive = time.Now()
	log.Printf("[session] Resumed thread %s (session: %s, window: %s, initialized: %v)",
		thread.ID, thread.SessionID, thread.WindowName, session.sessionInitialized)

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

// generateWindowName creates a human-readable window name
func (m *SessionManager) generateWindowName() string {
	word := randomWord()
	b := make([]byte, 1)
	_, _ = rand.Read(b)
	num := int(b[0])%99 + 1
	return fmt.Sprintf("%s-%d", word, num)
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
