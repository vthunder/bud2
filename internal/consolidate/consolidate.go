// Package consolidate handles memory consolidation - grouping related episodes
// into consolidated traces with LLM-generated summaries.
package consolidate

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/filter"
	"github.com/vthunder/bud2/internal/graph"
)

// LLMClient provides embedding and summarization capabilities
type LLMClient interface {
	Embed(text string) ([]float64, error)
	Summarize(fragments []string) (string, error)
	Generate(prompt string) (string, error) // For pyramid summary generation
}

// Consolidator handles memory consolidation
type Consolidator struct {
	graph  *graph.DB
	llm    LLMClient

	// Configuration
	TimeWindow   time.Duration // Max time span for grouping (default 30 min)
	MinGroupSize int           // Minimum episodes to form a group (default 1)
	MaxGroupSize int           // Maximum episodes per group (default 10)

	// Claude inference for relationship linking
	claude *ClaudeInference

	// Episode-episode sliding window configuration
	episodeBatchSize    int     // Batch size for sliding window (default 20)
	episodeBatchOverlap float64 // Overlap ratio for sliding window (default 0.5 = 50%)
}

// NewConsolidator creates a new consolidator
func NewConsolidator(g *graph.DB, llm LLMClient, claude *ClaudeInference) *Consolidator {
	return &Consolidator{
		graph:               g,
		llm:                 llm,
		claude:              claude,
		TimeWindow:          30 * time.Minute,
		MinGroupSize:        1,
		MaxGroupSize:        10,
		episodeBatchSize:    20,
		episodeBatchOverlap: 0.5,
	}
}


// episodeGroup represents a group of related episodes to consolidate
type episodeGroup struct {
	episodes  []*graph.Episode
	entityIDs map[string]bool // union of all entity IDs
}

// Run consolidates unconsolidated episodes into traces.
// Returns the number of traces created.
//
// Architecture:
// Phase 1: Claude infers episode-episode edges (sliding window per channel)
// Phase 2: Graph clustering using those edges → episode groups
// Phase 3: Create traces from clustered groups
func (c *Consolidator) Run() (int, error) {
	totalCreated := 0

	// Process episodes in batches until all are consolidated
	for {
		// Get episodes that haven't been consolidated yet
		episodes, err := c.graph.GetUnconsolidatedEpisodes(500)
		if err != nil {
			return totalCreated, fmt.Errorf("failed to get unconsolidated episodes: %w", err)
		}

		if len(episodes) == 0 {
			return totalCreated, nil
		}

		log.Printf("Found %d unconsolidated episodes", len(episodes))

		ctx := context.Background()

		// Phase 0: Detect near-duplicate episodes using C16 summary similarity
		log.Printf("Phase 0: Detecting near-duplicate episodes...")
		duplicateEdges := c.detectDuplicateEpisodes(episodes)
		log.Printf("Found %d duplicate episode pairs", len(duplicateEdges))

		// Phase 1: Claude infers episode-episode relationships
		log.Printf("Phase 1: Claude inference for episode-episode relationships...")
		episodeEdges, err := c.inferEpisodeEpisodeLinks(ctx, episodes)
		if err != nil {
			log.Printf("Failed to infer episode edges: %v", err)
			// Continue anyway - we can still try clustering with no edges
		}

		log.Printf("Inferred %d episode-episode edges", len(episodeEdges))

		// Merge duplicate edges with inferred edges
		episodeEdges = append(duplicateEdges, episodeEdges...)
		log.Printf("Total edges (duplicates + inferred): %d", len(episodeEdges))

		// Print edge summaries in verbose mode
		if c.claude != nil && c.claude.verbose {
			c.printEdgeSummaries(episodes, episodeEdges)
		}

		// Store edges in database
		for _, edge := range episodeEdges {
			if err := c.graph.AddEpisodeEpisodeEdge(edge.FromID, edge.ToID, "RELATED_TO", edge.Relationship, edge.Confidence); err != nil {
				log.Printf("Failed to add episode edge: %v", err)
			}
		}

		// Phase 2: Graph clustering using Claude-inferred edges
		log.Printf("Phase 2: Clustering episodes using inferred edges...")
		groups := c.clusterEpisodesByEdges(episodes, episodeEdges)
		log.Printf("Formed %d clusters", len(groups))

		// Phase 3: Create traces from clustered groups
		log.Printf("Phase 3: Creating traces from clusters...")
		created := 0
		for i, group := range groups {
			if err := c.consolidateGroup(group, i); err != nil {
				log.Printf("Failed to consolidate group %d: %v", i, err)
				continue
			}
			created++
		}

		totalCreated += created

		// If we processed fewer than 500 episodes, we're done
		if len(episodes) < 500 {
			return totalCreated, nil
		}
	}
}

