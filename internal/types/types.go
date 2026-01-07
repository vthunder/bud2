package types

import "time"

// Percept is sensory input with automatic properties (no judgment)
type Percept struct {
	ID        string            `json:"id"`
	Source    string            `json:"source"`    // discord, github, calendar
	Type      string            `json:"type"`      // message, notification, event
	Intensity float64           `json:"intensity"` // 0.0-1.0, automatic
	Timestamp time.Time         `json:"timestamp"`
	Tags      []string          `json:"tags"`      // [from:owner], [urgent], etc
	Data      map[string]any    `json:"data"`      // source-specific payload
	Features  map[string]any    `json:"features,omitempty"` // sense-defined clustering features
	Embedding []float64         `json:"embedding,omitempty"` // semantic embedding
}

// Recency returns seconds since percept was created
func (p *Percept) Recency() float64 {
	return time.Since(p.Timestamp).Seconds()
}

// ThreadStatus represents the logical state of a thread (conversation state)
type ThreadStatus string

const (
	StatusActive   ThreadStatus = "active"   // conversation is active
	StatusPaused   ThreadStatus = "paused"   // conversation paused
	StatusFrozen   ThreadStatus = "frozen"   // conversation frozen (deprecated, use SessionState)
	StatusComplete ThreadStatus = "complete" // conversation complete
)

// SessionState represents the runtime state of a thread's Claude session
type SessionState string

const (
	SessionFocused SessionState = "focused" // has attention, Claude running (limit: 1)
	SessionActive  SessionState = "active"  // Claude running in background (limit: 3)
	SessionFrozen  SessionState = "frozen"  // no Claude process, session on disk (unlimited)
	SessionNone    SessionState = ""        // no session yet
)

// Thread is a train of thought with computed salience
type Thread struct {
	ID          string            `json:"id"`
	Goal        string            `json:"goal"`
	Status      ThreadStatus      `json:"status"`
	Salience    float64           `json:"salience"`    // computed from percepts + relevance
	Activation  float64           `json:"activation"`  // current activation level (for routing)
	PerceptRefs []string          `json:"percept_refs"` // many-to-many refs to percepts
	State       ThreadState       `json:"state"`
	CreatedAt   time.Time         `json:"created_at"`
	LastActive  time.Time         `json:"last_active"`
	ProcessedAt *time.Time        `json:"processed_at,omitempty"` // when last sent to executive

	// Session management
	SessionID    string       `json:"session_id,omitempty"`    // Claude session ID for resume
	SessionState SessionState `json:"session_state,omitempty"` // runtime state: focused/active/frozen

	// Feature accumulation (for association matching)
	Features    ThreadFeatures    `json:"features"`

	// Semantic embeddings for topic matching
	Embeddings  ThreadEmbeddings  `json:"embeddings"`
}

// ThreadFeatures accumulates features from percepts for association matching
type ThreadFeatures struct {
	Channels map[string]float64 `json:"channels,omitempty"` // channel_id -> weight
	Authors  map[string]float64 `json:"authors,omitempty"`  // author_id -> weight
	Sources  map[string]float64 `json:"sources,omitempty"`  // source (discord, github) -> weight
}

// ThreadEmbeddings holds semantic embeddings for a thread
type ThreadEmbeddings struct {
	Centroid []float64 `json:"centroid,omitempty"` // average of percept embeddings
	Topic    []float64 `json:"topic,omitempty"`    // embedding of thread goal/summary
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

// Trace is a consolidated memory unit (compressed from percepts)
type Trace struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`    // summarized gist
	Embedding  []float64 `json:"embedding"`  // for similarity matching
	Activation float64   `json:"activation"` // current activation level (decays)
	Strength   int       `json:"strength"`   // reinforcement count
	Sources    []string  `json:"sources"`    // percept IDs that contributed
	IsCore     bool      `json:"is_core"`    // core identity traces (always activated)
	CreatedAt  time.Time `json:"created_at"`
	LastAccess time.Time `json:"last_access"`

	// Biological memory mechanisms
	Inhibits   []string   `json:"inhibits,omitempty"`    // IDs of traces this one suppresses
	LabileUntil *time.Time `json:"labile_until,omitempty"` // trace is modifiable until this time
}

// Recency returns seconds since trace was last accessed
func (t *Trace) Recency() float64 {
	return time.Since(t.LastAccess).Seconds()
}

// ImpulseSource identifies where an impulse came from
type ImpulseSource string

const (
	ImpulseTask     ImpulseSource = "task"     // from tasks.json - commitment due
	ImpulseIdea     ImpulseSource = "idea"     // from ideas.json - exploration urge
	ImpulseSchedule ImpulseSource = "schedule" // from schedule.json - recurring trigger
	ImpulseSystem   ImpulseSource = "system"   // system-generated (autonomous wake, etc)
)

// Impulse is an internal motivation (vs Percept which is external)
// Impulses and percepts are scored together by attention
type Impulse struct {
	ID          string            `json:"id"`
	Source      ImpulseSource     `json:"source"`    // task, idea, schedule, system
	Type        string            `json:"type"`      // due, triggered, explore, wake
	Intensity   float64           `json:"intensity"` // 0.0-1.0, based on urgency/priority
	Timestamp   time.Time         `json:"timestamp"`
	Description string            `json:"description"` // what this impulse is about
	Data        map[string]any    `json:"data"`        // source-specific payload
	Embedding   []float64         `json:"embedding,omitempty"` // for attention matching
}

// Recency returns seconds since impulse was created
func (i *Impulse) Recency() float64 {
	return time.Since(i.Timestamp).Seconds()
}

// ToPercept converts an impulse to a percept for unified attention processing
func (i *Impulse) ToPercept() *Percept {
	return &Percept{
		ID:        i.ID,
		Source:    "impulse:" + string(i.Source),
		Type:      i.Type,
		Intensity: i.Intensity,
		Timestamp: i.Timestamp,
		Tags:      []string{"internal", string(i.Source)},
		Data: map[string]any{
			"content":     i.Description, // used by prompt builder
			"description": i.Description,
			"impulse":     i.Data,
		},
		Embedding: i.Embedding,
	}
}

// IsLabile returns true if the trace is in its reconsolidation window
func (t *Trace) IsLabile() bool {
	if t.LabileUntil == nil {
		return false
	}
	return time.Now().Before(*t.LabileUntil)
}

// MakeLabile sets the trace's labile window (reconsolidation period)
func (t *Trace) MakeLabile(duration time.Duration) {
	until := time.Now().Add(duration)
	t.LabileUntil = &until
}
