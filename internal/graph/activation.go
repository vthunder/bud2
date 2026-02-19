package graph

import (
	"encoding/json"
	"log"
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
	SigmoidTheta = 0.3 // θ - firing threshold (lowered from 0.5 for better dynamic range)

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

	// Trigger 3: Entity-based seeding — match entity names/aliases in query text
	if queryText != "" {
		matchedEntities, err := g.FindEntitiesByText(queryText, 5)
		if err == nil {
			for _, entity := range matchedEntities {
				traceIDs, err := g.GetTracesForEntity(entity.ID)
				if err != nil {
					continue
				}
				// Cap at 5 traces per entity
				cap := 5
				if len(traceIDs) < cap {
					cap = len(traceIDs)
				}
				for _, id := range traceIDs[:cap] {
					seedSet[id] = true
				}
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

// FindTracesWithKeywords performs lexical/keyword matching using FTS5 BM25 ranking.
// Falls back to a Go-side full scan if the FTS5 index is unavailable.
// Returns up to topK trace IDs ordered by relevance.
func (g *DB) FindTracesWithKeywords(query string, topK int) ([]string, error) {
	keywords := extractKeywords(query)
	if len(keywords) == 0 {
		return nil, nil
	}

	// Try FTS5 path first: query trace_fts with OR-joined keywords, ranked by BM25.
	ftsQuery := strings.Join(keywords, " OR ")
	rows, err := g.db.Query(`
		SELECT trace_id
		FROM trace_fts
		WHERE summary MATCH ?
		ORDER BY rank
		LIMIT ?
	`, ftsQuery, topK)
	if err == nil {
		defer rows.Close()
		var result []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				result = append(result, id)
			}
		}
		if rows.Err() == nil {
			return result, nil
		}
	}

	// Fallback: full table scan with Go-side keyword counting (O(n) but always works).
	scanRows, err := g.db.Query(`
		SELECT t.id, COALESCE(ts.summary, '')
		FROM traces t
		LEFT JOIN trace_summaries ts ON t.id = ts.trace_id AND ts.compression_level = 32
	`)
	if err != nil {
		return nil, err
	}
	defer scanRows.Close()

	type scored struct {
		id    string
		score int
	}

	var candidates []scored
	for scanRows.Next() {
		var id, summary string
		if err := scanRows.Scan(&id, &summary); err != nil {
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

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

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
// Scores combine semantic similarity with stored activation to prefer recent/active memories.
// Only returns traces with similarity above MinSimilarityThreshold.
func (g *DB) FindSimilarTraces(queryEmb []float64, topK int) ([]string, error) {
	// Get all traces with embeddings and activation
	rows, err := g.db.Query(`
		SELECT id, embedding, activation FROM traces WHERE embedding IS NOT NULL
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
		var activation float64
		if err := rows.Scan(&id, &embBytes, &activation); err != nil {
			continue
		}

		var embedding []float64
		if err := json.Unmarshal(embBytes, &embedding); err != nil {
			continue
		}

		sim := cosineSimilarity(queryEmb, embedding)
		// Only include traces above minimum similarity threshold
		if sim >= MinSimilarityThreshold {
			// Use pure semantic similarity for seeding (no stored activation blend).
			// Temporal dynamics emerge naturally from spreading activation:
			// - Seeds get initial boost (SeedBoost = 0.5)
			// - Connected traces spread activation
			// - Lateral inhibition and sigmoid transform shape final distribution
			// Using stored activation here created a feedback loop where recently-active
			// traces dominated all queries regardless of semantic relevance.
			candidates = append(candidates, scored{id: id, score: sim})
		}
	}

	// Sort by blended score descending
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

// SimilarTrace represents a trace ID with its similarity score
type SimilarTrace struct {
	ID         string
	Similarity float64
}

// FindSimilarTracesAboveThreshold finds all traces with cosine similarity above the given threshold.
// Returns trace IDs with their raw similarity scores (not blended with activation).
// Used for creating SIMILAR_TO edges during consolidation.
func (g *DB) FindSimilarTracesAboveThreshold(queryEmb []float64, threshold float64, excludeID string) ([]SimilarTrace, error) {
	rows, err := g.db.Query(`SELECT id, embedding FROM traces WHERE embedding IS NOT NULL AND id != ?`, excludeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []SimilarTrace
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
		if sim >= threshold {
			result = append(result, SimilarTrace{ID: id, Similarity: sim})
		}
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
		// Low confidence - return empty or minimal result (FoK rejection)
		return result, nil
	}

	// Detect if query is about recent work/status (where operational traces ARE relevant)
	isStatusQuery := isStatusQuery(queryText)

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

	// Funnel retrieval Phase 1: take top-50 by activation, load only L8 summaries
	phase1Limit := 50
	if phase1Limit > len(candidates) {
		phase1Limit = len(candidates)
	}
	phase1Candidates := candidates[:phase1Limit]

	phase1IDs := make([]string, len(phase1Candidates))
	for i, c := range phase1Candidates {
		phase1IDs[i] = c.id
	}
	l8Map, err := g.GetTracesBatchAtLevel(phase1IDs, 8)
	if err != nil {
		// Fall back to direct full-detail fetch on error
		l8Map = nil
	}

	// Phase 1 scoring: combine activation with L8 text relevance to query
	type phase1scored struct {
		id         string
		activation float64
		score      float64
	}
	keywords := extractKeywords(queryText)
	phase1Scored := make([]phase1scored, 0, len(phase1Candidates))
	for _, c := range phase1Candidates {
		textScore := 0.0
		if l8Map != nil {
			if tr, ok := l8Map[c.id]; ok && tr != nil {
				summaryLower := strings.ToLower(tr.Summary)
				for _, kw := range keywords {
					if strings.Contains(summaryLower, kw) {
						textScore += 1.0
					}
				}
			}
		}
		// Combined score: activation dominant, text relevance breaks ties
		combinedScore := c.activation + textScore*0.1
		phase1Scored = append(phase1Scored, phase1scored{
			id:         c.id,
			activation: c.activation,
			score:      combinedScore,
		})
	}
	sort.Slice(phase1Scored, func(i, j int) bool {
		return phase1Scored[i].score > phase1Scored[j].score
	})

	// Phase 2: load full detail for top-N shortlisted candidates
	fetchLimit := limit
	if fetchLimit > len(phase1Scored) {
		fetchLimit = len(phase1Scored)
	}
	shortlisted := phase1Scored[:fetchLimit]

	// Log funnel filter rate metric (skip when limit=0, that's an intentional no-op)
	if limit > 0 && phase1Limit > 0 {
		filterRate := float64(phase1Limit-fetchLimit) / float64(phase1Limit)
		log.Printf("[funnel] phase1=%d shortlisted=%d filter_rate=%.2f query=%q", phase1Limit, fetchLimit, filterRate, queryText)
	}

	topIDs := make([]string, len(shortlisted))
	for i, c := range shortlisted {
		topIDs[i] = c.id
	}
	traceMap, err := g.GetTracesBatch(topIDs)
	if err != nil {
		return nil, err
	}

	// Apply operational bias and assemble result
	for _, c := range shortlisted {
		trace, ok := traceMap[c.id]
		if !ok || trace == nil {
			continue
		}
		act := c.activation
		if !isStatusQuery && trace.TraceType == TraceTypeOperational {
			act *= 0.5
		}
		trace.Activation = act
		result.Traces = append(result.Traces, trace)
	}

	// Re-sort after applying operational bias (may reorder results)
	sort.Slice(result.Traces, func(i, j int) bool {
		return result.Traces[i].Activation > result.Traces[j].Activation
	})

	return result, nil
}

// RetrieveWithContext performs memory retrieval that factors in current context.
// contextTraceIDs are traces that are already "activated" (e.g., from current working memory).
// These get added as additional seeds for spreading activation, biasing retrieval toward
// memories connected to the current context.
func (g *DB) RetrieveWithContext(queryEmb []float64, queryText string, contextTraceIDs []string, limit int) (*RetrievalResult, error) {
	result := &RetrievalResult{}

	// Build seed set from all triggers
	seedSet := make(map[string]bool)

	// Trigger 1: Semantic similarity (embedding-based)
	semanticSeeds, err := g.FindSimilarTraces(queryEmb, 20)
	if err == nil {
		for _, id := range semanticSeeds {
			seedSet[id] = true
		}
	}

	// Trigger 2: Lexical matching
	if queryText != "" {
		lexicalSeeds, err := g.FindTracesWithKeywords(queryText, 20)
		if err == nil {
			for _, id := range lexicalSeeds {
				seedSet[id] = true
			}
		}
	}

	// Trigger 3: Entity-based seeding
	if queryText != "" {
		matchedEntities, err := g.FindEntitiesByText(queryText, 5)
		if err == nil {
			for _, entity := range matchedEntities {
				traceIDs, err := g.GetTracesForEntity(entity.ID)
				if err != nil {
					continue
				}
				cap := 5
				if len(traceIDs) < cap {
					cap = len(traceIDs)
				}
				for _, id := range traceIDs[:cap] {
					seedSet[id] = true
				}
			}
		}
	}

	// Trigger 4: Context traces (current working memory/activated traces)
	for _, id := range contextTraceIDs {
		seedSet[id] = true
	}

	// Convert set to slice
	seedIDs := make([]string, 0, len(seedSet))
	for id := range seedSet {
		seedIDs = append(seedIDs, id)
	}

	if len(seedIDs) == 0 {
		return result, nil
	}

	// Spread activation from all seeds
	activation, err := g.SpreadActivation(seedIDs, DefaultIters)
	if err != nil {
		return nil, err
	}

	// Check FoK threshold
	maxActivation := 0.0
	for _, a := range activation {
		if a > maxActivation {
			maxActivation = a
		}
	}

	if maxActivation < FoKThreshold {
		// Low confidence - return empty or minimal result (FoK rejection with context)
		return result, nil
	}

	// Detect if query is about recent work/status (where operational traces ARE relevant)
	isStatusQuery := isStatusQuery(queryText)

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

	// Funnel retrieval Phase 1: take top-50 by activation, load only L8 summaries
	phase1LimitCtx := 50
	if phase1LimitCtx > len(candidates) {
		phase1LimitCtx = len(candidates)
	}
	phase1CandidatesCtx := candidates[:phase1LimitCtx]

	phase1IDsCtx := make([]string, len(phase1CandidatesCtx))
	for i, c := range phase1CandidatesCtx {
		phase1IDsCtx[i] = c.id
	}
	l8MapCtx, err := g.GetTracesBatchAtLevel(phase1IDsCtx, 8)
	if err != nil {
		l8MapCtx = nil
	}

	// Phase 1 scoring: activation + L8 text relevance
	type phase1scoredCtx struct {
		id         string
		activation float64
		score      float64
	}
	kwCtx := extractKeywords(queryText)
	phase1ScoredCtx := make([]phase1scoredCtx, 0, len(phase1CandidatesCtx))
	for _, c := range phase1CandidatesCtx {
		textScore := 0.0
		if l8MapCtx != nil {
			if tr, ok := l8MapCtx[c.id]; ok && tr != nil {
				summaryLower := strings.ToLower(tr.Summary)
				for _, kw := range kwCtx {
					if strings.Contains(summaryLower, kw) {
						textScore += 1.0
					}
				}
			}
		}
		combinedScore := c.activation + textScore*0.1
		phase1ScoredCtx = append(phase1ScoredCtx, phase1scoredCtx{
			id:         c.id,
			activation: c.activation,
			score:      combinedScore,
		})
	}
	sort.Slice(phase1ScoredCtx, func(i, j int) bool {
		return phase1ScoredCtx[i].score > phase1ScoredCtx[j].score
	})

	// Phase 2: load full detail for shortlisted top-N
	fetchLimitCtx := limit
	if fetchLimitCtx > len(phase1ScoredCtx) {
		fetchLimitCtx = len(phase1ScoredCtx)
	}
	shortlistedCtx := phase1ScoredCtx[:fetchLimitCtx]

	// Log funnel filter rate metric (skip when limit=0, that's an intentional no-op)
	if limit > 0 && phase1LimitCtx > 0 {
		filterRateCtx := float64(phase1LimitCtx-fetchLimitCtx) / float64(phase1LimitCtx)
		log.Printf("[funnel/ctx] phase1=%d shortlisted=%d filter_rate=%.2f query=%q", phase1LimitCtx, fetchLimitCtx, filterRateCtx, queryText)
	}

	topIDsCtx := make([]string, len(shortlistedCtx))
	for i, c := range shortlistedCtx {
		topIDsCtx[i] = c.id
	}
	traceMap, err := g.GetTracesBatch(topIDsCtx)
	if err != nil {
		return nil, err
	}

	// Apply operational bias and assemble result
	for _, c := range shortlistedCtx {
		trace, ok := traceMap[c.id]
		if !ok || trace == nil {
			continue
		}
		act := c.activation
		if !isStatusQuery && trace.TraceType == TraceTypeOperational {
			act *= 0.5
		}
		trace.Activation = act
		result.Traces = append(result.Traces, trace)
	}

	// Re-sort after applying operational bias (may reorder results)
	sort.Slice(result.Traces, func(i, j int) bool {
		return result.Traces[i].Activation > result.Traces[j].Activation
	})

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

	// Post-sigmoid activation complete - distribution logging removed for cleaner logs

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

// isStatusQuery detects if the query is asking about recent work, status, or "what did I do"
// For these queries, operational traces (deploy logs, meeting reminders, etc.) ARE relevant
func isStatusQuery(queryText string) bool {
	if queryText == "" {
		return false
	}

	queryLower := strings.ToLower(queryText)

	// Keywords indicating status/recent work queries
	statusKeywords := []string{
		"what did", "what have", "what was",
		"recent", "recently", "today", "yesterday", "this week",
		"status", "progress", "working on", "worked on",
		"last", "latest", "current",
		"deployed", "restarted", "synced", "pushed", "committed",
		"meeting", "calendar", "scheduled",
	}

	for _, keyword := range statusKeywords {
		if strings.Contains(queryLower, keyword) {
			return true
		}
	}

	return false
}
