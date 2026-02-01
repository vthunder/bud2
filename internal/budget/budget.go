package budget

import (
	"fmt"
	"log"
	"time"
)

// ThinkingBudget manages limits on autonomous Claude usage
type ThinkingBudget struct {
	tracker *SessionTracker

	// Limits
	DailyOutputTokens  int           // Max output tokens per 24h (default 1_000_000)
	MaxSessionDuration time.Duration // Max single session length (e.g., 10 min)
}

// NewThinkingBudget creates a new budget manager
func NewThinkingBudget(tracker *SessionTracker) *ThinkingBudget {
	return &ThinkingBudget{
		tracker:            tracker,
		DailyOutputTokens:  1_000_000,        // 1M output tokens/day default
		MaxSessionDuration: 10 * time.Minute,  // 10 min max per session
	}
}

// CanDoAutonomousWork checks if autonomous work is allowed
func (b *ThinkingBudget) CanDoAutonomousWork() (bool, string) {
	if b.tracker != nil {
		usage := b.tracker.TodayTokenUsage()
		if usage.OutputTokens >= b.DailyOutputTokens {
			return false, fmt.Sprintf("daily output token budget exceeded (%d/%d)",
				usage.OutputTokens, b.DailyOutputTokens)
		}
	}

	return true, ""
}

// GetStatus returns current budget status
func (b *ThinkingBudget) GetStatus() BudgetStatus {
	var usage TokenUsage
	activeSessions := 0
	longestActive := time.Duration(0)

	if b.tracker != nil {
		usage = b.tracker.TodayTokenUsage()
		if b.tracker.HasActiveSessions() {
			activeSessions = len(b.tracker.GetActiveSessions())
		}
		longestActive = b.tracker.LongestActiveSession()
	}

	return BudgetStatus{
		TodayOutputTokens:     usage.OutputTokens,
		DailyOutputTokenLimit: b.DailyOutputTokens,
		RemainingOutputTokens: b.DailyOutputTokens - usage.OutputTokens,
		TodayInputTokens:      usage.InputTokens,
		TodayCacheReadTokens:  usage.CacheReadInputTokens,
		TodayCacheWriteTokens: usage.CacheCreationInputTokens,
		TodaySessions:         usage.SessionCount,
		TodayTurns:            usage.TotalTurns,
		ActiveSessions:        activeSessions,
		LongestActiveDur:      longestActive,
		CanDoAutonomous:       b.canDoAutonomousInternal(),
	}
}

func (b *ThinkingBudget) canDoAutonomousInternal() bool {
	can, _ := b.CanDoAutonomousWork()
	return can
}

// BudgetStatus represents current budget state
type BudgetStatus struct {
	TodayOutputTokens     int
	DailyOutputTokenLimit int
	RemainingOutputTokens int
	TodayInputTokens      int
	TodayCacheReadTokens  int
	TodayCacheWriteTokens int
	TodaySessions         int
	TodayTurns            int
	ActiveSessions        int
	LongestActiveDur      time.Duration
	CanDoAutonomous       bool
}

// LogStatus logs the current budget status
func (b *ThinkingBudget) LogStatus() {
	status := b.GetStatus()
	log.Printf("[budget] Today: %dk/%dk output tokens (%d sessions, %d turns) | Active: %d | Can do autonomous: %v",
		status.TodayOutputTokens/1000, status.DailyOutputTokenLimit/1000,
		status.TodaySessions, status.TodayTurns,
		status.ActiveSessions, status.CanDoAutonomous)
}
