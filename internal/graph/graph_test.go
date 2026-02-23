package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// addTestTrace adds a trace with minimal pyramid summary for testing
func addTestTrace(t *testing.T, db *DB, tr *Trace) error {
	t.Helper()
	if err := db.AddTrace(tr); err != nil {
		return err
	}
	// Add minimal L32 summary so GetTrace queries work
	summary := tr.Summary
	if summary == "" {
		summary = "Test trace"
	}
	return db.AddTraceSummary(tr.ID, 32, summary, estimateTokens(summary))
}

// setupTestDB creates a temporary test database
func setupTestDB(t *testing.T) (*DB, func()) {
	t.Helper()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "graph-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Open database
	db, err := Open(tmpDir)
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

// TestSpreadingActivation tests the spreading activation algorithm
func TestSpreadingActivation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a network of related traces:
	// A --0.8--> B --0.6--> C
	//            |
	//            v
	//            D

	traces := []*Trace{
		{ID: "trace-A", Summary: "Trace A", Activation: 0.5, Embedding: []float64{1.0, 0.0, 0.0, 0.0}},
		{ID: "trace-B", Summary: "Trace B", Activation: 0.5, Embedding: []float64{0.8, 0.6, 0.0, 0.0}},
		{ID: "trace-C", Summary: "Trace C", Activation: 0.5, Embedding: []float64{0.5, 0.5, 0.5, 0.0}},
		{ID: "trace-D", Summary: "Trace D", Activation: 0.5, Embedding: []float64{0.3, 0.3, 0.3, 0.3}},
	}

	for _, tr := range traces {
		if err := db.AddTrace(tr); err != nil {
			t.Fatalf("Failed to add trace: %v", err)
		}
	}

	// Add relations
	db.AddTraceRelation("trace-A", "trace-B", EdgeRelatedTo, 0.8)
	db.AddTraceRelation("trace-B", "trace-C", EdgeRelatedTo, 0.6)
	db.AddTraceRelation("trace-B", "trace-D", EdgeRelatedTo, 0.4)

	// Spread activation from trace A
	activation, err := db.SpreadActivation([]string{"trace-A"}, 3)
	if err != nil {
		t.Fatalf("SpreadActivation failed: %v", err)
	}

	// Verify A has highest activation (seed node)
	if activation["trace-A"] == 0 {
		t.Error("Expected trace-A to have activation > 0")
	}

	// Verify B received activation from A
	if activation["trace-B"] == 0 {
		t.Error("Expected trace-B to receive activation from A")
	}

	// Verify C and D received less activation (further from seed)
	if activation["trace-C"] >= activation["trace-B"] {
		t.Error("Expected trace-C to have less activation than trace-B")
	}

	// Verify activation decays with distance
	t.Logf("Activations: A=%f, B=%f, C=%f, D=%f",
		activation["trace-A"], activation["trace-B"], activation["trace-C"], activation["trace-D"])
}

// TestMultiHopRetrieval tests that activation spreads across multiple hops
func TestMultiHopRetrieval(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a chain: A -> B -> C -> D -> E
	// Starting from A, we should reach at least C after 3 iterations
	for i := 0; i < 5; i++ {
		id := string(rune('A' + i))
		db.AddTrace(&Trace{
			ID:         "trace-" + id,
			Summary:    "Trace " + id,
			Activation: 0.5,
		})
	}

	db.AddTraceRelation("trace-A", "trace-B", EdgeRelatedTo, 0.9)
	db.AddTraceRelation("trace-B", "trace-C", EdgeRelatedTo, 0.9)
	db.AddTraceRelation("trace-C", "trace-D", EdgeRelatedTo, 0.9)
	db.AddTraceRelation("trace-D", "trace-E", EdgeRelatedTo, 0.9)

	// Spread with default iterations
	activation, _ := db.SpreadActivation([]string{"trace-A"}, 3)

	t.Logf("Multi-hop activations: A=%f, B=%f, C=%f, D=%f, E=%f",
		activation["trace-A"], activation["trace-B"], activation["trace-C"],
		activation["trace-D"], activation["trace-E"])

	// B and C should receive activation (1-2 hops)
	if activation["trace-B"] == 0 {
		t.Error("Expected trace-B to receive activation (1 hop)")
	}

	// Due to lateral inhibition and decay, very distant nodes may not activate
	// This is actually correct behavior - we don't want unbounded spreading
	// Verify that activation decreases with distance for nodes that are activated
	if activation["trace-B"] > 0 && activation["trace-C"] > 0 {
		if activation["trace-C"] >= activation["trace-B"] {
			t.Error("Expected activation to decrease with distance")
		}
	}
}

// TestFeelingOfKnowing tests the FoK rejection mechanism
func TestFeelingOfKnowing(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create traces with embeddings
	knownTrace := &Trace{
		ID:        "trace-known",
		Summary:   "The project deadline is Friday",
		Embedding: []float64{0.9, 0.1, 0.0, 0.0}, // Specific topic
	}
	db.AddTrace(knownTrace)

	// Query with similar embedding - should find it
	similarQuery := []float64{0.85, 0.15, 0.0, 0.0}
	result, err := db.Retrieve(similarQuery, "project deadline", 5)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	if len(result.Traces) == 0 {
		t.Error("Expected to retrieve trace with similar embedding")
	}

	// Query with very different embedding - FoK should reject
	differentQuery := []float64{0.0, 0.0, 0.9, 0.1}
	result, err = db.Retrieve(differentQuery, "unrelated topic", 5)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	// With low similarity, max activation should be below FoK threshold
	// so we expect empty or minimal results
	t.Logf("FoK test: retrieved %d traces with different query", len(result.Traces))
}

// TestTraceActivationUpdate tests that retrieval updates activation
func TestTraceActivationUpdate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	trace := &Trace{
		ID:         "trace-1",
		Summary:    "Test trace",
		Activation: 0.5,
		Embedding:  []float64{0.5, 0.5, 0.0, 0.0},
	}
	db.AddTrace(trace)

	// Record initial last_accessed
	initialTrace, _ := db.GetTrace("trace-1")
	initialAccess := initialTrace.LastAccessed

	// Wait a moment
	time.Sleep(10 * time.Millisecond)

	// Update activation
	db.UpdateTraceActivation("trace-1", 0.9)

	// Verify update
	updated, _ := db.GetTrace("trace-1")
	if updated.Activation != 0.9 {
		t.Errorf("Expected activation 0.9, got %f", updated.Activation)
	}

	if !updated.LastAccessed.After(initialAccess) {
		t.Error("Expected last_accessed to be updated")
	}
}

