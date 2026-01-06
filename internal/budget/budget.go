package budget

import (
	"log"
	"time"
)

// ThinkingBudget manages limits on autonomous Claude usage
type ThinkingBudget struct {
	tracker *SessionTracker

	// Limits
	DailyMinutes         float64       // Max thinking minutes per day (e.g., 30)
	MaxSessionDuration   time.Duration // Max single session length (e.g., 10 min)
	MinIntervalBetween   time.Duration // Minimum time between autonomous calls (e.g., 1 hour)

	// State
	lastAutonomousCall time.Time
}

// NewThinkingBudget creates a new budget manager
func NewThinkingBudget(tracker *SessionTracker) *ThinkingBudget {
	return &ThinkingBudget{
		tracker:            tracker,
		DailyMinutes:       30,               // 30 minutes/day default
		MaxSessionDuration: 10 * time.Minute, // 10 min max per session
		MinIntervalBetween: 1 * time.Hour,    // 1 hour between autonomous calls
	}
}

// CanDoAutonomousWork checks if autonomous work is allowed
func (b *ThinkingBudget) CanDoAutonomousWork() (bool, string) {
	// Check if a session is already active
	if b.tracker != nil && b.tracker.HasActiveSessions() {
		return false, "session already active"
	}

	// Check daily budget
	if b.tracker != nil {
		todayMinutes := b.tracker.TodayThinkingMinutes()
		if todayMinutes >= b.DailyMinutes {
			return false, "daily thinking budget exceeded"
		}
	}

	// Check minimum interval
	if !b.lastAutonomousCall.IsZero() {
		elapsed := time.Since(b.lastAutonomousCall)
		if elapsed < b.MinIntervalBetween {
			remaining := b.MinIntervalBetween - elapsed
			return false, "too soon since last autonomous call (wait " + remaining.Round(time.Minute).String() + ")"
		}
	}

	return true, ""
}

// RecordAutonomousCall marks that an autonomous call was made
func (b *ThinkingBudget) RecordAutonomousCall() {
	b.lastAutonomousCall = time.Now()
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