// clusterEpisodesByEdges uses Claude-inferred edges to cluster episodes into groups.
// Uses a simple connected components algorithm on high-confidence edges.
func (c *Consolidator) clusterEpisodesByEdges(episodes []*graph.Episode, edges []EpisodeEdge) []*episodeGroup {
	if len(episodes) == 0 {
		return nil
	}

	// Build episode ID -> episode map
	episodeMap := make(map[string]*graph.Episode)
	for _, ep := range episodes {
		episodeMap[ep.ID] = ep
	}

	// Build adjacency list from high-confidence edges (confidence >= 0.7)
	adjacency := make(map[string][]string)
	for _, edge := range edges {
		if edge.Confidence >= 0.7 {
			adjacency[edge.FromID] = append(adjacency[edge.FromID], edge.ToID)
			adjacency[edge.ToID] = append(adjacency[edge.ToID], edge.FromID)
		}
	}

	// Find connected components using DFS
	visited := make(map[string]bool)
	var groups []*episodeGroup

	var dfs func(episodeID string, group *episodeGroup)
	dfs = func(episodeID string, group *episodeGroup) {
		if visited[episodeID] {
			return
		}
		visited[episodeID] = true

		ep, exists := episodeMap[episodeID]
		if !exists {
			return
		}

		group.episodes = append(group.episodes, ep)

		// Visit neighbors
		for _, neighborID := range adjacency[episodeID] {
			dfs(neighborID, group)
		}
	}

	// Process each episode
	for _, ep := range episodes {
		if visited[ep.ID] {
			continue
		}

		group := &episodeGroup{
			episodes:  []*graph.Episode{},
			entityIDs: make(map[string]bool),
		}

		dfs(ep.ID, group)

		// Collect entities from all episodes in group
		for _, e := range group.episodes {
			entities, _ := c.graph.GetEpisodeEntities(e.ID)
			for _, entityID := range entities {
				group.entityIDs[entityID] = true
			}
		}

		if len(group.episodes) >= c.MinGroupSize {
			groups = append(groups, group)
		}
	}

	return groups
}

