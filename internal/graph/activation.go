package graph

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
)

// Activation parameters (from Synapse paper - arxiv:2601.02744)
const (
	// Per-iteration decay (not per-query)
	DecayRate    = 0.5 // δ - retention factor per iteration (1-δ retained)
	SpreadFactor = 0.8 // S - spreading coefficient

	// Iteration control
	DefaultIters = 3 // T - iterations to stability

	// Lateral inhibition parameters
	InhibitionStrength = 0.15 // β - how strongly winners suppress losers
	InhibitionTopM     = 7    // M - number of top nodes that suppress

	// Sigmoid transform parameters
	SigmoidGamma = 5.0 // γ - steepness of sigmoid
	SigmoidTheta = 0.5 // θ - firing threshold

	// Temporal decay for edge weights
	TemporalDecayRho = 0.01 // ρ - temporal decay coefficient

	// Feeling of knowing rejection
	FoKThreshold = 0.12 // τ_gate - reject if max activation below this

	// Graph limits
	MaxActiveNodes  = 10000
	MaxEdgesPerNode = 15

	// Seed boost for matched traces
	SeedBoost = 0.5 // additive boost for seed nodes
)

// GetAllTraceActivations returns current activation values for all traces
func (g *DB) GetAllTraceActivations() (map[string]float64, error) {
	rows, err := g.db.Query(`SELECT id, activation FROM traces WHERE activation > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]float64)
	for rows.Next() {
		var id string
		var activation float64
		if err := rows.Scan(&id, &activation); err != nil {
			continue
		}
		result[id] = activation
	}
	return result, nil
}

// PersistActivations saves activation values to the database
func (g *DB) PersistActivations(activations map[string]float64) error {
	for id, activation := range activations {
		if err := g.UpdateTraceActivation(id, activation); err != nil {
			// Continue on error, best effort
			continue
		}
	}
	return nil
}

// SpreadActivation performs spreading activation from seed nodes
// Implements Synapse-style algorithm with per-iteration decay, fan effect, and lateral inhibition
// Returns a map of node IDs to activation levels
func (g *DB) SpreadActivation(seedIDs []string, iterations int) (map[string]float64, error) {
	if iterations <= 0 {
		iterations = DefaultIters
	}

	// Initialize activation map - start fresh each query
	activation := make(map[string]float64)

	// Track which nodes are seeds (they get protection from full decay)
	seedSet := make(map[string]bool)
	for _, id := range seedIDs {
		activation[id] = SeedBoost
		seedSet[id] = true
	}

	// Build neighbor cache and compute fan-out degrees
	neighborCache := make(map[string][]Neighbor)
	fanOut := make(map[string]float64)
	for id := range activation {
		neighbors, err := g.GetTraceNeighbors(id)
		if err == nil {
			neighborCache[id] = neighbors
			fanOut[id] = math.Max(1.0, float64(len(neighbors)))
		}
	}

	// Iterate spreading activation (T=3 iterations)
	for iter := 0; iter < iterations; iter++ {
		newActivation := make(map[string]float64)

		for id, a := range activation {
			// Get neighbors (lazy load if not cached)
			neighbors, ok := neighborCache[id]
			if !ok {
				var err error
				neighbors, err = g.GetTraceNeighbors(id)
				if err != nil {
					continue
				}
				neighborCache[id] = neighbors
				fanOut[id] = math.Max(1.0, float64(len(neighbors)))
			}

			// Spread to neighbors with fan effect
			// Formula: S * w_ji * a_j / fan(j)
			for _, neighbor := range neighbors {
				contribution := SpreadFactor * neighbor.Weight * a / fanOut[id]
				newActivation[neighbor.ID] += contribution

				// Cache neighbor's neighbors for next iteration
				if _, ok := neighborCache[neighbor.ID]; !ok {
					nNeighbors, err := g.GetTraceNeighbors(neighbor.ID)
					if err == nil {
						neighborCache[neighbor.ID] = nNeighbors
						fanOut[neighbor.ID] = math.Max(1.0, float64(len(nNeighbors)))
					}
				}
			}

			// Self-activation with decay: (1-δ) * a_i(t)
			decayedSelf := (1 - DecayRate) * a
			newActivation[id] += decayedSelf

			// Seed nodes maintain minimum activation (prevents isolated nodes from vanishing)
			if seedSet[id] && newActivation[id] < 0.3 {
				newActivation[id] = 0.3
			}
		}

		activation = newActivation
	}

	// Apply lateral inhibition (top-M winners suppress competitors)
	activation = applyLateralInhibition(activation)

	// Apply sigmoid transform to convert to firing rates
	activation = applySigmoid(activation)

	return activation, nil
}

// SpreadActivationFromEmbedding spreads activation using dual-trigger seeding
// Dual trigger: combines lexical matching (BM25-style) AND semantic embedding
func (g *DB) SpreadActivationFromEmbedding(queryEmb []float64, queryText string, topK int, iterations int) (map[string]float64, error) {
	seedSet := make(map[string]bool)

	// Trigger 1: Semantic similarity (embedding-based)
	semanticSeeds, err := g.FindSimilarTraces(queryEmb, topK)
	if err == nil {
		for _, id := range semanticSeeds {
			seedSet[id] = true
		}
	}

	// Trigger 2: Lexical matching (BM25-style keyword matching)
	if queryText != "" {
		lexicalSeeds, err := g.FindTracesWithKeywords(queryText, topK)
		if err == nil {
			for _, id := range lexicalSeeds {
				seedSet[id] = true
			}
		}
	}

	// Convert set to slice
	seedIDs := make([]string, 0, len(seedSet))
	for id := range seedSet {
		seedIDs = append(seedIDs, id)
	}

	if len(seedIDs) == 0 {
		return make(map[string]float64), nil
	}

	return g.SpreadActivation(seedIDs, iterations)
}

// FindTracesWithKeywords performs lexical/keyword matching (BM25-style)
// Returns traces that contain query keywords in their summary
func (g *DB) FindTracesWithKeywords(query string, topK int) ([]string, error) {
	// Extract keywords (simple: lowercase, split, filter short words)
	keywords := extractKeywords(query)
	if len(keywords) == 0 {
		return nil, nil
	}

	// Build SQL LIKE query for each keyword
	// This is a simple approximation of BM25 - matches any keyword
	rows, err := g.db.Query(`SELECT id, summary FROM traces`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		id    string
		score int // number of keyword matches
	}

	var candidates []scored
	for rows.Next() {
		var id, summary string
		if err := rows.Scan(&id, &summary); err != nil {
			continue
		}

		summaryLower := strings.ToLower(summary)
		matchCount := 0
		for _, kw := range keywords {
			if strings.Contains(summaryLower, kw) {
				matchCount++
			}
		}

		if matchCount > 0 {
			candidates = append(candidates, scored{id: id, score: matchCount})
		}
	}

	// Sort by match count descending
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

// extractKeywords extracts searchable keywords from query text
func extractKeywords(query string) []string {
	// Simple tokenization: lowercase, split on whitespace/punctuation
	query = strings.ToLower(query)

	// Replace common punctuation with spaces
	for _, p := range []string{".", ",", "!", "?", ":", ";", "'", "\""} {
		query = strings.ReplaceAll(query, p, " ")
	}

	words := strings.Fields(query)

	// Filter out short words and common stop words
	stopWords := map[string]bool{
		"a": true, "an": true, "the": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "must": true, "shall": true,
		"i": true, "me": true, "my": true, "we": true, "our": true,
		"you": true, "your": true, "he": true, "she": true, "it": true,
		"they": true, "them": true, "their": true, "this": true, "that": true,
		"what": true, "which": true, "who": true, "whom": true, "whose": true,
		"where": true, "when": true, "why": true, "how": true,
		"and": true, "or": true, "but": true, "if": true, "then": true,
		"than": true, "so": true, "as": true, "of": true, "at": true,
		"by": true, "for": true, "with": true, "about": true, "into": true,
		"to": true, "from": true, "in": true, "on": true, "up": true,
		"out": true, "off": true, "over": true, "under": true,
		"tell": true, "know": true,
	}

	var keywords []string
	for _, word := range words {
		if len(word) >= 3 && !stopWords[word] {
			keywords = append(keywords, word)
		}
	}

	return keywords
}

// Minimum similarity threshold for seeding
const MinSimilarityThreshold = 0.3

// FindSimilarTraces finds traces similar to the query embedding
// Only returns traces with similarity above MinSimilarityThreshold
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
		// Only include traces above minimum similarity threshold
		if sim >= MinSimilarityThreshold {
			candidates = append(candidates, scored{id: id, score: sim})
		}
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

// Retrieve performs full memory retrieval with dual-trigger spreading activation
// Uses both embedding similarity AND lexical matching for seeding
func (g *DB) Retrieve(queryEmb []float64, queryText string, limit int) (*RetrievalResult, error) {
	result := &RetrievalResult{}

	// Spread activation using dual triggers (semantic + lexical)
	activation, err := g.SpreadActivationFromEmbedding(queryEmb, queryText, 20, DefaultIters)
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
			trace.Activation = candidates[i].activation
			result.Traces = append(result.Traces, trace)
		}
	}

	return result, nil
}

// applyLateralInhibition applies Synapse-style lateral inhibition
// Top M winners suppress competitors: û_i = max(0, u_i - β * Σ(u_k - u_i) for u_k > u_i)
func applyLateralInhibition(activation map[string]float64) map[string]float64 {
	if len(activation) == 0 {
		return activation
	}

	// Sort nodes by activation to find top M
	type nodeAct struct {
		id  string
		act float64
	}
	nodes := make([]nodeAct, 0, len(activation))
	for id, act := range activation {
		nodes = append(nodes, nodeAct{id, act})
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].act > nodes[j].act
	})

	// Identify top M winners
	topM := InhibitionTopM
	if topM > len(nodes) {
		topM = len(nodes)
	}
	winners := make(map[string]float64)
	for i := 0; i < topM; i++ {
		winners[nodes[i].id] = nodes[i].act
	}

	// Apply inhibition: each non-winner is suppressed by winners above it
	result := make(map[string]float64)
	for id, act := range activation {
		if _, isWinner := winners[id]; isWinner {
			// Winners keep their activation
			result[id] = act
		} else {
			// Non-winners are suppressed by winners
			inhibition := 0.0
			for _, winnerAct := range winners {
				if winnerAct > act {
					inhibition += (winnerAct - act)
				}
			}
			suppressed := act - InhibitionStrength*inhibition
			if suppressed > 0 {
				result[id] = suppressed
			}
			// If suppressed <= 0, node is dropped
		}
	}

	return result
}

// applySigmoid applies sigmoid transform to convert activations to firing rates
// σ(x) = 1 / (1 + exp(-γ(x - θ)))
func applySigmoid(activation map[string]float64) map[string]float64 {
	result := make(map[string]float64)
	for id, act := range activation {
		// Sigmoid transform
		firing := 1.0 / (1.0 + math.Exp(-SigmoidGamma*(act-SigmoidTheta)))
		result[id] = firing
	}
	return result
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