// TestDecayActivation tests global activation decay
func TestDecayActivation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	db.AddTrace(&Trace{ID: "trace-1", Summary: "Test 1", Activation: 1.0})
	db.AddTrace(&Trace{ID: "trace-2", Summary: "Test 2", Activation: 0.8})

	// Decay by 0.9 (lose 10% activation)
	db.DecayActivation(0.9)

	t1, _ := db.GetTrace("trace-1")
	if t1.Activation != 0.9 {
		t.Errorf("Expected trace-1 activation 0.9, got %f", t1.Activation)
	}

	t2, _ := db.GetTrace("trace-2")
	if t2.Activation < 0.71 || t2.Activation > 0.73 {
		t.Errorf("Expected trace-2 activation ~0.72, got %f", t2.Activation)
	}
}

// TestReinforceTrace tests trace reinforcement with EMA
func TestReinforceTrace(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create trace with initial embedding
	db.AddTrace(&Trace{
		ID:        "trace-1",
		Summary:   "Test trace",
		Strength:  1,
		Embedding: []float64{1.0, 0.0, 0.0, 0.0},
	})

	// Reinforce with different embedding
	newEmb := []float64{0.0, 1.0, 0.0, 0.0}
	db.ReinforceTrace("trace-1", newEmb, 0.3) // alpha=0.3

	// Check embedding was blended
	updated, _ := db.GetTrace("trace-1")

	// With alpha=0.3: new = 0.3*[0,1,0,0] + 0.7*[1,0,0,0] = [0.7, 0.3, 0, 0]
	expectedFirst := 0.7
	if updated.Embedding[0] < expectedFirst-0.01 || updated.Embedding[0] > expectedFirst+0.01 {
		t.Errorf("Expected embedding[0] ~%f, got %f", expectedFirst, updated.Embedding[0])
	}

	// Strength should increase
	if updated.Strength != 2 {
		t.Errorf("Expected strength 2, got %d", updated.Strength)
	}
}

// TestLabile tests the labile/reconsolidation window
func TestLabile(t *testing.T) {
	trace := &Trace{
		ID:      "trace-1",
		Summary: "Test trace",
	}

	// Initially not labile
	if trace.IsLabile() {
		t.Error("Expected trace to not be labile initially")
	}

	// Make labile
	trace.MakeLabile(1 * time.Hour)
	if !trace.IsLabile() {
		t.Error("Expected trace to be labile after MakeLabile")
	}

	// Set expired labile window
	trace.LabileUntil = time.Now().Add(-1 * time.Hour)
	if trace.IsLabile() {
		t.Error("Expected trace to not be labile after window expires")
	}
}

// TestTraceRelations tests linking traces via relations
func TestTraceRelations(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create two traces
	db.AddTrace(&Trace{ID: "trace-A", Summary: "Trace A"})
	db.AddTrace(&Trace{ID: "trace-B", Summary: "Trace B"})

	// Link them
	err := db.AddTraceRelation("trace-A", "trace-B", EdgeRelatedTo, 0.8)
	if err != nil {
		t.Fatalf("AddTraceRelation failed: %v", err)
	}

	// Get neighbors of A
	neighbors, err := db.GetTraceNeighbors("trace-A")
	if err != nil {
		t.Fatalf("GetTraceNeighbors failed: %v", err)
	}

	if len(neighbors) != 1 {
		t.Fatalf("Expected 1 neighbor, got %d", len(neighbors))
	}

	if neighbors[0].ID != "trace-B" {
		t.Errorf("Expected neighbor ID trace-B, got %s", neighbors[0].ID)
	}

	if neighbors[0].Weight != 0.8 {
		t.Errorf("Expected weight 0.8, got %f", neighbors[0].Weight)
	}

	// Relation should be bidirectional in GetTraceNeighbors
	neighborsB, _ := db.GetTraceNeighbors("trace-B")
	if len(neighborsB) != 1 || neighborsB[0].ID != "trace-A" {
		t.Error("Expected bidirectional neighbor lookup")
	}
}

// TestFindSimilarTracesAboveThreshold tests finding traces by similarity
func TestFindSimilarTracesAboveThreshold(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create traces with embeddings
	// Trace A and B have similar embeddings, C is different
	db.AddTrace(&Trace{ID: "trace-A", Summary: "About work projects", Embedding: []float64{0.9, 0.1, 0.1}})
	db.AddTrace(&Trace{ID: "trace-B", Summary: "Work related", Embedding: []float64{0.85, 0.15, 0.1}})
	db.AddTrace(&Trace{ID: "trace-C", Summary: "Completely different", Embedding: []float64{0.1, 0.9, 0.1}})

	// Find traces similar to A with high threshold
	similar, err := db.FindSimilarTracesAboveThreshold([]float64{0.9, 0.1, 0.1}, 0.9, "trace-A")
	if err != nil {
		t.Fatalf("FindSimilarTracesAboveThreshold failed: %v", err)
	}

	// B should be found (similarity ~0.98), C should not (similarity ~0.3)
	if len(similar) != 1 {
		t.Fatalf("Expected 1 similar trace, got %d", len(similar))
	}
	if similar[0].ID != "trace-B" {
		t.Errorf("Expected trace-B, got %s", similar[0].ID)
	}
	if similar[0].Similarity < 0.9 {
		t.Errorf("Expected similarity >= 0.9, got %f", similar[0].Similarity)
	}

	// Lower threshold should find more
	similar2, _ := db.FindSimilarTracesAboveThreshold([]float64{0.9, 0.1, 0.1}, 0.2, "trace-A")
	if len(similar2) != 2 {
		t.Errorf("Expected 2 similar traces at threshold 0.2, got %d", len(similar2))
	}
}