// consolidateGroup creates a trace from a group of episodes
func (c *Consolidator) consolidateGroup(group *episodeGroup, index int) error {
	// Build fragments for summarization
	var fragments []string
	for _, ep := range group.episodes {
		prefix := ""
		if ep.Author != "" {
			prefix = ep.Author + ": "
		}
		fragments = append(fragments, prefix+ep.Content)
	}

	// Generate summary - always use LLM for proper memory format
	var summary string
	var err error

	if c.llm != nil {
		summary, err = c.llm.Summarize(fragments)
		if err != nil {
			log.Printf("Summarization failed, using truncation: %v", err)
			summary = truncate(strings.Join(fragments, " "), 300)
		}
	} else {
		summary = truncate(strings.Join(fragments, " "), 300)
	}

	// Remove [Past] prefix if present (legacy)
	summary = strings.TrimPrefix(summary, "[Past] ")

	// Skip ephemeral/low-value content that shouldn't become long-term memories
	if isEphemeralContent(summary) || isAllLowInfo(group.episodes) {
		// Link episodes to sentinel trace so they aren't retried by GetUnconsolidatedEpisodes
		for _, ep := range group.episodes {
			c.graph.LinkTraceToSource("_ephemeral", ep.ID)
		}
		log.Printf("Skipped low-value content (%d episodes): %s",
			len(group.episodes), truncate(summary, 80))
		return nil
	}

	// Generate trace ID
	idSuffix := group.episodes[0].ID
	if len(idSuffix) > 8 {
		idSuffix = idSuffix[:8]
	}
	traceID := fmt.Sprintf("trace-%d-%s", time.Now().UnixNano(), idSuffix)

	// Calculate embedding
	var embedding []float64
	if c.llm != nil {
		embedding, _ = c.llm.Embed(summary)
	}
	if len(embedding) == 0 {
		embedding = calculateCentroid(group.episodes)
	}

	// Classify trace type
	traceType := classifyTraceType(summary, group.episodes)

	// Create trace
	trace := &graph.Trace{
		ID:         traceID,
		Summary:    summary,
		Topic:      "conversation",
		TraceType:  traceType,
		Activation: 0.1, // Start at floor, let spreading activation boost if relevant
		Strength:   len(group.episodes), // Strength based on number of source episodes
		Embedding:  embedding,
		CreatedAt:  time.Now(),
	}

	if err := c.graph.AddTrace(trace); err != nil {
		return fmt.Errorf("failed to add trace: %w", err)
	}

	// Link trace to all source episodes
	for _, ep := range group.episodes {
		if err := c.graph.LinkTraceToSource(traceID, ep.ID); err != nil {
			log.Printf("Failed to link trace to episode %s: %v", ep.ID, err)
		}
	}

	// Link trace to all entities
	for entityID := range group.entityIDs {
		if err := c.graph.LinkTraceToEntity(traceID, entityID); err != nil {
			log.Printf("Failed to link trace to entity %s: %v", entityID, err)
		}
	}

	// Generate pyramid summaries (L64→L32→L16→L8→L4) from source episodes
	if c.llm != nil {
		log.Printf("DEBUG: Generating pyramid summaries for trace %s from %d episodes", trace.ShortID, len(group.episodes))
		if err := c.graph.GenerateTracePyramid(traceID, group.episodes, c.llm); err != nil {
			log.Printf("Failed to generate pyramid summaries for trace %s: %v", trace.ShortID, err)
		} else {
			log.Printf("DEBUG: Successfully generated pyramid summaries for trace %s", trace.ShortID)
		}
	} else {
		log.Printf("DEBUG: Skipping pyramid generation for trace %s - c.llm is nil", trace.ShortID)
	}

	// Link to similar traces (>0.85 similarity)
	if len(embedding) > 0 {
		if linked := c.linkToSimilarTraces(traceID, embedding, 0.85); linked > 0 {
			log.Printf("Linked trace %s to %d similar traces", trace.ShortID, linked)
		}
	}

	typeLabel := ""
	if traceType == graph.TraceTypeOperational {
		typeLabel = " [operational]"
	}
	log.Printf("Created trace %s from %d episodes%s: %s",
		trace.ShortID, len(group.episodes), typeLabel, truncate(summary, 80))

	return nil
}

// calculateCentroid computes the centroid embedding from multiple episodes
func calculateCentroid(episodes []*graph.Episode) []float64 {
	if len(episodes) == 0 {
		return nil
	}

	// Find first episode with embedding to determine dimension
	var dim int
	for _, ep := range episodes {
		if len(ep.Embedding) > 0 {
			dim = len(ep.Embedding)
			break
		}
	}
	if dim == 0 {
		return nil
	}

	// Calculate mean
	centroid := make([]float64, dim)
	count := 0
	for _, ep := range episodes {
		if len(ep.Embedding) == dim {
			for i, v := range ep.Embedding {
				centroid[i] += v
			}
			count++
		}
	}

	if count == 0 {
		return nil
	}

	for i := range centroid {
		centroid[i] /= float64(count)
	}

	return centroid
}

