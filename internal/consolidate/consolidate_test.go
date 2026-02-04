package consolidate

// Tests for episode grouping logic in consolidation.
// Covers: time-based grouping, channel-based grouping, entity overlap, edge cases.

import (
	"os"
	"testing"
	"time"

	"github.com/vthunder/bud2/internal/graph"
)

// setupTestDB creates a temporary test database
func setupTestDB(t *testing.T) (*graph.DB, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "consolidate-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	db, err := graph.Open(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to open database: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return db, cleanup
}

// mockLLM implements LLMClient for tests
type mockLLM struct{}

func (m *mockLLM) Embed(text string) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3, 0.4}, nil
}

func (m *mockLLM) Summarize(fragments []string) (string, error) {
	// Return a simple concatenation
	result := ""
	for i, f := range fragments {
		if i > 0 {
			result += " | "
		}
		result += f
	}
	return result, nil
}

func TestGroupEpisodesByTimeProximity(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	c := NewConsolidator(db, &mockLLM{})
	c.TimeWindow = 30 * time.Minute

	now := time.Now()

	// Create episodes: first two within 10 min, third is 1 hour later
	episodes := []*graph.Episode{
		{ID: "ep-1", Content: "First message", Channel: "general", TimestampEvent: now},
		{ID: "ep-2", Content: "Second message", Channel: "general", TimestampEvent: now.Add(10 * time.Minute)},
		{ID: "ep-3", Content: "Third message", Channel: "general", TimestampEvent: now.Add(1 * time.Hour)},
	}

	groups := c.groupEpisodes(episodes)

	// Should have 2 groups: (ep-1, ep-2) and (ep-3)
	if len(groups) != 2 {
		t.Fatalf("Expected 2 groups, got %d", len(groups))
	}

	// First group should have 2 episodes
	if len(groups[0].episodes) != 2 {
		t.Errorf("Expected first group to have 2 episodes, got %d", len(groups[0].episodes))
	}

	// Second group should have 1 episode
	if len(groups[1].episodes) != 1 {
		t.Errorf("Expected second group to have 1 episode, got %d", len(groups[1].episodes))
	}
}

func TestGroupEpisodesBySameChannel(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	c := NewConsolidator(db, &mockLLM{})
	c.TimeWindow = 30 * time.Minute

	now := time.Now()

	// Create episodes: same timestamp but different channels
	episodes := []*graph.Episode{
		{ID: "ep-1", Content: "Message in general", Channel: "general", TimestampEvent: now},
		{ID: "ep-2", Content: "Message in random", Channel: "random", TimestampEvent: now.Add(5 * time.Minute)},
		{ID: "ep-3", Content: "Another in general", Channel: "general", TimestampEvent: now.Add(10 * time.Minute)},
	}

	groups := c.groupEpisodes(episodes)

	// ep-1 and ep-3 should be grouped together (same channel, within time window)
	// ep-2 should be in its own group (different channel, no entity overlap)
	if len(groups) != 2 {
		t.Fatalf("Expected 2 groups, got %d", len(groups))
	}

	// Find the group with ep-1
	var generalGroup *episodeGroup
	for _, g := range groups {
		for _, ep := range g.episodes {
			if ep.ID == "ep-1" {
				generalGroup = g
				break
			}
		}
	}

	if generalGroup == nil {
		t.Fatal("Could not find group containing ep-1")
	}

	// Check that ep-3 is also in this group
	foundEp3 := false
	for _, ep := range generalGroup.episodes {
		if ep.ID == "ep-3" {
			foundEp3 = true
			break
		}
	}
	if !foundEp3 {
		t.Error("Expected ep-3 to be grouped with ep-1 (same channel)")
	}
}

