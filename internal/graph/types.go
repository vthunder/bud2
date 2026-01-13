package graph

import (
	"time"
)

// EdgeType defines the type of relationship between nodes
type EdgeType string

const (
	// Episode edges
	EdgeRepliesTo EdgeType = "REPLIES_TO"
	EdgeFollows   EdgeType = "FOLLOWS"

	// Entity edges
	EdgeSameAs    EdgeType = "SAME_AS"
	EdgeRelatedTo EdgeType = "RELATED_TO"
	EdgeMentions  EdgeType = "MENTIONS"

	// Trace edges
	EdgeSourcedFrom   EdgeType = "SOURCED_FROM"
	EdgeInvolves      EdgeType = "INVOLVES"
	EdgeInvalidatedBy EdgeType = "INVALIDATED_BY"
)

// EntityType defines categories of entities
type EntityType string

const (
	EntityPerson   EntityType = "person"
	EntityProject  EntityType = "project"
	EntityConcept  EntityType = "concept"
	EntityLocation EntityType = "location"
	EntityTime     EntityType = "time"
	EntityOther    EntityType = "other"
)

// Episode represents a raw message in the memory graph (Tier 1)
type Episode struct {
	ID                string    `json:"id"`
	Content           string    `json:"content"`
	Source            string    `json:"source"`             // discord, calendar, etc.
	Author            string    `json:"author,omitempty"`
	AuthorID          string    `json:"author_id,omitempty"`
	Channel           string    `json:"channel,omitempty"`
	TimestampEvent    time.Time `json:"timestamp_event"`    // T: when it happened
	TimestampIngested time.Time `json:"timestamp_ingested"` // T': when we learned it
	DialogueAct       string    `json:"dialogue_act,omitempty"`
	EntropyScore      float64   `json:"entropy_score,omitempty"`
	Embedding         []float64 `json:"embedding,omitempty"`
	ReplyTo           string    `json:"reply_to,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// Entity represents an extracted named entity (Tier 2)
type Entity struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Type      EntityType `json:"type"`
	Salience  float64    `json:"salience"`
	Embedding []float64  `json:"embedding,omitempty"`
	Aliases   []string   `json:"aliases,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Trace represents a consolidated memory (Tier 3)
type Trace struct {
	ID           string    `json:"id"`
	Summary      string    `json:"summary"`
	Topic        string    `json:"topic,omitempty"`
	Activation   float64   `json:"activation"`
	Strength     int       `json:"strength"`
	IsCore       bool      `json:"is_core"`
	Embedding    []float64 `json:"embedding,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastAccessed time.Time `json:"last_accessed"`
	LabileUntil  time.Time `json:"labile_until,omitempty"`

	// Related data (populated on retrieval)
	SourceIDs []string `json:"source_ids,omitempty"`
	EntityIDs []string `json:"entity_ids,omitempty"`
}

// Edge represents a relationship between nodes
type Edge struct {
	ID        int64    `json:"id,omitempty"`
	FromID    string   `json:"from_id"`
	ToID      string   `json:"to_id"`
	Type      EdgeType `json:"type"`
	Weight    float64  `json:"weight"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// Neighbor represents a node connected by an edge (for spreading activation)
type Neighbor struct {
	ID     string
	Weight float64
	Type   EdgeType
}

// ActivationResult holds spreading activation results
type ActivationResult struct {
	NodeID     string
	NodeType   string // "episode", "entity", "trace"
	Activation float64
}

// RetrievalResult holds memory retrieval results
type RetrievalResult struct {
	Traces   []*Trace
	Episodes []*Episode
	Entities []*Entity
}

// IsLabile returns true if the trace is in its reconsolidation window
func (t *Trace) IsLabile() bool {
	if t.LabileUntil.IsZero() {
		return false
	}
	return time.Now().Before(t.LabileUntil)
}

// MakeLabile sets the trace as labile for the given duration
func (t *Trace) MakeLabile(duration time.Duration) {
	t.LabileUntil = time.Now().Add(duration)
}

// Recency returns seconds since the trace was last accessed
func (t *Trace) Recency() float64 {
	return time.Since(t.LastAccessed).Seconds()
}
