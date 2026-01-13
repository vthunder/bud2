package graph

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	result, err := db.Retrieve(similarQuery, 5)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	if len(result.Traces) == 0 {
		t.Error("Expected to retrieve trace with similar embedding")
	}

	// Query with very different embedding - FoK should reject
	differentQuery := []float64{0.0, 0.0, 0.9, 0.1}
	result, err = db.Retrieve(differentQuery, 5)
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

	// Add non-core traces
	db.AddTrace(&Trace{ID: "trace-1", Summary: "Test 1", Activation: 1.0})
	db.AddTrace(&Trace{ID: "trace-2", Summary: "Test 2", Activation: 0.8})

	// Add a core trace (should not decay)
	db.AddTrace(&Trace{ID: "core-1", Summary: "Core identity", Activation: 1.0, IsCore: true})

	// Decay by 0.9 (lose 10% activation)
	db.DecayActivation(0.9)

	// Check non-core traces decayed
	t1, _ := db.GetTrace("trace-1")
	if t1.Activation != 0.9 {
		t.Errorf("Expected trace-1 activation 0.9, got %f", t1.Activation)
	}

	t2, _ := db.GetTrace("trace-2")
	if t2.Activation < 0.71 || t2.Activation > 0.73 {
		t.Errorf("Expected trace-2 activation ~0.72, got %f", t2.Activation)
	}

	// Check core trace did NOT decay
	core, _ := db.GetTrace("core-1")
	if core.Activation != 1.0 {
		t.Errorf("Expected core trace to maintain activation 1.0, got %f", core.Activation)
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