func TestGroupEpisodesByEntityOverlap(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Add entities
	db.AddEntity(&graph.Entity{ID: "entity-alice", Name: "Alice", Type: graph.EntityPerson})
	db.AddEntity(&graph.Entity{ID: "entity-bob", Name: "Bob", Type: graph.EntityPerson})

	// Add episodes with entity links
	// Note: The grouping algorithm processes episodes in order and doesn't
	// backtrack, so ep-3 (which mentions both Alice and Bob) will join ep-1's
	// group (which has Alice), but ep-2 (Bob only) won't be reconsidered.
	now := time.Now()
	episodes := []*graph.Episode{
		{ID: "ep-1", Content: "Alice said something", Channel: "ch-1", TimestampEvent: now},
		{ID: "ep-2", Content: "Bob responded", Channel: "ch-2", TimestampEvent: now.Add(5 * time.Minute)},
		{ID: "ep-3", Content: "Alice mentioned Bob", Channel: "ch-3", TimestampEvent: now.Add(10 * time.Minute)},
	}

	for _, ep := range episodes {
		db.AddEpisode(ep)
	}

	// Link entities to episodes
	db.LinkEpisodeToEntity("ep-1", "entity-alice")
	db.LinkEpisodeToEntity("ep-2", "entity-bob")
	db.LinkEpisodeToEntity("ep-3", "entity-alice")
	db.LinkEpisodeToEntity("ep-3", "entity-bob")

	c := NewConsolidator(db, &mockLLM{})
	c.TimeWindow = 30 * time.Minute

	groups := c.groupEpisodes(episodes)

	// Current behavior: 2 groups
	// - Group 1: ep-1 (Alice) + ep-3 (Alice, Bob) - joined via Alice overlap
	// - Group 2: ep-2 (Bob) - standalone because Bob wasn't in group when ep-2 was checked
	// This is a design choice: no backtracking, simpler algorithm.
	if len(groups) != 2 {
		t.Fatalf("Expected 2 groups, got %d", len(groups))
	}

	// Find the group containing ep-1
	var group1 *episodeGroup
	for _, g := range groups {
		for _, ep := range g.episodes {
			if ep.ID == "ep-1" {
				group1 = g
				break
			}
		}
	}

	if group1 == nil {
		t.Fatal("Could not find group containing ep-1")
	}

	// ep-3 should be in same group as ep-1 (Alice overlap)
	foundEp3 := false
	for _, ep := range group1.episodes {
		if ep.ID == "ep-3" {
			foundEp3 = true
			break
		}
	}
	if !foundEp3 {
		t.Error("Expected ep-3 to be grouped with ep-1 (Alice entity overlap)")
	}

	// Group should have both Alice and Bob in entityIDs (from ep-3)
	if !group1.entityIDs["entity-alice"] {
		t.Error("Expected entity-alice in group")
	}
	if !group1.entityIDs["entity-bob"] {
		t.Error("Expected entity-bob in group (added via ep-3)")
	}
}

func TestGroupEpisodesNoOverlap(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Add distinct entities
	db.AddEntity(&graph.Entity{ID: "entity-alice", Name: "Alice", Type: graph.EntityPerson})
	db.AddEntity(&graph.Entity{ID: "entity-bob", Name: "Bob", Type: graph.EntityPerson})

	now := time.Now()
	episodes := []*graph.Episode{
		{ID: "ep-1", Content: "Alice said something", Channel: "ch-alice", TimestampEvent: now},
		{ID: "ep-2", Content: "Bob said something", Channel: "ch-bob", TimestampEvent: now.Add(5 * time.Minute)},
	}

	for _, ep := range episodes {
		db.AddEpisode(ep)
	}

	// Link to different entities
	db.LinkEpisodeToEntity("ep-1", "entity-alice")
	db.LinkEpisodeToEntity("ep-2", "entity-bob")

	c := NewConsolidator(db, &mockLLM{})
	c.TimeWindow = 30 * time.Minute

	groups := c.groupEpisodes(episodes)

	// Different channels AND different entities = separate groups
	if len(groups) != 2 {
		t.Fatalf("Expected 2 groups (no overlap), got %d", len(groups))
	}
}

func TestGroupEpisodesRespectMaxGroupSize(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	c := NewConsolidator(db, &mockLLM{})
	c.TimeWindow = 30 * time.Minute
	c.MaxGroupSize = 3 // Limit to 3 episodes per group

	now := time.Now()

	// Create 5 episodes, all in same channel
	episodes := make([]*graph.Episode, 5)
	for i := 0; i < 5; i++ {
		episodes[i] = &graph.Episode{
			ID:             "ep-" + string(rune('1'+i)),
			Content:        "Message " + string(rune('1'+i)),
			Channel:        "general",
			TimestampEvent: now.Add(time.Duration(i) * time.Minute),
		}
	}

	groups := c.groupEpisodes(episodes)

	// With MaxGroupSize=3, first group gets 3, second group gets 2
	if len(groups) < 2 {
		t.Fatalf("Expected at least 2 groups (max size limit), got %d", len(groups))
	}

	for i, g := range groups {
		if len(g.episodes) > c.MaxGroupSize {
			t.Errorf("Group %d exceeds MaxGroupSize: %d > %d", i, len(g.episodes), c.MaxGroupSize)
		}
	}
}

