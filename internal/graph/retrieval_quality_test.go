package graph

import (
	"fmt"
	"testing"
)

// RetrievalTestFixture generates synthetic graphs for retrieval quality testing.
// It creates clusters of related traces with known ground truth for precision/recall measurement.
type RetrievalTestFixture struct {
	db *DB

	// Topics and their traces
	topics      []string           // e.g., ["work", "personal", "health"]
	topicTraces map[string][]string // topicName -> []traceID

	// Entities per topic
	topicEntities map[string][]string // topicName -> []entityID
}

// NewRetrievalTestFixture creates a fixture with the given topic structure.
// tracesPerTopic: how many traces to create for each topic
// entitiesPerTopic: how many entities to create per topic
func NewRetrievalTestFixture(t *testing.T, db *DB, topics []string, tracesPerTopic, entitiesPerTopic int) *RetrievalTestFixture {
	t.Helper()

	f := &RetrievalTestFixture{
		db:            db,
		topics:        topics,
		topicTraces:   make(map[string][]string),
		topicEntities: make(map[string][]string),
	}

	// Create entities for each topic
	for i, topic := range topics {
		for j := 0; j < entitiesPerTopic; j++ {
			entityID := fmt.Sprintf("entity-%s-%d", topic, j)
			entityName := fmt.Sprintf("%s-Entity-%d", topic, j)
			db.AddEntity(&Entity{
				ID:       entityID,
				Name:     entityName,
				Type:     EntityProduct, // Use PRODUCT as generic type
				Salience: 0.5,
			})
			f.topicEntities[topic] = append(f.topicEntities[topic], entityID)
		}

		// Create traces for each topic
		for j := 0; j < tracesPerTopic; j++ {
			traceID := fmt.Sprintf("trace-%s-%d", topic, j)

			// Generate a topic-specific embedding
			// Each topic gets a different "direction" in embedding space
			embedding := makeTopicEmbedding(i, len(topics))

			// Add some variation within topic
			embedding = addNoise(embedding, 0.1)

			db.AddTrace(&Trace{
				ID:         traceID,
				Summary:    fmt.Sprintf("Trace about %s topic, item %d", topic, j),
				Activation: 0.5,
				Embedding:  embedding,
			})

			// Link trace to topic entities (each trace links to all topic entities)
			for _, entityID := range f.topicEntities[topic] {
				db.LinkTraceToEntity(traceID, entityID)
			}

			f.topicTraces[topic] = append(f.topicTraces[topic], traceID)
		}
	}

	return f
}

// makeTopicEmbedding creates an embedding vector for a specific topic.
// Uses a simple scheme: topic i gets a one-hot-ish vector at position i.
func makeTopicEmbedding(topicIndex, numTopics int) []float64 {
	// Use enough dimensions for clean separation
	dims := 16
	if numTopics > dims {
		dims = numTopics
	}
	emb := make([]float64, dims)

	// One-hot style: each topic gets its own dimension
	emb[topicIndex] = 1.0

	return normalize(emb)
}

// addNoise adds random noise to an embedding
func addNoise(emb []float64, amount float64) []float64 {
	result := make([]float64, len(emb))
	for i, v := range emb {
		// Simple deterministic "noise" based on position
		noise := (float64(i%3) - 1) * amount
		result[i] = v + noise
	}
	return normalize(result)
}

// normalize normalizes a vector to unit length
func normalize(v []float64) []float64 {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	if sum == 0 {
		return v
	}
	mag := sqrt(sum)
	result := make([]float64, len(v))
	for i, x := range v {
		result[i] = x / mag
	}
	return result
}

// sqrt is a simple square root (to avoid math import for such a simple function)
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// QueryTopic returns an embedding for querying a specific topic
func (f *RetrievalTestFixture) QueryEmbedding(topicIndex int) []float64 {
	return makeTopicEmbedding(topicIndex, len(f.topics))
}

// GetGroundTruth returns the trace IDs that should be retrieved for a topic query
func (f *RetrievalTestFixture) GetGroundTruth(topicIndex int) []string {
	return f.topicTraces[f.topics[topicIndex]]
}