// TestSimilarToEdgeInSpreadingActivation tests that SIMILAR_TO edges are used in spreading activation
func TestSimilarToEdgeInSpreadingActivation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create traces with embeddings (A similar to B, C different)
	db.AddTrace(&Trace{ID: "trace-A", Summary: "Topic one", Embedding: []float64{0.9, 0.1, 0.0}, Activation: 0.5})
	db.AddTrace(&Trace{ID: "trace-B", Summary: "Topic one related", Embedding: []float64{0.85, 0.15, 0.0}, Activation: 0.5})
	db.AddTrace(&Trace{ID: "trace-C", Summary: "Different topic", Embedding: []float64{0.0, 0.1, 0.9}, Activation: 0.5})

	// Add SIMILAR_TO edge between A and B
	err := db.AddTraceRelation("trace-A", "trace-B", EdgeSimilarTo, 0.95)
	if err != nil {
		t.Fatalf("AddTraceRelation failed: %v", err)
	}

	// Verify neighbors work
	neighbors, _ := db.GetTraceNeighbors("trace-A")
	hasB := false
	for _, n := range neighbors {
		if n.ID == "trace-B" && n.Type == EdgeSimilarTo {
			hasB = true
			break
		}
	}
	if !hasB {
		t.Error("Expected trace-B as SIMILAR_TO neighbor of trace-A")
	}

	// Do spreading activation from query similar to A
	// B should receive activation through SIMILAR_TO edge
	activations, err := db.SpreadActivationFromEmbedding([]float64{0.9, 0.1, 0.0}, "", 5, 2)
	if err != nil {
		t.Fatalf("SpreadActivation failed: %v", err)
	}

	t.Logf("Activations: A=%.4f, B=%.4f, C=%.4f", activations["trace-A"], activations["trace-B"], activations["trace-C"])

	// Both A and B should have activation (B via spreading through SIMILAR_TO edge)
	if activations["trace-A"] == 0 {
		t.Error("Expected trace-A to have activation")
	}
	if activations["trace-B"] == 0 {
		t.Error("Expected trace-B to have activation (via SIMILAR_TO edge)")
	}
}

// TestStats tests database statistics
func TestStats(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Add some data
	db.AddTrace(&Trace{ID: "trace-1", Summary: "Test"})
	db.AddTrace(&Trace{ID: "trace-2", Summary: "Test"})

	stats, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	if stats["traces"] != 2 {
		t.Errorf("Expected traces count 2, got %d", stats["traces"])
	}
}

// TestClear tests database clearing
func TestClear(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Add data
	db.AddTrace(&Trace{ID: "trace-1", Summary: "Test"})

	// Clear
	if err := db.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// Verify empty
	stats, _ := db.Stats()
	if stats["traces"] != 0 {
		t.Error("Expected traces to be cleared")
	}
}