func TestGroupEpisodesMinGroupSize(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	c := NewConsolidator(db, &mockLLM{})
	c.TimeWindow = 30 * time.Minute
	c.MinGroupSize = 2 // Require at least 2 episodes

	now := time.Now()

	// Create episodes: two close together, one far apart
	episodes := []*graph.Episode{
		{ID: "ep-1", Content: "First", Channel: "general", TimestampEvent: now},
		{ID: "ep-2", Content: "Second", Channel: "general", TimestampEvent: now.Add(5 * time.Minute)},
		{ID: "ep-3", Content: "Lonely", Channel: "random", TimestampEvent: now.Add(2 * time.Hour)},
	}

	groups := c.groupEpisodes(episodes)

	// ep-1 and ep-2 form a valid group (size 2)
	// ep-3 is alone (size 1) and should be skipped with MinGroupSize=2
	validGroups := 0
	for _, g := range groups {
		if len(g.episodes) >= c.MinGroupSize {
			validGroups++
		}
	}

	// Only groups with >= MinGroupSize should be returned
	for _, g := range groups {
		if len(g.episodes) < c.MinGroupSize {
			t.Errorf("Group with %d episodes returned despite MinGroupSize=%d", len(g.episodes), c.MinGroupSize)
		}
	}
}

func TestGroupEpisodesEmptyInput(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	c := NewConsolidator(db, &mockLLM{})

	groups := c.groupEpisodes(nil)
	if groups != nil {
		t.Error("Expected nil for empty input")
	}

	groups = c.groupEpisodes([]*graph.Episode{})
	if groups != nil {
		t.Error("Expected nil for empty slice")
	}
}

func TestGroupEpisodesSingleEpisode(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	c := NewConsolidator(db, &mockLLM{})
	c.MinGroupSize = 1

	episodes := []*graph.Episode{
		{ID: "ep-1", Content: "Solo message", Channel: "general", TimestampEvent: time.Now()},
	}

	groups := c.groupEpisodes(episodes)

	if len(groups) != 1 {
		t.Fatalf("Expected 1 group for single episode, got %d", len(groups))
	}

	if len(groups[0].episodes) != 1 {
		t.Errorf("Expected 1 episode in group, got %d", len(groups[0].episodes))
	}
}

func TestGroupEpisodesTransitiveEntityChaining(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create entities: A, B, C
	db.AddEntity(&graph.Entity{ID: "entity-a", Name: "A", Type: graph.EntityPerson})
	db.AddEntity(&graph.Entity{ID: "entity-b", Name: "B", Type: graph.EntityPerson})
	db.AddEntity(&graph.Entity{ID: "entity-c", Name: "C", Type: graph.EntityPerson})

	now := time.Now()
	episodes := []*graph.Episode{
		{ID: "ep-1", Content: "A", Channel: "ch-1", TimestampEvent: now},
		{ID: "ep-2", Content: "B", Channel: "ch-2", TimestampEvent: now.Add(5 * time.Minute)},
		{ID: "ep-3", Content: "C", Channel: "ch-3", TimestampEvent: now.Add(10 * time.Minute)},
	}

	for _, ep := range episodes {
		db.AddEpisode(ep)
	}

	// Chain: ep-1 has A, ep-2 has A and B, ep-3 has B and C
	// This creates a transitive chain: ep-1 -> ep-2 -> ep-3
	db.LinkEpisodeToEntity("ep-1", "entity-a")
	db.LinkEpisodeToEntity("ep-2", "entity-a")
	db.LinkEpisodeToEntity("ep-2", "entity-b")
	db.LinkEpisodeToEntity("ep-3", "entity-b")
	db.LinkEpisodeToEntity("ep-3", "entity-c")

	c := NewConsolidator(db, &mockLLM{})
	c.TimeWindow = 30 * time.Minute

	groups := c.groupEpisodes(episodes)

	// All should be in one group via transitive entity overlap
	if len(groups) != 1 {
		t.Fatalf("Expected 1 group (transitive entity chaining), got %d", len(groups))
	}

	if len(groups[0].episodes) != 3 {
		t.Errorf("Expected 3 episodes in group, got %d", len(groups[0].episodes))
	}

	// Verify all three entities are in the group's entityIDs
	expectedEntities := []string{"entity-a", "entity-b", "entity-c"}
	for _, e := range expectedEntities {
		if !groups[0].entityIDs[e] {
			t.Errorf("Expected entity %s in group", e)
		}
	}
}