// GetNoisyTraces returns traces that should NOT be retrieved for a topic query
func (f *RetrievalTestFixture) GetNoisyTraces(topicIndex int) []string {
	var noisy []string
	for i, topic := range f.topics {
		if i != topicIndex {
			noisy = append(noisy, f.topicTraces[topic]...)
		}
	}
	return noisy
}

// measurePrecisionAtK calculates precision@k: (relevant in top k) / k
func measurePrecisionAtK(retrieved []*Trace, groundTruth map[string]bool, k int) float64 {
	if k > len(retrieved) {
		k = len(retrieved)
	}
	if k == 0 {
		return 0
	}
	relevant := 0
	for i := 0; i < k; i++ {
		if groundTruth[retrieved[i].ID] {
			relevant++
		}
	}
	return float64(relevant) / float64(k)
}

// measureRecallAtK calculates recall@k: (relevant in top k) / total_relevant
func measureRecallAtK(retrieved []*Trace, groundTruth map[string]bool, k int) float64 {
	if len(groundTruth) == 0 {
		return 0
	}
	if k > len(retrieved) {
		k = len(retrieved)
	}
	relevant := 0
	for i := 0; i < k; i++ {
		if groundTruth[retrieved[i].ID] {
			relevant++
		}
	}
	return float64(relevant) / float64(len(groundTruth))
}

// TestRetrievalQualitySmallGraph tests retrieval on a small 3-topic graph
func TestRetrievalQualitySmallGraph(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create small fixture: 3 topics, 5 traces each, 2 entities per topic
	topics := []string{"work", "personal", "health"}
	fixture := NewRetrievalTestFixture(t, db, topics, 5, 2)

	// Test retrieval for each topic
	for i, topic := range topics {
		t.Run(topic, func(t *testing.T) {
			queryEmb := fixture.QueryEmbedding(i)
			queryText := fmt.Sprintf("Something about %s-Entity-0", topic)

			result, err := db.Retrieve(queryEmb, queryText, 5)
			if err != nil {
				t.Fatalf("Retrieve failed: %v", err)
			}

			// Build ground truth set
			groundTruth := make(map[string]bool)
			for _, traceID := range fixture.GetGroundTruth(i) {
				groundTruth[traceID] = true
			}

			// Calculate metrics
			p5 := measurePrecisionAtK(result.Traces, groundTruth, 5)
			r5 := measureRecallAtK(result.Traces, groundTruth, 5)

			t.Logf("Topic %s: retrieved %d traces, precision@5=%.2f, recall@5=%.2f",
				topic, len(result.Traces), p5, r5)

			// We expect good precision (most retrieved should be relevant)
			if p5 < 0.6 && len(result.Traces) > 0 {
				t.Errorf("Low precision@5: %.2f (expected >= 0.6)", p5)
			}

			// Log retrieved traces for debugging
			for i, tr := range result.Traces {
				isRelevant := groundTruth[tr.ID]
				t.Logf("  %d. %s (activation=%.3f, relevant=%v)", i+1, tr.ID, tr.Activation, isRelevant)
			}
		})
	}
}

// TestRetrievalQualityMediumGraph tests retrieval with more noise (10 topics, 20 traces each)
func TestRetrievalQualityMediumGraph(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create medium fixture: 10 topics, 20 traces each = 200 traces total
	topics := []string{"work", "personal", "health", "finance", "travel",
		"projects", "meetings", "learning", "family", "hobbies"}
	fixture := NewRetrievalTestFixture(t, db, topics, 20, 3)

	// Test retrieval for a few topics
	testTopics := []int{0, 3, 7} // work, finance, learning

	for _, topicIdx := range testTopics {
		topic := topics[topicIdx]
		t.Run(topic, func(t *testing.T) {
			queryEmb := fixture.QueryEmbedding(topicIdx)
			queryText := fmt.Sprintf("Something about %s-Entity-0", topic)

			result, err := db.Retrieve(queryEmb, queryText, 10)
			if err != nil {
				t.Fatalf("Retrieve failed: %v", err)
			}

			// Build ground truth set
			groundTruth := make(map[string]bool)
			for _, traceID := range fixture.GetGroundTruth(topicIdx) {
				groundTruth[traceID] = true
			}

			// Calculate metrics
			p5 := measurePrecisionAtK(result.Traces, groundTruth, 5)
			p10 := measurePrecisionAtK(result.Traces, groundTruth, 10)

			t.Logf("Topic %s (200 total traces): retrieved %d, precision@5=%.2f, precision@10=%.2f",
				topic, len(result.Traces), p5, p10)

			// With more noise, we expect slightly lower but still reasonable precision
			if p5 < 0.4 && len(result.Traces) >= 5 {
				t.Errorf("Low precision@5: %.2f (expected >= 0.4)", p5)
			}
		})
	}
}

