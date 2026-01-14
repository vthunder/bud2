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

	// Entity edges (structural)
	EdgeSameAs    EdgeType = "SAME_AS"
	EdgeRelatedTo EdgeType = "RELATED_TO"
	EdgeMentions  EdgeType = "MENTIONS"

	// Entity edges (semantic relationships)
	EdgeWorksAt    EdgeType = "WORKS_AT"    // PERSON → ORG
	EdgeLivesIn    EdgeType = "LIVES_IN"    // PERSON → LOCATION
	EdgeMarriedTo  EdgeType = "MARRIED_TO"  // PERSON → PERSON
	EdgeSiblingOf  EdgeType = "SIBLING_OF"  // PERSON → PERSON
	EdgeParentOf   EdgeType = "PARENT_OF"   // PERSON → PERSON
	EdgeChildOf    EdgeType = "CHILD_OF"    // PERSON → PERSON
	EdgeFriendOf   EdgeType = "FRIEND_OF"   // PERSON → PERSON
	EdgeWorksOn    EdgeType = "WORKS_ON"    // PERSON → PROJECT/ORG
	EdgeLocatedIn  EdgeType = "LOCATED_IN"  // ORG/FAC → LOCATION
	EdgePartOf     EdgeType = "PART_OF"     // ORG → ORG (team → company)
	EdgeStudiedAt  EdgeType = "STUDIED_AT"  // PERSON → ORG (school)
	EdgeMetAt      EdgeType = "MET_AT"      // PERSON → LOCATION/ORG/EVENT
	EdgeCofounderOf EdgeType = "COFOUNDER_OF" // PERSON → PERSON
	EdgeOwnerOf    EdgeType = "OWNER_OF"    // PERSON → PRODUCT
	EdgeHasEmail   EdgeType = "HAS_EMAIL"   // PERSON → EMAIL
	EdgePrefers    EdgeType = "PREFERS"     // PERSON → PRODUCT/ORG/LOC
	EdgeAllergicTo EdgeType = "ALLERGIC_TO" // PERSON → PRODUCT
	EdgeHasPet     EdgeType = "HAS_PET"     // PERSON → PET

	// Trace edges
	EdgeSourcedFrom   EdgeType = "SOURCED_FROM"
	EdgeInvolves      EdgeType = "INVOLVES"
	EdgeInvalidatedBy EdgeType = "INVALIDATED_BY"
)

// EntityType defines categories of entities (OntoNotes-compatible schema)
type EntityType string

const (
	// Core entity types (OntoNotes)
	EntityPerson    EntityType = "PERSON"     // People, including fictional
	EntityOrg       EntityType = "ORG"        // Organizations
	EntityGPE       EntityType = "GPE"        // Geopolitical entities (countries, cities, states)
	EntityLoc       EntityType = "LOC"        // Non-GPE locations (mountains, bodies of water)
	EntityFac       EntityType = "FAC"        // Facilities (buildings, airports, highways)
	EntityProduct   EntityType = "PRODUCT"    // Products (vehicles, weapons, foods)
	EntityEvent     EntityType = "EVENT"      // Named events (hurricanes, battles, wars)
	EntityWorkOfArt EntityType = "WORK_OF_ART" // Titles of books, songs, etc.
	EntityLaw       EntityType = "LAW"        // Named documents made into laws
	EntityLanguage  EntityType = "LANGUAGE"   // Named languages
	EntityNorp      EntityType = "NORP"       // Nationalities, religious or political groups

	// Numeric/temporal types (OntoNotes)
	EntityDate     EntityType = "DATE"     // Absolute or relative dates
	EntityTime     EntityType = "TIME"     // Times smaller than a day
	EntityMoney    EntityType = "MONEY"    // Monetary values
	EntityPercent  EntityType = "PERCENT"  // Percentages
	EntityQuantity EntityType = "QUANTITY" // Measurements
	EntityCardinal EntityType = "CARDINAL" // Numerals not covered by other types
	EntityOrdinal  EntityType = "ORDINAL"  // "first", "second", etc.

	// Custom types (extended beyond OntoNotes)
	EntityEmail EntityType = "EMAIL" // Email addresses
	EntityPet   EntityType = "PET"   // Pet names

	// Fallback
	EntityOther EntityType = "OTHER" // Unknown or unclassified
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