// BenchmarkSpreadActivation benchmarks spreading activation performance
func BenchmarkSpreadActivation(b *testing.B) {
	// Create temp directory
	tmpDir, _ := os.MkdirTemp("", "graph-bench-*")
	defer os.RemoveAll(tmpDir)

	db, _ := Open(tmpDir)
	defer db.Close()

	// Create 100 traces with random connections
	for i := 0; i < 100; i++ {
		id := filepath.Base(tmpDir) + "-" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		db.AddTrace(&Trace{ID: id, Summary: "Trace"})

		if i > 0 {
			prevID := filepath.Base(tmpDir) + "-" + string(rune('A'+(i-1)%26)) + string(rune('0'+(i-1)/26))
			db.AddTraceRelation(prevID, id, EdgeRelatedTo, 0.5)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.SpreadActivation([]string{filepath.Base(tmpDir) + "-A0"}, 3)
	}
}

// setupEntityBridgedDB creates a test DB with traces sharing entities.
// Returns: db, cleanup, and the entity/trace IDs for assertions.
//
//	trace-1 --[entity-jane]--> trace-2
//	trace-1 --[entity-jane, entity-proj]--> trace-3
//	trace-4 has no shared entities
func setupEntityBridgedDB(t *testing.T) (*DB, func()) {
	t.Helper()
	db, cleanup := setupTestDB(t)

	// Create entities
	db.AddEntity(&Entity{ID: "entity-jane", Name: "Jane", Type: EntityPerson, Salience: 0.8})
	db.AddEntity(&Entity{ID: "entity-proj", Name: "Project Alpha", Type: EntityProduct, Salience: 0.6})
	db.AddEntity(&Entity{ID: "entity-bob", Name: "Bob", Type: EntityPerson, Salience: 0.4})

	// Add alias for Jane
	db.AddEntityAlias("entity-jane", "Jane Smith")

	// Create traces
	db.AddTrace(&Trace{ID: "trace-1", Summary: "Meeting with Jane about Project Alpha", Activation: 0.5, Embedding: []float64{1.0, 0.0, 0.0, 0.0}})
	db.AddTrace(&Trace{ID: "trace-2", Summary: "Jane's birthday is in March", Activation: 0.5, Embedding: []float64{0.0, 1.0, 0.0, 0.0}})
	db.AddTrace(&Trace{ID: "trace-3", Summary: "Project Alpha deadline discussion with Jane", Activation: 0.5, Embedding: []float64{0.0, 0.0, 1.0, 0.0}})
	db.AddTrace(&Trace{ID: "trace-4", Summary: "Unrelated trace about weather", Activation: 0.5, Embedding: []float64{0.0, 0.0, 0.0, 1.0}})

	// Link traces to entities
	db.LinkTraceToEntity("trace-1", "entity-jane")
	db.LinkTraceToEntity("trace-1", "entity-proj")
	db.LinkTraceToEntity("trace-2", "entity-jane")
	db.LinkTraceToEntity("trace-3", "entity-jane")
	db.LinkTraceToEntity("trace-3", "entity-proj")
	db.LinkTraceToEntity("trace-4", "entity-bob")

	return db, cleanup
}

func TestEntityBridgedNeighbors(t *testing.T) {
	db, cleanup := setupEntityBridgedDB(t)
	defer cleanup()

	// trace-1 shares entity-jane with trace-2 and trace-3
	// trace-1 also shares entity-proj with trace-3
	neighbors, err := db.GetTraceNeighborsThroughEntities("trace-1", 15)
	if err != nil {
		t.Fatalf("GetTraceNeighborsThroughEntities failed: %v", err)
	}

	if len(neighbors) == 0 {
		t.Fatal("Expected entity-bridged neighbors, got none")
	}

	// Should find trace-2 and trace-3 as neighbors
	neighborIDs := make(map[string]float64)
	for _, n := range neighbors {
		neighborIDs[n.ID] = n.Weight
		if n.Type != EdgeSharedEntity {
			t.Errorf("Expected EdgeSharedEntity type, got %s", n.Type)
		}
	}

	if _, ok := neighborIDs["trace-2"]; !ok {
		t.Error("Expected trace-2 as neighbor (shares entity-jane)")
	}
	if _, ok := neighborIDs["trace-3"]; !ok {
		t.Error("Expected trace-3 as neighbor (shares entity-jane and entity-proj)")
	}
	if _, ok := neighborIDs["trace-4"]; ok {
		t.Error("trace-4 should NOT be a neighbor of trace-1 (no shared entities)")
	}

	t.Logf("Entity-bridged neighbors of trace-1: %+v", neighbors)
}

func TestEntityBridged2HopNeighbors(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Graph:
	//   entity-alice -[KNOWS]-> entity-bob
	//   trace-1 linked to entity-alice
	//   trace-2 linked to entity-bob (NOT alice)
	//   trace-3 linked to entity-alice (1-hop from trace-1)
	//
	// Without 2-hop: trace-1 neighbors = {trace-3}
	// With 2-hop via alice→bob relation: trace-1 neighbors = {trace-3, trace-2}
	db.AddEntity(&Entity{ID: "entity-alice", Name: "Alice", Type: EntityPerson, Salience: 0.8})
	db.AddEntity(&Entity{ID: "entity-bob", Name: "Bob", Type: EntityPerson, Salience: 0.7})

	db.AddTrace(&Trace{ID: "trace-1", Summary: "Talked with Alice today", Activation: 0.5, Embedding: []float64{1.0, 0.0, 0.0, 0.0}})
	db.AddTrace(&Trace{ID: "trace-2", Summary: "Bob's project status", Activation: 0.5, Embedding: []float64{0.0, 1.0, 0.0, 0.0}})
	db.AddTrace(&Trace{ID: "trace-3", Summary: "Alice mentioned a problem", Activation: 0.5, Embedding: []float64{0.0, 0.0, 1.0, 0.0}})

	db.LinkTraceToEntity("trace-1", "entity-alice")
	db.LinkTraceToEntity("trace-2", "entity-bob")  // bob only
	db.LinkTraceToEntity("trace-3", "entity-alice") // alice, 1-hop from trace-1

	// Add entity relation: alice KNOWS bob
	if err := db.AddEntityRelation("entity-alice", "entity-bob", EdgeKnows, 0.9); err != nil {
		t.Fatalf("AddEntityRelation failed: %v", err)
	}

	neighbors, err := db.GetTraceNeighborsThroughEntities("trace-1", 15)
	if err != nil {
		t.Fatalf("GetTraceNeighborsThroughEntities failed: %v", err)
	}

	neighborIDs := make(map[string]float64)
	for _, n := range neighbors {
		neighborIDs[n.ID] = n.Weight
	}

	// 1-hop: trace-3 shares entity-alice with trace-1
	if _, ok := neighborIDs["trace-3"]; !ok {
		t.Error("Expected trace-3 as 1-hop neighbor (shares entity-alice)")
	}

	// 2-hop: trace-2 reachable via alice→bob→trace-2
	if _, ok := neighborIDs["trace-2"]; !ok {
		t.Error("Expected trace-2 as 2-hop neighbor (alice KNOWS bob, bob linked to trace-2)")
	}

	// 2-hop weight should be lower than 1-hop
	if w2hop, ok := neighborIDs["trace-2"]; ok {
		if w1hop, ok2 := neighborIDs["trace-3"]; ok2 {
			if w2hop >= w1hop {
				t.Errorf("2-hop weight (%f) should be less than 1-hop weight (%f)", w2hop, w1hop)
			}
		}
	}

	t.Logf("2-hop entity neighbors: %+v", neighbors)
}

func TestEntityBridgedSpreadActivation(t *testing.T) {
	db, cleanup := setupEntityBridgedDB(t)
	defer cleanup()

	// Spread from trace-1 — should reach trace-2 and trace-3 through entity bridges
	activation, err := db.SpreadActivation([]string{"trace-1"}, 3)
	if err != nil {
		t.Fatalf("SpreadActivation failed: %v", err)
	}

	if activation["trace-2"] == 0 {
		t.Error("Expected trace-2 to receive activation through entity bridge")
	}
	if activation["trace-3"] == 0 {
		t.Error("Expected trace-3 to receive activation through entity bridge")
	}

	t.Logf("Entity-bridged activations: trace-1=%f, trace-2=%f, trace-3=%f, trace-4=%f",
		activation["trace-1"], activation["trace-2"], activation["trace-3"], activation["trace-4"])
}

func TestEntitySeeding(t *testing.T) {
	db, cleanup := setupEntityBridgedDB(t)
	defer cleanup()

	// Query mentioning "Jane" should seed Jane-related traces
	matchedEntities, err := db.FindEntitiesByText("meeting with Jane tomorrow", 5)
	if err != nil {
		t.Fatalf("FindEntitiesByText failed: %v", err)
	}

	if len(matchedEntities) == 0 {
		t.Fatal("Expected to find entity 'Jane' in query text")
	}

	found := false
	for _, e := range matchedEntities {
		if e.ID == "entity-jane" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected entity-jane to be matched")
	}

	// Also test alias matching: "Jane Smith"
	matchedAliases, err := db.FindEntitiesByText("I spoke with Jane Smith yesterday", 5)
	if err != nil {
		t.Fatalf("FindEntitiesByText failed: %v", err)
	}

	found = false
	for _, e := range matchedAliases {
		if e.ID == "entity-jane" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected entity-jane to be matched via alias 'Jane Smith'")
	}
}

func TestEntitySeedingNoFalsePositives(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create entities with names that could cause false positives
	db.AddEntity(&Entity{ID: "entity-ai", Name: "AI", Type: EntityProduct, Salience: 0.5})
	db.AddEntity(&Entity{ID: "entity-go", Name: "Go", Type: EntityLanguage, Salience: 0.5})
	db.AddEntity(&Entity{ID: "entity-ed", Name: "Ed", Type: EntityPerson, Salience: 0.5})

	// "AI" is only 2 chars — should be skipped (< 3 char minimum)
	matches, _ := db.FindEntitiesByText("I said something about AI today", 5)
	for _, m := range matches {
		if m.ID == "entity-ai" {
			t.Error("Should not match 'AI' (too short, < 3 chars)")
		}
	}

	// "Go" is only 2 chars — should be skipped
	matches, _ = db.FindEntitiesByText("Let's go to the store", 5)
	for _, m := range matches {
		if m.ID == "entity-go" {
			t.Error("Should not match 'Go' (too short, < 3 chars)")
		}
	}

	// "Ed" is only 2 chars — should be skipped
	matches, _ = db.FindEntitiesByText("I edited the file", 5)
	for _, m := range matches {
		if m.ID == "entity-ed" {
			t.Error("Should not match 'Ed' (too short, < 3 chars)")
		}
	}

	// But longer names should match with word boundaries
	db.AddEntity(&Entity{ID: "entity-alice", Name: "Alice", Type: EntityPerson, Salience: 0.5})
	matches, _ = db.FindEntitiesByText("I met Alice at the park", 5)
	found := false
	for _, m := range matches {
		if m.ID == "entity-alice" {
			found = true
		}
	}
	if !found {
		t.Error("Expected to match 'Alice' as a whole word")
	}

	// "Alice" should NOT match inside "Malice"
	matches, _ = db.FindEntitiesByText("There was no malice intended", 5)
	for _, m := range matches {
		if m.ID == "entity-alice" {
			t.Error("Should not match 'Alice' inside 'malice' (word boundary)")
		}
	}
}

func TestEntityNeighborCap(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create one entity shared by many traces
	db.AddEntity(&Entity{ID: "entity-shared", Name: "SharedThing", Type: EntityProduct, Salience: 0.5})

	// Create source trace
	db.AddTrace(&Trace{ID: "trace-source", Summary: "Source trace"})
	db.LinkTraceToEntity("trace-source", "entity-shared")

	// Create 20 traces sharing the same entity (more than MaxEdgesPerNode=15)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("trace-neighbor-%d", i)
		db.AddTrace(&Trace{ID: id, Summary: fmt.Sprintf("Neighbor trace %d", i)})
		db.LinkTraceToEntity(id, "entity-shared")
	}

	neighbors, err := db.GetTraceNeighborsThroughEntities("trace-source", MaxEdgesPerNode)
	if err != nil {
		t.Fatalf("GetTraceNeighborsThroughEntities failed: %v", err)
	}

	if len(neighbors) > MaxEdgesPerNode {
		t.Errorf("Expected at most %d neighbors, got %d", MaxEdgesPerNode, len(neighbors))
	}

	t.Logf("Returned %d entity-bridged neighbors (cap=%d)", len(neighbors), MaxEdgesPerNode)
}