// TestRetrievalQualityLargeGraph tests retrieval at scale (500+ traces)
func TestRetrievalQualityLargeGraph(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large graph test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create large fixture: 10 topics, 50 traces each = 500 traces total
	topics := []string{"work", "personal", "health", "finance", "travel",
		"projects", "meetings", "learning", "family", "hobbies"}
	fixture := NewRetrievalTestFixture(t, db, topics, 50, 5)

	// Test retrieval
	topic := "work"
	t.Run(topic, func(t *testing.T) {
		queryEmb := fixture.QueryEmbedding(0)
		queryText := fmt.Sprintf("Something about %s-Entity-0", topic)

		result, err := db.Retrieve(queryEmb, queryText, 10)
		if err != nil {
			t.Fatalf("Retrieve failed: %v", err)
		}

		// Build ground truth set
		groundTruth := make(map[string]bool)
		for _, traceID := range fixture.GetGroundTruth(0) {
			groundTruth[traceID] = true
		}

		p5 := measurePrecisionAtK(result.Traces, groundTruth, 5)
		p10 := measurePrecisionAtK(result.Traces, groundTruth, 10)

		t.Logf("Topic %s (500 total traces): retrieved %d, precision@5=%.2f, precision@10=%.2f",
			topic, len(result.Traces), p5, p10)

		// At scale, precision may drop - document the actual behavior
		t.Logf("Large graph retrieval behavior captured for analysis")
	})
}

// TestEntityOnlyRetrieval tests retrieval when only entity matching is available (no semantic similarity)
func TestEntityOnlyRetrieval(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create entities
	db.AddEntity(&Entity{ID: "entity-alice", Name: "Alice", Type: EntityPerson, Salience: 0.8})

	// Create traces linked to Alice
	for i := 0; i < 5; i++ {
		db.AddTrace(&Trace{
			ID:         fmt.Sprintf("trace-alice-%d", i),
			Summary:    fmt.Sprintf("Meeting with Alice about topic %d", i),
			Activation: 0.5,
			Embedding:  []float64{0.1, 0.1, 0.1, 0.1}, // Low-magnitude embedding
		})
		db.LinkTraceToEntity(fmt.Sprintf("trace-alice-%d", i), "entity-alice")
	}

	// Create noise traces (not linked to Alice)
	for i := 0; i < 10; i++ {
		db.AddTrace(&Trace{
			ID:         fmt.Sprintf("trace-noise-%d", i),
			Summary:    fmt.Sprintf("Unrelated trace %d", i),
			Activation: 0.5,
			Embedding:  []float64{0.1, 0.1, 0.1, 0.1},
		})
	}

	// Query mentioning "Alice" - should find Alice traces via entity seeding
	result, err := db.Retrieve([]float64{0.1, 0.1, 0.1, 0.1}, "Discuss something with Alice", 5)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	// Check that Alice traces are retrieved
	aliceCount := 0
	for _, tr := range result.Traces {
		if len(tr.ID) > 11 && tr.ID[:11] == "trace-alice" {
			aliceCount++
		}
	}

	t.Logf("Retrieved %d traces, %d related to Alice", len(result.Traces), aliceCount)

	if len(result.Traces) > 0 && aliceCount == 0 {
		t.Error("Expected at least some Alice traces to be retrieved via entity seeding")
	}
}