// truncate shortens text to maxLen, adding ellipsis if needed
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// isAllLowInfo returns true if every episode in the group is a backchannel or
// greeting (e.g., "ok", "great", "hi"). Uses the dialogue_act field if set,
// otherwise falls back to content-based classification.
func isAllLowInfo(episodes []*graph.Episode) bool {
	if len(episodes) == 0 {
		return true
	}
	for _, ep := range episodes {
		act := ep.DialogueAct
		if act == "" {
			// Fallback: classify content directly
			act = string(filter.ClassifyDialogueAct(ep.Content))
		}
		if act != string(filter.ActBackchannel) && act != string(filter.ActGreeting) {
			return false
		}
	}
	return true
}

// classifyTraceType determines whether a trace is operational (transient system
// activity) or knowledge (facts, decisions, preferences worth remembering).
// Operational traces decay 3x faster during activation decay.
func classifyTraceType(summary string, episodes []*graph.Episode) graph.TraceType {
	lower := strings.ToLower(summary)

	// Meeting reminders and calendar notifications
	// Check for calendar notification patterns (starts/starting soon/in, Google Meet links)
	isMeetingReminder := strings.Contains(lower, "upcoming meeting") ||
		strings.Contains(lower, "start soon") || // Covers "starts soon", "start soon", "starting soon"
		strings.Contains(lower, "starts in") && (strings.Contains(lower, "m") || strings.Contains(lower, "minute")) ||
		strings.Contains(lower, "meeting starts") ||
		strings.Contains(lower, "heads up") && (strings.Contains(lower, "meeting") || strings.Contains(lower, "sprint") || strings.Contains(lower, "planning")) ||
		strings.Contains(lower, "meet.google.com") ||
		// Sprint planning notifications (even without "meeting" word)
		strings.Contains(lower, "sprint planning") && (strings.Contains(lower, "starts") || strings.Contains(lower, "soon") || strings.Contains(lower, "in "))
	if isMeetingReminder && !strings.Contains(lower, "discussed") && !strings.Contains(lower, "decided") {
		return graph.TraceTypeOperational
	}

	// State sync / deployment / restart activity
	if strings.Contains(lower, "state sync") || strings.Contains(lower, "synced state") ||
		strings.Contains(lower, "restarted") && !strings.Contains(lower, "because") ||
		strings.Contains(lower, "launchd service") && strings.Contains(lower, "running") ||
		strings.Contains(lower, "rebuilt binaries") ||
		strings.Contains(lower, "deployed") && !strings.Contains(lower, "decision") {
		return graph.TraceTypeOperational
	}

	// Autonomous wake confirmations / idle wakes
	if strings.Contains(lower, "no actionable work") ||
		strings.Contains(lower, "idle wake") ||
		strings.Contains(lower, "wellness check") && !strings.Contains(lower, "finding") {
		return graph.TraceTypeOperational
	}

	// Pure acknowledgments without substantive content
	if strings.Contains(lower, "confirmed") && !strings.Contains(lower, "decision") &&
		!strings.Contains(lower, "preference") && len(summary) < 150 {
		return graph.TraceTypeOperational
	}

	// Dev work implementation notes without decision rationale
	// These are status updates about work done, not learnings or decisions
	if isDevWorkNote(lower) && !hasKnowledgeIndicator(lower) {
		return graph.TraceTypeOperational
	}

	return graph.TraceTypeKnowledge
}