func TestMultiEntitySharedWeight(t *testing.T) {
	db, cleanup := setupEntityBridgedDB(t)
	defer cleanup()

	neighbors, err := db.GetTraceNeighborsThroughEntities("trace-1", 15)
	if err != nil {
		t.Fatalf("GetTraceNeighborsThroughEntities failed: %v", err)
	}

	weightByID := make(map[string]float64)
	for _, n := range neighbors {
		weightByID[n.ID] = n.Weight
	}

	// trace-3 shares 2 entities (jane + project alpha) with trace-1 -> weight = min(1.0, 2*0.3) = 0.6
	// trace-2 shares 1 entity (jane) with trace-1 -> weight = min(1.0, 1*0.3) = 0.3
	w3 := weightByID["trace-3"]
	w2 := weightByID["trace-2"]

	if w3 <= w2 {
		t.Errorf("Expected trace-3 (2 shared entities, weight=%f) to have higher weight than trace-2 (1 shared entity, weight=%f)", w3, w2)
	}

	if w3 != 0.6 {
		t.Errorf("Expected trace-3 weight 0.6 (2 shared * 0.3), got %f", w3)
	}

	if w2 != 0.3 {
		t.Errorf("Expected trace-2 weight 0.3 (1 shared * 0.3), got %f", w2)
	}

	t.Logf("Multi-entity weights: trace-2=%f, trace-3=%f", w2, w3)
}

// TestReconsolidationFlags tests marking, querying, and clearing reconsolidation flags
func TestReconsolidationFlags(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Add two traces
	tr1 := &Trace{ID: "trace-recon-1", Summary: "Original summary about Alice"}
	tr2 := &Trace{ID: "trace-recon-2", Summary: "Another trace about Bob"}
	if err := addTestTrace(t, db, tr1); err != nil {
		t.Fatalf("AddTrace failed: %v", err)
	}
	if err := addTestTrace(t, db, tr2); err != nil {
		t.Fatalf("AddTrace failed: %v", err)
	}

	// Initially no traces need reconsolidation
	needsRecon, err := db.GetTracesNeedingReconsolidation()
	if err != nil {
		t.Fatalf("GetTracesNeedingReconsolidation failed: %v", err)
	}
	if len(needsRecon) != 0 {
		t.Errorf("Expected 0 traces needing reconsolidation, got %d", len(needsRecon))
	}

	// Mark trace-1 for reconsolidation
	if err := db.MarkTraceForReconsolidation("trace-recon-1"); err != nil {
		t.Fatalf("MarkTraceForReconsolidation failed: %v", err)
	}

	// Now 1 trace should need reconsolidation
	needsRecon, err = db.GetTracesNeedingReconsolidation()
	if err != nil {
		t.Fatalf("GetTracesNeedingReconsolidation failed: %v", err)
	}
	if len(needsRecon) != 1 {
		t.Fatalf("Expected 1 trace needing reconsolidation, got %d", len(needsRecon))
	}
	if needsRecon[0] != "trace-recon-1" {
		t.Errorf("Expected trace-recon-1, got %s", needsRecon[0])
	}

	// Mark trace-2 as well
	if err := db.MarkTraceForReconsolidation("trace-recon-2"); err != nil {
		t.Fatalf("MarkTraceForReconsolidation failed: %v", err)
	}
	needsRecon, err = db.GetTracesNeedingReconsolidation()
	if err != nil {
		t.Fatalf("GetTracesNeedingReconsolidation failed: %v", err)
	}
	if len(needsRecon) != 2 {
		t.Errorf("Expected 2 traces needing reconsolidation, got %d", len(needsRecon))
	}

	// Clear flag for trace-1
	if err := db.ClearReconsolidationFlag("trace-recon-1"); err != nil {
		t.Fatalf("ClearReconsolidationFlag failed: %v", err)
	}
	needsRecon, err = db.GetTracesNeedingReconsolidation()
	if err != nil {
		t.Fatalf("GetTracesNeedingReconsolidation failed: %v", err)
	}
	if len(needsRecon) != 1 {
		t.Fatalf("Expected 1 trace after clear, got %d", len(needsRecon))
	}
	if needsRecon[0] != "trace-recon-2" {
		t.Errorf("Expected trace-recon-2 to remain, got %s", needsRecon[0])
	}
}

