package focus

import (
	"time"
)

// Priority defines the importance level of a pending item
type Priority int

const (
	P0Critical    Priority = 0 // Time-critical (alarms, reminders) - preempts all
	P1UserInput   Priority = 1 // User messages - high priority
	P2DueTask     Priority = 2 // Deadlines, scheduled tasks - medium-high
	P3ActiveWork  Priority = 3 // Continuation of active work - medium
	P4Exploration Priority = 4 // Ideas, exploration - idle only
)

// PendingItem represents something waiting for attention
type PendingItem struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`      // user_input, due_task, reminder, idea, etc.
	Priority  Priority       `json:"priority"`
	Salience  float64        `json:"salience"`  // 0.0-1.0 computed importance
	Source    string         `json:"source"`    // discord, calendar, internal, etc.
	Content   string         `json:"content"`   // main content
	ChannelID string         `json:"channel_id,omitempty"`
	AuthorID  string         `json:"author_id,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"` // additional context
}

// Mode defines the attention mode for a domain
type Mode struct {
	Domain    string    `json:"domain"`     // "gtd", "calendar", "all", specific channel
	Action    string    `json:"action"`     // "bypass_reflex", "debug", "practice"
	SetBy     string    `json:"set_by"`     // "executive", "user"
	ExpiresAt time.Time `json:"expires_at"` // when mode expires
	Reason    string    `json:"reason,omitempty"`
}

// IsExpired returns true if the mode has expired
func (m *Mode) IsExpired() bool {
	if m.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(m.ExpiresAt)
}

// FocusState represents the current attention state
type FocusState struct {
	CurrentItem *PendingItem   `json:"current_item,omitempty"` // What we're focused on
	Suspended   []*PendingItem `json:"suspended,omitempty"`    // Stack of interrupted items
	Modes       []*Mode        `json:"modes,omitempty"`        // Active attention modes
	Arousal     float64        `json:"arousal"`                // 0.0-1.0 overall activity level
}

// ContextBundle represents assembled context for the executive
type ContextBundle struct {
	CurrentFocus       *PendingItem
	Suspended          []*PendingItem
	BufferContent      string            // Conversation buffer for current scope
	HasAuthorizations  bool              // True if buffer contains historical authorizations
	Memories           []MemorySummary   // Retrieved memory traces (new ones only)
	PriorMemoriesCount int               // Count of memories already sent this session
	ReflexLog          []ReflexActivity  // Recent reflex activity
	CoreIdentity       string            // Core identity (verbatim from core.md)
	Metadata           map[string]string // Additional context
	WakeSessionContext string            // Recent conversation context for autonomous wake prompts
}

// MemorySummary is a simplified view of a memory trace for context
type MemorySummary struct {
	ID        string    `json:"id"`
	Summary   string    `json:"summary"`
	Level     int       `json:"level,omitempty"` // Compression level (0 = stored summary)
	Timestamp time.Time `json:"timestamp"`       // When the memory was created
}

// ReflexActivity represents a recent reflex action for context
type ReflexActivity struct {
	Timestamp time.Time `json:"timestamp"`
	Query     string    `json:"query"`
	Response  string    `json:"response"`
	Reflex    string    `json:"reflex"`
}

// String returns a string representation of Priority
func (p Priority) String() string {
	switch p {
	case P0Critical:
		return "P0:Critical"
	case P1UserInput:
		return "P1:UserInput"
	case P2DueTask:
		return "P2:DueTask"
	case P3ActiveWork:
		return "P3:ActiveWork"
	case P4Exploration:
		return "P4:Exploration"
	default:
		return "Unknown"
	}
}