// isDevWorkNote checks if the summary appears to be a dev work status update
func isDevWorkNote(lower string) bool {
	// Past-tense implementation verbs
	devVerbs := []string{
		"updated ", "implemented ", "fixed ", "added ", "created ",
		"refactored ", "removed ", "deleted ", "modified ", "changed ",
		"wrote ", "built ", "expanded ", "pruned ", "wired ",
		"researched ", "explored ", "investigated ", "analyzed ",
	}
	for _, verb := range devVerbs {
		if strings.Contains(lower, verb) {
			return true
		}
	}
	return false
}

// hasKnowledgeIndicator checks if the summary contains decision rationale or learnings
func hasKnowledgeIndicator(lower string) bool {
	indicators := []string{
		"decided", "because", "reason", "chose", "choice",
		"approach", "prefer", "finding", "learned", "discovered",
		"root cause", "conclusion", "insight", "realized",
		"will use", "should use", "plan to", "strategy",
	}
	for _, indicator := range indicators {
		if strings.Contains(lower, indicator) {
			return true
		}
	}
	return false
}

// isEphemeralContent returns true if the summary represents transient content
// that shouldn't be stored as a long-term memory trace.
func isEphemeralContent(summary string) bool {
	lower := strings.ToLower(summary)

	// Meeting countdown reminders ("X minutes and Y seconds")
	if strings.Contains(lower, "minutes and") && strings.Contains(lower, "seconds") {
		return true
	}

	// "starting in X minutes" without meaningful context
	if strings.Contains(lower, "starting in") && strings.Contains(lower, "minutes") &&
		len(summary) < 200 {
		return true
	}

	// "starts in X minutes" variant
	if strings.Contains(lower, "starts in") && strings.Contains(lower, "minutes") &&
		len(summary) < 200 {
		return true
	}

	return false
}

// linkToSimilarTraces finds existing traces with high similarity and creates SIMILAR_TO edges.
// Returns the number of edges created.
func (c *Consolidator) linkToSimilarTraces(traceID string, embedding []float64, threshold float64) int {
	similar, err := c.graph.FindSimilarTracesAboveThreshold(embedding, threshold, traceID)
	if err != nil {
		log.Printf("Failed to find similar traces: %v", err)
		return 0
	}

	linked := 0
	for _, s := range similar {
		err := c.graph.AddTraceRelation(traceID, s.ID, graph.EdgeSimilarTo, s.Similarity)
		if err == nil {
			linked++
		}
	}
	return linked
}

// cosineSimilarity computes cosine similarity between two embeddings
func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
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

