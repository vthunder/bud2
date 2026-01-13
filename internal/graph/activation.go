package graph

import (
	"encoding/json"
	"math"
	"sort"
)

// Activation parameters (from Synapse paper)
const (
	DecayRate       = 0.1  // δ - how much activation decays each iteration
	SpreadFactor    = 0.8  // S - how much activation spreads to neighbors
	DefaultIters    = 3    // T - iterations to stability
	FoKThreshold    = 0.12 // "Feeling of Knowing" rejection threshold
	MaxActiveNodes  = 10000
	MaxEdgesPerNode = 15
)

// SpreadActivation performs spreading activation from seed nodes
// Returns a map of node IDs to activation levels
func (g *DB) SpreadActivation(seedIDs []string, iterations int) (map[string]float64, error) {
	if iterations <= 0 {
		iterations = DefaultIters
	}

	activation := make(map[string]float64)

	// Initialize seed nodes with full activation
	for _, id := range seedIDs {
		activation[id] = 1.0
	}

	// Iterate spreading activation
	for i := 0; i < iterations; i++ {
		newActivation := make(map[string]float64)

		for id, a := range activation {
			// Get neighbors (traces and their relations)
			neighbors, err := g.GetTraceNeighbors(id)
			if err != nil {
				continue
			}

			fanOut := float64(len(neighbors))
			if fanOut == 0 {
				fanOut = 1
			}

			// Spread to neighbors
			for _, neighbor := range neighbors {
				contribution := SpreadFactor * neighbor.Weight * a / fanOut
				newActivation[neighbor.ID] += contribution
			}

			// Self-activation with decay
			newActivation[id] += (1 - DecayRate) * a
		}

		activation = newActivation
	}

	// Apply lateral inhibition (suppress weaker activations)
	activation = applyInhibition(activation)

	return activation, nil
}

// SpreadActivationFromEmbedding spreads activation from nodes similar to the query embedding
func (g *DB) SpreadActivationFromEmbedding(queryEmb []float64, topK int, iterations int) (map[string]float64, error) {
	// Find seed nodes via vector similarity
	seedIDs, err := g.FindSimilarTraces(queryEmb, topK)
	if err != nil {
		return nil, err
	}

	if len(seedIDs) == 0 {
		return make(map[string]float64), nil
	}

	return g.SpreadActivation(seedIDs, iterations)
}

// FindSimilarTraces finds traces similar to the query embedding
func (g *DB) FindSimilarTraces(queryEmb []float64, topK int) ([]string, error) {
	// Get all traces with embeddings
	rows, err := g.db.Query(`
		SELECT id, embedding FROM traces WHERE embedding IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		id    string
		score float64
	}

	var candidates []scored
	for rows.Next() {
		var id string
		var embBytes []byte
		if err := rows.Scan(&id, &embBytes); err != nil {
			continue
		}

		var embedding []float64
		if err := json.Unmarshal(embBytes, &embedding); err != nil {
			continue
		}

		sim := cosineSimilarity(queryEmb, embedding)
		candidates = append(candidates, scored{id: id, score: sim})
	}

	// Sort by similarity descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	// Return top K
	result := make([]string, 0, topK)
	for i := 0; i < len(candidates) && i < topK; i++ {
		result = append(result, candidates[i].id)
	}

	return result, nil
}

// Retrieve performs full memory retrieval with spreading activation
func (g *DB) Retrieve(queryEmb []float64, limit int) (*RetrievalResult, error) {
	result := &RetrievalResult{}

	// Spread activation from query embedding
	activation, err := g.SpreadActivationFromEmbedding(queryEmb, 20, DefaultIters)
	if err != nil {
		return nil, err
	}

	// Check "Feeling of Knowing" - should we reject this query?
	maxActivation := 0.0
	for _, a := range activation {
		if a > maxActivation {
			maxActivation = a
		}
	}

	if maxActivation < FoKThreshold {
		// Low confidence - return empty or minimal result
		return result, nil
	}

	// Sort by activation and get top traces
	type scored struct {
		id         string
		activation float64
	}
	var candidates []scored
	for id, a := range activation {
		candidates = append(candidates, scored{id: id, activation: a})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].activation > candidates[j].activation
	})

	// Fetch top traces
	for i := 0; i < len(candidates) && i < limit; i++ {
		trace, err := g.GetTrace(candidates[i].id)
		if err == nil && trace != nil {
			// Update activation in DB
			g.UpdateTraceActivation(trace.ID, candidates[i].activation)
			trace.Activation = candidates[i].activation
			result.Traces = append(result.Traces, trace)
		}
	}

	return result, nil
}

// applyInhibition applies lateral inhibition to suppress weaker activations
func applyInhibition(activation map[string]float64) map[string]float64 {
	if len(activation) == 0 {
		return activation
	}

	// Find max activation
	maxActivation := 0.0
	for _, a := range activation {
		if a > maxActivation {
			maxActivation = a
		}
	}

	// Suppress activations that are much lower than max
	// (Simple version: remove those below 10% of max)
	threshold := maxActivation * 0.1
	for id, a := range activation {
		if a < threshold {
			delete(activation, id)
		}
	}

	return activation
}

// BoostActivation boosts activation for specific traces (e.g., from percept similarity)
func (g *DB) BoostActivation(traceIDs []string, boost float64, threshold float64) error {
	for _, id := range traceIDs {
		trace, err := g.GetTrace(id)
		if err != nil || trace == nil {
			continue
		}

		newActivation := trace.Activation + boost
		if newActivation > 1.0 {
			newActivation = 1.0
		}

		if newActivation >= threshold {
			g.UpdateTraceActivation(id, newActivation)
		}
	}
	return nil
}

// cosineSimilarity computes similarity between two embeddings
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}