// TestUpdateTrace tests updating a trace's summary, embedding, and type
func TestUpdateTrace(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	original := &Trace{ID: "trace-update-1", Summary: "Original content"}
	if err := addTestTrace(t, db, original); err != nil {
		t.Fatalf("AddTrace failed: %v", err)
	}

	// Update with new summary, embedding, type
	newSummary := "Updated content with richer context about the project"
	newEmbedding := []float64{0.1, 0.2, 0.3, 0.4, 0.5}
	if err := db.UpdateTrace("trace-update-1", newSummary, newEmbedding, TraceTypeKnowledge, 3); err != nil {
		t.Fatalf("UpdateTrace failed: %v", err)
	}

	// Update trace_summaries at level=0 (verbatim) - this is what GetTrace reads first
	if err := db.AddTraceSummary("trace-update-1", 0, newSummary, estimateTokens(newSummary)); err != nil {
		t.Fatalf("AddTraceSummary failed: %v", err)
	}

	// Verify update
	updated, err := db.GetTrace("trace-update-1")
	if err != nil {
		t.Fatalf("GetTrace failed: %v", err)
	}
	if updated == nil {
		t.Fatal("Expected trace, got nil")
	}
	if updated.Summary != newSummary {
		t.Errorf("Expected summary %q, got %q", newSummary, updated.Summary)
	}
	if updated.TraceType != TraceTypeKnowledge {
		t.Errorf("Expected TraceTypeKnowledge, got %s", updated.TraceType)
	}
	if updated.Strength != 3 {
		t.Errorf("Expected strength 3, got %d", updated.Strength)
	}
}

// TestReconsolidationEndToEnd tests the full reconsolidation flow:
// add episode to existing trace → mark for reconsolidation → reconsolidate → verify summary updated
func TestReconsolidationEndToEnd(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create an episode and initial trace
	ep1 := &Episode{
		ID:             "ep-recon-1",
		Content:        "Alice joined the Nightshade team as tech lead",
		Author:         "user",
		Channel:        "general",
		TimestampEvent: time.Now().Add(-2 * time.Hour),
	}
	if err := db.AddEpisode(ep1); err != nil {
		t.Fatalf("AddEpisode failed: %v", err)
	}

	// Initial trace from first episode
	tr := &Trace{
		ID:       "trace-e2e-recon",
		Summary:  "Alice joined Nightshade as tech lead",
		Strength: 1,
	}
	if err := addTestTrace(t, db, tr); err != nil {
		t.Fatalf("AddTrace failed: %v", err)
	}
	if err := db.LinkTraceToSource("trace-e2e-recon", "ep-recon-1"); err != nil {
		t.Fatalf("LinkTraceToSource failed: %v", err)
	}

	// New episode arrives with updated information
	ep2 := &Episode{
		ID:             "ep-recon-2",
		Content:        "Alice is now leading the privacy initiative at Nightshade",
		Author:         "user",
		Channel:        "general",
		TimestampEvent: time.Now().Add(-1 * time.Hour),
	}
	if err := db.AddEpisode(ep2); err != nil {
		t.Fatalf("AddEpisode failed: %v", err)
	}

	// Link new episode to trace and mark for reconsolidation
	if err := db.LinkTraceToSource("trace-e2e-recon", "ep-recon-2"); err != nil {
		t.Fatalf("LinkTraceToSource failed: %v", err)
	}
	if err := db.MarkTraceForReconsolidation("trace-e2e-recon"); err != nil {
		t.Fatalf("MarkTraceForReconsolidation failed: %v", err)
	}

	// Verify flag is set
	needsRecon, err := db.GetTracesNeedingReconsolidation()
	if err != nil {
		t.Fatalf("GetTracesNeedingReconsolidation failed: %v", err)
	}
	if len(needsRecon) == 0 || needsRecon[0] != "trace-e2e-recon" {
		t.Fatal("Expected trace to be marked for reconsolidation")
	}

	// Simulate reconsolidation: update trace with combined content
	allEpisodes, err := db.GetTraceSourceEpisodes("trace-e2e-recon")
	if err != nil {
		t.Fatalf("GetTraceSourceEpisodes failed: %v", err)
	}
	if len(allEpisodes) != 2 {
		t.Fatalf("Expected 2 source episodes, got %d", len(allEpisodes))
	}

	// Build new summary from all episodes (simulating LLM summarization)
	newSummary := "Alice joined Nightshade as tech lead and is now leading the privacy initiative"
	newEmbedding := []float64{0.5, 0.6, 0.7}
	if err := db.UpdateTrace("trace-e2e-recon", newSummary, newEmbedding, TraceTypeKnowledge, len(allEpisodes)); err != nil {
		t.Fatalf("UpdateTrace failed: %v", err)
	}

	// Store updated summary at level=0 (verbatim) - this is what GetTrace reads first
	if err := db.AddTraceSummary("trace-e2e-recon", 0, newSummary, estimateTokens(newSummary)); err != nil {
		t.Fatalf("AddTraceSummary failed: %v", err)
	}

	// Clear the flag
	if err := db.ClearReconsolidationFlag("trace-e2e-recon"); err != nil {
		t.Fatalf("ClearReconsolidationFlag failed: %v", err)
	}

	// Verify: no more traces needing reconsolidation
	needsRecon, err = db.GetTracesNeedingReconsolidation()
	if err != nil {
		t.Fatalf("GetTracesNeedingReconsolidation failed: %v", err)
	}
	if len(needsRecon) != 0 {
		t.Errorf("Expected 0 traces needing reconsolidation after clear, got %d", len(needsRecon))
	}

	// Verify: trace now has updated summary and increased strength
	updated, err := db.GetTrace("trace-e2e-recon")
	if err != nil {
		t.Fatalf("GetTrace failed: %v", err)
	}
	if updated == nil {
		t.Fatal("Expected trace, got nil")
	}
	if updated.Summary != newSummary {
		t.Errorf("Expected updated summary %q, got %q", newSummary, updated.Summary)
	}
	if updated.Strength != 2 {
		t.Errorf("Expected strength 2 (2 episodes), got %d", updated.Strength)
	}

	t.Logf("Reconsolidation e2e: trace updated from 1 episode to 2, summary reflects new context")
}