// detectDuplicateEpisodes finds near-duplicate episodes using C16 summary similarity.
// Returns high-confidence edges (1.0) for episodes with similarity > 0.95.
// This catches obvious duplicates that Claude inference might miss.
func (c *Consolidator) detectDuplicateEpisodes(episodes []*graph.Episode) []EpisodeEdge {
	if len(episodes) < 2 {
		return nil
	}

	// Load C16 summaries for all episodes
	episodeIDs := make([]string, len(episodes))
	for i, ep := range episodes {
		episodeIDs[i] = ep.ID
	}

	summaries, err := c.graph.GetEpisodeSummariesBatch(episodeIDs, graph.CompressionLevel16)
	if err != nil {
		log.Printf("Failed to load C16 summaries for duplicate detection: %v", err)
		return nil
	}

	// Build map of episode ID -> embedding (use episode embedding as proxy for C16)
	type episodeWithEmbedding struct {
		ep        *graph.Episode
		embedding []float64
	}

	var withEmbeddings []episodeWithEmbedding
	for _, ep := range episodes {
		if len(ep.Embedding) > 0 {
			withEmbeddings = append(withEmbeddings, episodeWithEmbedding{
				ep:        ep,
				embedding: ep.Embedding,
			})
		}
	}

	if len(withEmbeddings) < 2 {
		return nil
	}

	// Compare all pairs using cosine similarity
	var duplicateEdges []EpisodeEdge
	threshold := 0.95 // Very high threshold to catch only near-duplicates

	for i := 0; i < len(withEmbeddings); i++ {
		for j := i + 1; j < len(withEmbeddings); j++ {
			ep1 := withEmbeddings[i]
			ep2 := withEmbeddings[j]

			similarity := cosineSimilarity(ep1.embedding, ep2.embedding)
			if similarity >= threshold {
				// Check C16 summaries if available for additional validation
				summary1, ok1 := summaries[ep1.ep.ID]
				summary2, ok2 := summaries[ep2.ep.ID]

				if ok1 && ok2 && summary1 != nil && summary2 != nil {
					// Both have C16 summaries - verify they're actually similar
					if !strings.Contains(strings.ToLower(summary1.Summary), strings.ToLower(summary2.Summary[:min(len(summary2.Summary), 20)])) &&
					   !strings.Contains(strings.ToLower(summary2.Summary), strings.ToLower(summary1.Summary[:min(len(summary1.Summary), 20)])) {
						// Embeddings similar but content different - skip
						continue
					}
				}

				duplicateEdges = append(duplicateEdges, EpisodeEdge{
					FromID:       ep1.ep.ID,
					ToID:         ep2.ep.ID,
					Relationship: "duplicate_of",
					Confidence:   similarity,
				})
			}
		}
	}

	return duplicateEdges
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// inferEpisodeEpisodeLinks uses Claude to infer semantic relationships between episodes
// using a sliding window approach with 50% overlap to achieve O(kn) complexity instead of O(n²)
func (c *Consolidator) inferEpisodeEpisodeLinks(ctx context.Context, episodes []*graph.Episode) ([]EpisodeEdge, error) {
	if len(episodes) == 0 {
		return nil, nil
	}

	// Sort episodes by timestamp to ensure temporal ordering
	sorted := make([]*graph.Episode, len(episodes))
	copy(sorted, episodes)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].TimestampEvent.Before(sorted[j].TimestampEvent)
	})

	// Load C16 summaries for all episodes
	episodeIDs := make([]string, len(sorted))
	for i, ep := range sorted {
		episodeIDs[i] = ep.ID
	}

	summaries, err := c.graph.GetEpisodeSummariesBatch(episodeIDs, graph.CompressionLevel16)
	if err != nil {
		log.Printf("Failed to load C16 summaries: %v", err)
		return nil, err
	}

	// Create enriched episodes struct with summary
	type enrichedEpisode struct {
		*graph.Episode
		summaryC16 string
	}

	// Filter episodes with C16 summaries available
	var withSummaries []*enrichedEpisode
	for _, ep := range sorted {
		if summary, ok := summaries[ep.ID]; ok && summary != nil {
			withSummaries = append(withSummaries, &enrichedEpisode{
				Episode:    ep,
				summaryC16: summary.Summary,
			})
		}
	}

	if len(withSummaries) == 0 {
		log.Printf("No episodes with C16 summaries available, skipping edge inference")
		return nil, nil
	}

	// Calculate sliding window parameters
	batchSize := c.episodeBatchSize
	stepSize := int(float64(batchSize) * (1.0 - c.episodeBatchOverlap))
	if stepSize < 1 {
		stepSize = 1
	}

	log.Printf("Using sliding window: batch_size=%d, step_size=%d, total_episodes=%d",
		batchSize, stepSize, len(withSummaries))

	// Process episodes in sliding windows
	var allEdges []EpisodeEdge
	for start := 0; start < len(withSummaries); start += stepSize {
		end := start + batchSize
		if end > len(withSummaries) {
			end = len(withSummaries)
		}

		enrichedBatch := withSummaries[start:end]
		if len(enrichedBatch) < 2 {
			break // Need at least 2 episodes to infer edges
		}

		// Create episodesWithSummary slice for Claude
		episodesForInference := make([]EpisodeForInference, len(enrichedBatch))
		for i, e := range enrichedBatch {
			episodesForInference[i] = &episodeWithSummary{
				Episode:    e.Episode,
				summaryC16: e.summaryC16,
			}
		}

		log.Printf("Processing batch %d-%d (%d episodes)",
			start, end-1, len(episodesForInference))

		// Infer edges for this batch using Claude
		edges, err := c.claude.InferEpisodeEdges(ctx, episodesForInference)
		if err != nil {
			log.Printf("Failed to infer edges for batch %d-%d: %v", start, end-1, err)
			continue
		}

		allEdges = append(allEdges, edges...)

		// Stop if we've reached the end
		if end == len(withSummaries) {
			break
		}
	}

	return allEdges, nil
}

