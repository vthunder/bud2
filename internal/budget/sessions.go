package budget

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Session tracks a Claude thinking session
type Session struct {
	ID          string     `json:"id"`
	ThreadID    string     `json:"thread_id"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	DurationSec float64    `json:"duration_sec,omitempty"`
}

// SessionTracker tracks active and completed sessions
type SessionTracker struct {
	statePath string
	mu        sync.RWMutex

	// In-memory state
	active    map[string]*Session // sessionID -> session
	completed []*Session          // today's completed sessions
	today     string              // date string for daily reset
}

// NewSessionTracker creates a new tracker
func NewSessionTracker(statePath string) *SessionTracker {
	t := &SessionTracker{
		statePath: statePath,
		active:    make(map[string]*Session),
		today:     time.Now().Format("2006-01-02"),
	}
	t.load()
	return t
}

// StartSession records a new session starting
func (t *SessionTracker) StartSession(sessionID, threadID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.checkDayRollover()

	t.active[sessionID] = &Session{
		ID:        sessionID,
		ThreadID:  threadID,
		StartedAt: time.Now(),
	}
	t.save()
}

// CompleteSession marks a session as done
func (t *SessionTracker) CompleteSession(sessionID string) *Session {
	t.mu.Lock()
	defer t.mu.Unlock()

	session, ok := t.active[sessionID]
	if !ok {
		// Session not found - might have been started before tracker existed
		// Create a placeholder with unknown start time
		now := time.Now()
		session = &Session{
			ID:          sessionID,
			StartedAt:   now, // Unknown, use now
			CompletedAt: &now,
			DurationSec: 0, // Unknown
		}
		t.completed = append(t.completed, session)
		t.save()
		return session
	}

	now := time.Now()
	session.CompletedAt = &now
	session.DurationSec = now.Sub(session.StartedAt).Seconds()

	delete(t.active, sessionID)
	t.completed = append(t.completed, session)
	t.save()

	return session
}

// HasActiveSessions returns true if any sessions are running
func (t *SessionTracker) HasActiveSessions() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.active) > 0
}

// GetActiveSessions returns all active sessions
func (t *SessionTracker) GetActiveSessions() []*Session {
	t.mu.RLock()
	defer t.mu.RUnlock()

	sessions := make([]*Session, 0, len(t.active))
	for _, s := range t.active {
		sessions = append(sessions, s)
	}
	return sessions
}

// TodayThinkingMinutes returns total thinking time today in minutes
func (t *SessionTracker) TodayThinkingMinutes() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	t.checkDayRollover()

	var total float64
	for _, s := range t.completed {
		total += s.DurationSec
	}

	// Add time from active sessions (still running)
	for _, s := range t.active {
		total += time.Since(s.StartedAt).Seconds()
	}

	return total / 60.0
}

// LongestActiveSession returns the duration of the longest-running active session
func (t *SessionTracker) LongestActiveSession() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var longest time.Duration
	for _, s := range t.active {
		d := time.Since(s.StartedAt)
		if d > longest {
			longest = d
		}
	}
	return longest
}

// checkDayRollover resets completed sessions on new day
func (t *SessionTracker) checkDayRollover() {
	today := time.Now().Format("2006-01-02")
	if today != t.today {
		t.completed = nil
		t.today = today
	}
}

// Persistence

func (t *SessionTracker) filePath() string {
	return filepath.Join(t.statePath, "sessions.json")
}

func (t *SessionTracker) load() {
	data, err := os.ReadFile(t.filePath())
	if err != nil {
		return // File doesn't exist yet
	}

	var file struct {
		Date      string     `json:"date"`
		Active    []*Session `json:"active"`
		Completed []*Session `json:"completed"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return
	}

	// Only load if same day
	if file.Date == t.today {
		t.completed = file.Completed
		for _, s := range file.Active {
			t.active[s.ID] = s
		}
	}
}

func (t *SessionTracker) save() {
	active := make([]*Session, 0, len(t.active))
	for _, s := range t.active {
		active = append(active, s)
	}

	file := struct {
		Date      string     `json:"date"`
		Active    []*Session `json:"active"`
		Completed []*Session `json:"completed"`
	}{
		Date:      t.today,
		Active:    active,
		Completed: t.completed,
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}

	os.WriteFile(t.filePath(), data, 0644)
}
