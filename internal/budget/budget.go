package budget

import (
	"log"
	"time"
)

// ThinkingBudget manages limits on autonomous Claude usage
type ThinkingBudget struct {
	tracker *SessionTracker

	// Limits
	DailyMinutes       float64       // Max thinking minutes per day (e.g., 30)
	MaxSessionDuration time.Duration // Max single session length (e.g., 10 min)
}

// NewThinkingBudget creates a new budget manager
func NewThinkingBudget(tracker *SessionTracker) *ThinkingBudget {
	return &ThinkingBudget{
		tracker:            tracker,
		DailyMinutes:       30,               // 30 minutes/day default
		MaxSessionDuration: 10 * time.Minute, // 10 min max per session
	}
}

// CanDoAutonomousWork checks if autonomous work is allowed
func (b *ThinkingBudget) CanDoAutonomousWork() (bool, string) {
	// Note: We don't block on active sessions. The attention system handles
	// thread prioritization, and session manager handles concurrent limits.
	// Impulses can queue even if Claude is currently processing something.

	// Check daily budget
	if b.tracker != nil {
		todayMinutes := b.tracker.TodayThinkingMinutes()
		if todayMinutes >= b.DailyMinutes {
			return false, "daily thinking budget exceeded"
		}
	}

	return true, ""
}

// GetStatus returns current budget status
func (b *ThinkingBudget) GetStatus() BudgetStatus {
	todayMinutes := float64(0)
	activeSessions := 0
	longestActive := time.Duration(0)

	if b.tracker != nil {
		todayMinutes = b.tracker.TodayThinkingMinutes()
		if b.tracker.HasActiveSessions() {
			activeSessions = len(b.tracker.GetActiveSessions())
		}
		longestActive = b.tracker.LongestActiveSession()
	}

	return BudgetStatus{
		TodayMinutes:       todayMinutes,
		DailyBudget:        b.DailyMinutes,
		RemainingMinutes:   b.DailyMinutes - todayMinutes,
		ActiveSessions:     activeSessions,
		LongestActiveDur:   longestActive,
		CanDoAutonomous:    b.canDoAutonomousInternal(),
	}
}

func (b *ThinkingBudget) canDoAutonomousInternal() bool {
	can, _ := b.CanDoAutonomousWork()
	return can
}

// BudgetStatus represents current budget state
type BudgetStatus struct {
	TodayMinutes       float64
	DailyBudget        float64
	RemainingMinutes   float64
	ActiveSessions     int
	LongestActiveDur   time.Duration
	CanDoAutonomous    bool
}

// LogStatus logs the current budget status
func (b *ThinkingBudget) LogStatus() {
	status := b.GetStatus()
	log.Printf("[budget] Today: %.1f/%.1f min | Active sessions: %d | Can do autonomous: %v",
		status.TodayMinutes, status.DailyBudget, status.ActiveSessions, status.CanDoAutonomous)
}