// episodeWithSummary wraps an Episode with its C16 summary for inference
type episodeWithSummary struct {
	*graph.Episode
	summaryC16 string
}

// Interface implementation for EpisodeForInference
func (e *episodeWithSummary) GetID() string {
	return e.Episode.ID
}

func (e *episodeWithSummary) GetAuthor() string {
	return e.Episode.Author
}

func (e *episodeWithSummary) GetTimestamp() time.Time {
	return e.Episode.TimestampEvent
}

func (e *episodeWithSummary) GetSummaryC16() string {
	return e.summaryC16
}

// printEdgeSummaries prints episode edge summaries in verbose mode
// Format: [id] 8-word summary -> relationship: [other-id]
func (c *Consolidator) printEdgeSummaries(episodes []*graph.Episode, edges []EpisodeEdge) {
	// Build episode ID -> episode map for quick lookup
	episodeMap := make(map[string]*graph.Episode)
	for _, ep := range episodes {
		episodeMap[ep.ID] = ep
	}

	// Load C8 summaries for all episodes
	episodeIDs := make([]string, len(episodes))
	for i, ep := range episodes {
		episodeIDs[i] = ep.ID
	}

	summaries, err := c.graph.GetEpisodeSummariesBatch(episodeIDs, graph.CompressionLevel8)
	if err != nil {
		log.Printf("Failed to load C8 summaries for edge display: %v", err)
		return
	}

	// Build edge map: fromID -> []edge
	edgeMap := make(map[string][]EpisodeEdge)
	for _, edge := range edges {
		edgeMap[edge.FromID] = append(edgeMap[edge.FromID], edge)
	}

	log.Printf("\n=== Episode Edge Summary ===")

	// Print each episode and its outgoing edges
	for _, ep := range episodes {
		shortID := ep.ID
		if len(shortID) > 5 {
			shortID = shortID[len(shortID)-5:]
		}

		// Get C8 summary (8 words) for display
		summary := ep.Content
		if s, ok := summaries[ep.ID]; ok && s != nil {
			summary = s.Summary
		} else {
			// Fallback: truncate content to approximately 8 words
			words := strings.Fields(summary)
			if len(words) > 8 {
				summary = strings.Join(words[:8], " ")
			}
		}

		// Truncate summary to fit display
		if len(summary) > 60 {
			summary = summary[:60] + "..."
		}

		// Check if this episode has outgoing edges
		outEdges := edgeMap[ep.ID]
		if len(outEdges) == 0 {
			// No outgoing edges
			log.Printf("[%s] %s", shortID, summary)
		} else {
			// Print with edges
			for i, edge := range outEdges {
				targetShortID := edge.ToID
				if len(targetShortID) > 5 {
					targetShortID = targetShortID[len(targetShortID)-5:]
				}

				if i == 0 {
					log.Printf("[%s] %s -> %s: [%s]",
						shortID, summary, edge.Relationship, targetShortID)
				} else {
					// Continuation line for multiple edges from same episode
					log.Printf("       %*s -> %s: [%s]",
						len(summary), "", edge.Relationship, targetShortID)
				}
			}
		}
	}

	log.Printf("=== End Edge Summary ===\n")
}