func TestIsAllLowInfo(t *testing.T) {
	tests := []struct {
		name     string
		episodes []*graph.Episode
		expected bool
	}{
		{
			name:     "empty",
			episodes: []*graph.Episode{},
			expected: true,
		},
		{
			name: "all backchannels",
			episodes: []*graph.Episode{
				{DialogueAct: "backchannel"},
				{DialogueAct: "backchannel"},
			},
			expected: true,
		},
		{
			name: "all greetings",
			episodes: []*graph.Episode{
				{DialogueAct: "greeting"},
				{DialogueAct: "greeting"},
			},
			expected: true,
		},
		{
			name: "mixed low info",
			episodes: []*graph.Episode{
				{DialogueAct: "backchannel"},
				{DialogueAct: "greeting"},
			},
			expected: true,
		},
		{
			name: "one substantive",
			episodes: []*graph.Episode{
				{DialogueAct: "backchannel"},
				{DialogueAct: "statement"},
			},
			expected: false,
		},
		{
			name: "content fallback - low info",
			episodes: []*graph.Episode{
				{Content: "ok"},
				{Content: "great"},
			},
			expected: true,
		},
		{
			name: "content fallback - substantive",
			episodes: []*graph.Episode{
				{Content: "ok"},
				{Content: "Let me explain the architecture"},
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isAllLowInfo(tc.episodes)
			if result != tc.expected {
				t.Errorf("isAllLowInfo() = %v, expected %v", result, tc.expected)
			}
		})
	}
}

func TestClassifyTraceType(t *testing.T) {
	tests := []struct {
		name     string
		summary  string
		expected graph.TraceType
	}{
		{
			name:     "meeting reminder",
			summary:  "[Past] Bud: Upcoming meeting in 10 minutes: Sprint planning",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "meeting reminder - starts soon",
			summary:  "[Past] Bud: Sprint Planning for Nightshade starts soon",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "meeting reminder - starts in with time",
			summary:  "[Past] Bud: Heads up - DevOps Sprint Planning starts in 13m37s.",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "meeting reminder - Google Meet link",
			summary:  "[Past] Bud: Upcoming DevOps Sprint Planning meeting starting soon https://meet.google.com/abc-defg-hij",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "meeting reminder - meeting starts",
			summary:  "[Past] Bud: DA Sprint Planning meeting starts in 10 minutes",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "meeting reminder - scheduled to start soon",
			summary:  "[Past] An unblock light node development meeting is scheduled to start soon; the link is provided.",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "state sync",
			summary:  "[Past] Bud: State sync completed, pushed changes",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "idle wake",
			summary:  "[Past] No actionable work found during wake",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "knowledge - decision",
			summary:  "[Past] Decided to use Redis for caching",
			expected: graph.TraceTypeKnowledge,
		},
		{
			name:     "knowledge - preference",
			summary:  "[Past] User prefers morning check-ins",
			expected: graph.TraceTypeKnowledge,
		},
		{
			name:     "knowledge - fact",
			summary:  "[Past] Sarah is the new PM for Project Alpha",
			expected: graph.TraceTypeKnowledge,
		},
		{
			name:     "knowledge - meeting discussion",
			summary:  "[Past] We discussed the sprint planning process and decided to move it to Mondays",
			expected: graph.TraceTypeKnowledge,
		},
		// Dev work patterns - operational (no decision rationale)
		{
			name:     "dev work - updated",
			summary:  "[Past] Updated Budget to use output tokens",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "dev work - implemented",
			summary:  "[Past] Implemented FOLLOWS edges between episodes",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "dev work - fixed",
			summary:  "[Past] Fixed token metrics display",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "dev work - added",
			summary:  "[Past] Added entity extraction to Bud responses",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "dev work - explored",
			summary:  "[Past] Explored WNUT 2017 NER benchmark for entity extraction",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "dev work - researched",
			summary:  "[Past] Researched spreading activation parameters",
			expected: graph.TraceTypeOperational,
		},
		{
			name:     "dev work - pruned",
			summary:  "[Past] Pruned 32 bad PRODUCT entities from the database",
			expected: graph.TraceTypeOperational,
		},
		// Dev work with knowledge indicators - should stay knowledge
		{
			name:     "dev work - with decision",
			summary:  "[Past] Updated caching layer because Redis was causing latency issues",
			expected: graph.TraceTypeKnowledge,
		},
		{
			name:     "dev work - with finding",
			summary:  "[Past] Explored entropy filter and finding was that it blocks all user messages",
			expected: graph.TraceTypeKnowledge,
		},
		{
			name:     "dev work - with root cause",
			summary:  "[Past] Fixed entity extraction bug, root cause was missing null check",
			expected: graph.TraceTypeKnowledge,
		},
		{
			name:     "dev work - with approach",
			summary:  "[Past] Implemented two-pass extraction approach for better precision",
			expected: graph.TraceTypeKnowledge,
		},
		{
			name:     "dev work - with chose",
			summary:  "[Past] Refactored auth module, chose JWT over sessions for scalability",
			expected: graph.TraceTypeKnowledge,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := classifyTraceType(tc.summary, nil)
			if result != tc.expected {
				t.Errorf("classifyTraceType(%q) = %v, expected %v", tc.summary, result, tc.expected)
			}
		})
	}
}

