package types

import "time"

// Percept is sensory input with automatic properties (no judgment)
type Percept struct {
	ID        string            `json:"id"`
	Source    string            `json:"source"`    // discord, github, calendar
	Type      string            `json:"type"`      // message, notification, event
	Intensity float64           `json:"intensity"` // 0.0-1.0, automatic
	Timestamp time.Time         `json:"timestamp"`
	Tags      []string          `json:"tags"` // [from:owner], [urgent], etc
	Data      map[string]any    `json:"data"` // source-specific payload
}

// Recency returns seconds since percept was created
func (p *Percept) Recency() float64 {
	return time.Since(p.Timestamp).Seconds()
}

// ThreadStatus represents the state of a thread
type ThreadStatus string

const (
	StatusActive   ThreadStatus = "active"
	StatusPaused   ThreadStatus = "paused"
	StatusFrozen   ThreadStatus = "frozen"
	StatusComplete ThreadStatus = "complete"
)

// Thread is a train of thought with computed salience
type Thread struct {
	ID          string            `json:"id"`
	Goal        string            `json:"goal"`
	Status      ThreadStatus      `json:"status"`
	Salience    float64           `json:"salience"` // computed from percepts + relevance
	PerceptRefs []string          `json:"percept_refs"` // many-to-many refs to percepts
	State       ThreadState       `json:"state"`
	CreatedAt   time.Time         `json:"created_at"`
	LastActive  time.Time         `json:"last_active"`
}

// ThreadState holds thread-specific context
type ThreadState struct {
	Phase    string         `json:"phase"`
	Context  map[string]any `json:"context"`
	NextStep string         `json:"next_step"`
}

// Action is a pending effector action
type Action struct {
	ID        string         `json:"id"`
	Effector  string         `json:"effector"` // discord, github
	Type      string         `json:"type"`     // send_message, comment
	Payload   map[string]any `json:"payload"`
	Status    string         `json:"status"` // pending, complete, failed
	Timestamp time.Time      `json:"timestamp"`
}

// Arousal modulates attention threshold
type Arousal struct {
	Level     float64        `json:"level"` // 0.0 (calm) to 1.0 (urgent)
	Factors   ArousalFactors `json:"factors"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// ArousalFactors contribute to arousal level
type ArousalFactors struct {
	UserWaiting    bool `json:"user_waiting"`
	RecentErrors   int  `json:"recent_errors"`
	BudgetPressure bool `json:"budget_pressure"`
}

// Reflex is a pattern-action rule
type Reflex struct {
	ID              string `json:"id"`
	Pattern         string `json:"pattern"` // source.type.* pattern
	Action          string `json:"action"`  // action to execute
	SpawnAwareness  bool   `json:"spawn_awareness"`
	AwarenessType   string `json:"awareness_type,omitempty"`
}