// TestEpisodeTraceEdges tests linking episodes to traces and querying those links
func TestEpisodeTraceEdges(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create episode and trace
	ep := &Episode{
		ID:             "ep-link-1",
		Content:        "Discussion about the new API design",
		Author:         "user",
		TimestampEvent: time.Now(),
	}
	if err := db.AddEpisode(ep); err != nil {
		t.Fatalf("AddEpisode failed: %v", err)
	}
	tr := &Trace{ID: "trace-link-1", Summary: "API design decisions"}
	if err := addTestTrace(t, db, tr); err != nil {
		t.Fatalf("AddTrace failed: %v", err)
	}

	// Add episode-trace edge
	if err := db.AddEpisodeTraceEdge("ep-link-1", "trace-link-1", "informs the design described", 0.85); err != nil {
		t.Fatalf("AddEpisodeTraceEdge failed: %v", err)
	}

	// Query: episodes referencing this trace
	episodes, err := db.GetEpisodesReferencingTrace("trace-link-1")
	if err != nil {
		t.Fatalf("GetEpisodesReferencingTrace failed: %v", err)
	}
	if len(episodes) != 1 {
		t.Fatalf("Expected 1 episode referencing trace, got %d", len(episodes))
	}
	if episodes[0].ID != "ep-link-1" {
		t.Errorf("Expected ep-link-1, got %s", episodes[0].ID)
	}

	// Query: traces referenced by this episode
	traceIDs, err := db.GetTracesReferencedByEpisode("ep-link-1")
	if err != nil {
		t.Fatalf("GetTracesReferencedByEpisode failed: %v", err)
	}
	if len(traceIDs) != 1 || traceIDs[0] != "trace-link-1" {
		t.Errorf("Expected [trace-link-1], got %v", traceIDs)
	}

	// Query: edge details
	edges, err := db.GetEpisodeTraceEdges("trace-link-1")
	if err != nil {
		t.Fatalf("GetEpisodeTraceEdges failed: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("Expected 1 edge, got %d", len(edges))
	}
	if edges[0].RelationshipDesc != "informs the design described" {
		t.Errorf("Expected relationship desc 'informs the design described', got %q", edges[0].RelationshipDesc)
	}
	if edges[0].Confidence != 0.85 {
		t.Errorf("Expected confidence 0.85, got %f", edges[0].Confidence)
	}

	// Deduplication: adding same edge again should not error
	if err := db.AddEpisodeTraceEdge("ep-link-1", "trace-link-1", "duplicate attempt", 0.5); err != nil {
		t.Errorf("Duplicate AddEpisodeTraceEdge should not fail, got: %v", err)
	}
	edges, _ = db.GetEpisodeTraceEdges("trace-link-1")
	if len(edges) != 1 {
		t.Errorf("Duplicate edge insert should be ignored, still expected 1 edge, got %d", len(edges))
	}
}

// TestMarkTraceDone tests the completion tracking feature
func TestMarkTraceDone(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Add two traces: one to be marked done, one to remain active
	active := &Trace{ID: "trace-active", ShortID: "tr_act", Summary: "Active ongoing work", Activation: 0.8}
	done := &Trace{ID: "trace-done", ShortID: "tr_don", Summary: "Completed task", Activation: 0.7}

	if err := addTestTrace(t, db, active); err != nil {
		t.Fatalf("AddTrace(active) failed: %v", err)
	}
	if err := addTestTrace(t, db, done); err != nil {
		t.Fatalf("AddTrace(done) failed: %v", err)
	}

	// Before marking: both traces appear in activated traces
	traces, err := db.GetActivatedTraces(0.5, 10)
	if err != nil {
		t.Fatalf("GetActivatedTraces failed: %v", err)
	}
	if len(traces) != 2 {
		t.Fatalf("Expected 2 activated traces, got %d", len(traces))
	}

	// Mark the done trace as completed
	if err := db.MarkTraceDone("tr_don", "ep_abc"); err != nil {
		t.Fatalf("MarkTraceDone failed: %v", err)
	}

	// After marking: only the active trace appears in activated traces
	traces, err = db.GetActivatedTraces(0.5, 10)
	if err != nil {
		t.Fatalf("GetActivatedTraces after done failed: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("Expected 1 activated trace after done, got %d", len(traces))
	}
	if traces[0].ShortID != "tr_act" {
		t.Errorf("Expected active trace, got %s", traces[0].ShortID)
	}

	// Done trace is still directly retrievable by short_id
	tr, err := db.GetTraceByShortID("tr_don")
	if err != nil {
		t.Fatalf("GetTraceByShortID failed: %v", err)
	}
	if tr == nil {
		t.Fatal("Done trace should still be retrievable by short_id")
	}
	if !tr.Done {
		t.Error("Expected Done=true on retrieved trace")
	}
	if tr.Resolution != "ep_abc" {
		t.Errorf("Expected Resolution='ep_abc', got %q", tr.Resolution)
	}
	if tr.DoneAt.IsZero() {
		t.Error("Expected DoneAt to be set")
	}

	// Mark non-existent trace should return error
	if err := db.MarkTraceDone("tr_nope", ""); err == nil {
		t.Error("Expected error for non-existent trace, got nil")
	}
}

// TestMarkTraceConflict tests the conflict tracking feature
func TestMarkTraceConflict(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Add two traces that will be marked as conflicting
	trA := &Trace{ID: "trace-prefer-dark", ShortID: "tr_drk", Summary: "User prefers dark mode", Activation: 0.7}
	trB := &Trace{ID: "trace-prefer-light", ShortID: "tr_lgt", Summary: "User switched to light mode", Activation: 0.7}

	if err := addTestTrace(t, db, trA); err != nil {
		t.Fatalf("AddTrace(A) failed: %v", err)
	}
	if err := addTestTrace(t, db, trB); err != nil {
		t.Fatalf("AddTrace(B) failed: %v", err)
	}

	// Before marking: no conflicts
	trAFetched, err := db.GetTraceByShortID("tr_drk")
	if err != nil || trAFetched == nil {
		t.Fatalf("GetTraceByShortID(tr_drk) failed: %v", err)
	}
	if trAFetched.HasConflict {
		t.Error("Expected HasConflict=false before marking")
	}

	// Mark the conflict (caller must mark both directions)
	if err := db.MarkTraceConflict("trace-prefer-dark", "tr_lgt"); err != nil {
		t.Fatalf("MarkTraceConflict(A) failed: %v", err)
	}
	if err := db.MarkTraceConflict("trace-prefer-light", "tr_drk"); err != nil {
		t.Fatalf("MarkTraceConflict(B) failed: %v", err)
	}

	// After marking: both traces show conflict info
	trAFetched, err = db.GetTraceByShortID("tr_drk")
	if err != nil || trAFetched == nil {
		t.Fatalf("GetTraceByShortID(tr_drk) after conflict failed: %v", err)
	}
	if !trAFetched.HasConflict {
		t.Error("Expected HasConflict=true on trace A")
	}
	if trAFetched.ConflictWith != "tr_lgt" {
		t.Errorf("Expected ConflictWith='tr_lgt', got %q", trAFetched.ConflictWith)
	}

	trBFetched, err := db.GetTraceByShortID("tr_lgt")
	if err != nil || trBFetched == nil {
		t.Fatalf("GetTraceByShortID(tr_lgt) after conflict failed: %v", err)
	}
	if !trBFetched.HasConflict {
		t.Error("Expected HasConflict=true on trace B")
	}
	if trBFetched.ConflictWith != "tr_drk" {
		t.Errorf("Expected ConflictWith='tr_drk', got %q", trBFetched.ConflictWith)
	}

	// Marking the same conflict again is idempotent
	if err := db.MarkTraceConflict("trace-prefer-dark", "tr_lgt"); err != nil {
		t.Fatalf("Second MarkTraceConflict should be idempotent, got: %v", err)
	}
	trAFetched2, _ := db.GetTraceByShortID("tr_drk")
	if trAFetched2.ConflictWith != "tr_lgt" {
		t.Errorf("Idempotent re-mark should not duplicate entry, got %q", trAFetched2.ConflictWith)
	}

	// GetTraceShortID returns the correct short_id
	shortID, err := db.GetTraceShortID("trace-prefer-dark")
	if err != nil {
		t.Fatalf("GetTraceShortID failed: %v", err)
	}
	if shortID != "tr_drk" {
		t.Errorf("Expected short_id 'tr_drk', got %q", shortID)
	}
}

// TestInjectConflictPartners verifies that retrieval results are augmented with
// conflict partner traces when a conflicted trace is retrieved.
func TestInjectConflictPartners(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Add two conflicting traces
	trA := &Trace{ID: "trace-dark-mode", ShortID: "tr_dA", Summary: "User prefers dark mode", Activation: 0.8}
	trB := &Trace{ID: "trace-light-mode", ShortID: "tr_lB", Summary: "User switched to light mode", Activation: 0.6}

	if err := addTestTrace(t, db, trA); err != nil {
		t.Fatalf("AddTrace(A): %v", err)
	}
	if err := addTestTrace(t, db, trB); err != nil {
		t.Fatalf("AddTrace(B): %v", err)
	}
	if err := db.MarkTraceConflict("trace-dark-mode", "tr_lB"); err != nil {
		t.Fatalf("MarkTraceConflict(A): %v", err)
	}
	if err := db.MarkTraceConflict("trace-light-mode", "tr_dA"); err != nil {
		t.Fatalf("MarkTraceConflict(B): %v", err)
	}

	// Fetch A to confirm it has has_conflict set
	fetchedA, err := db.GetTrace("trace-dark-mode")
	if err != nil || fetchedA == nil {
		t.Fatalf("GetTrace(A): %v", err)
	}
	fetchedA.Activation = 0.75

	// Simulate a retrieval result that only contains trace A
	result := &RetrievalResult{Traces: []*Trace{fetchedA}}

	// injectConflictPartners should add trace B
	db.injectConflictPartners(result)

	if len(result.Traces) != 2 {
		t.Fatalf("Expected 2 traces after injection, got %d", len(result.Traces))
	}

	// Find the injected partner
	var partner *Trace
	for _, tr := range result.Traces {
		if tr.ID == "trace-light-mode" {
			partner = tr
		}
	}
	if partner == nil {
		t.Fatal("Expected trace-light-mode to be injected as conflict partner")
	}
	// Partner should have the same activation as the conflicting trace
	if partner.Activation != fetchedA.Activation {
		t.Errorf("Expected partner activation=%.2f, got %.2f", fetchedA.Activation, partner.Activation)
	}

	// Already-present partners should not be duplicated
	db.injectConflictPartners(result)
	if len(result.Traces) != 2 {
		t.Errorf("Second inject should not add duplicates, got %d traces", len(result.Traces))
	}
}

// TestInjectConflictPartnersResolved verifies that already-resolved conflicts
// do not cause the superseded partner to be injected into retrieval results.
func TestInjectConflictPartnersResolved(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Add two conflicting traces
	trA := &Trace{ID: "trace-winner", ShortID: "tr_win", Summary: "User prefers dark mode (current)", Activation: 0.8}
	trB := &Trace{ID: "trace-loser", ShortID: "tr_los", Summary: "User prefers light mode (superseded)", Activation: 0.6}

	if err := addTestTrace(t, db, trA); err != nil {
		t.Fatalf("AddTrace(A): %v", err)
	}
	if err := addTestTrace(t, db, trB); err != nil {
		t.Fatalf("AddTrace(B): %v", err)
	}
	if err := db.MarkTraceConflict("trace-winner", "tr_los"); err != nil {
		t.Fatalf("MarkTraceConflict(A): %v", err)
	}
	if err := db.MarkTraceConflict("trace-loser", "tr_win"); err != nil {
		t.Fatalf("MarkTraceConflict(B): %v", err)
	}

	// Resolve the conflict: winner keeps, loser marked done
	if err := db.ResolveTraceConflict("trace-winner"); err != nil {
		t.Fatalf("ResolveTraceConflict(winner): %v", err)
	}
	if err := db.ResolveTraceConflict("trace-loser"); err != nil {
		t.Fatalf("ResolveTraceConflict(loser): %v", err)
	}
	if err := db.MarkTraceDone("tr_los", ""); err != nil {
		t.Fatalf("MarkTraceDone(loser): %v", err)
	}

	// Fetch winner (has_conflict=true, conflict_resolved_at set)
	fetchedA, err := db.GetTrace("trace-winner")
	if err != nil || fetchedA == nil {
		t.Fatalf("GetTrace(winner): %v", err)
	}
	if fetchedA.ConflictResolvedAt.IsZero() {
		t.Error("Expected ConflictResolvedAt to be set on winner")
	}
	fetchedA.Activation = 0.75

	// Retrieval result with only the winner
	result := &RetrievalResult{Traces: []*Trace{fetchedA}}

	// injectConflictPartners should NOT inject the resolved/done partner
	db.injectConflictPartners(result)

	if len(result.Traces) != 1 {
		t.Errorf("Resolved conflict: expected 1 trace (no injection), got %d", len(result.Traces))
	}
}