func TestIsEphemeralContent(t *testing.T) {
	tests := []struct {
		name     string
		summary  string
		expected bool
	}{
		{
			name:     "countdown",
			summary:  "[Past] Meeting in 5 minutes and 30 seconds",
			expected: true,
		},
		{
			name:     "starting in",
			summary:  "[Past] Starting in 10 minutes",
			expected: true,
		},
		{
			name:     "starts in",
			summary:  "[Past] Meeting starts in 15 minutes",
			expected: true,
		},
		{
			name:     "not ephemeral - decision about meeting",
			summary:  "[Past] Decided the meeting starts in the afternoon, specifically at 2pm. This is important because...",
			expected: false, // Long enough to not be ephemeral
		},
		{
			name:     "not ephemeral - normal content",
			summary:  "[Past] Discussed the new architecture for the memory system",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isEphemeralContent(tc.summary)
			if result != tc.expected {
				t.Errorf("isEphemeralContent(%q) = %v, expected %v", tc.summary, result, tc.expected)
			}
		})
	}
}

func TestCalculateCentroid(t *testing.T) {
	episodes := []*graph.Episode{
		{ID: "ep-1", Embedding: []float64{1.0, 0.0, 0.0}},
		{ID: "ep-2", Embedding: []float64{0.0, 1.0, 0.0}},
		{ID: "ep-3", Embedding: []float64{0.0, 0.0, 1.0}},
	}

	centroid := calculateCentroid(episodes)

	if len(centroid) != 3 {
		t.Fatalf("Expected centroid of length 3, got %d", len(centroid))
	}

	// Centroid should be (1/3, 1/3, 1/3)
	expected := 1.0 / 3.0
	tolerance := 0.001
	for i, v := range centroid {
		if v < expected-tolerance || v > expected+tolerance {
			t.Errorf("centroid[%d] = %f, expected ~%f", i, v, expected)
		}
	}
}

func TestCalculateCentroidEmpty(t *testing.T) {
	centroid := calculateCentroid(nil)
	if centroid != nil {
		t.Error("Expected nil for empty input")
	}

	centroid = calculateCentroid([]*graph.Episode{})
	if centroid != nil {
		t.Error("Expected nil for empty slice")
	}
}

func TestCalculateCentroidNoEmbeddings(t *testing.T) {
	episodes := []*graph.Episode{
		{ID: "ep-1", Embedding: nil},
		{ID: "ep-2", Embedding: []float64{}},
	}

	centroid := calculateCentroid(episodes)
	if centroid != nil {
		t.Error("Expected nil when no episodes have embeddings")
	}
}
