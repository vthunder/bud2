// Package consolidate handles memory consolidation - grouping related episodes
// into consolidated traces with LLM-generated summaries.
package consolidate

import (
	"fmt"
	"log"
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
}

// Consolidator handles memory consolidation
type Consolidator struct {
	graph  *graph.DB
	llm    LLMClient

	// Configuration
	TimeWindow   time.Duration // Max time span for grouping (default 30 min)
	MinGroupSize int           // Minimum episodes to form a group (default 1)
	MaxGroupSize int           // Maximum episodes per group (default 10)
}

// NewConsolidator creates a new consolidator
func NewConsolidator(g *graph.DB, llm LLMClient) *Consolidator {
	return &Consolidator{
		graph:        g,
		llm:          llm,
		TimeWindow:   30 * time.Minute,
		MinGroupSize: 1,
		MaxGroupSize: 10,
	}
}

// episodeGroup represents a group of related episodes to consolidate
type episodeGroup struct {
	episodes  []*graph.Episode
	entityIDs map[string]bool // union of all entity IDs
}

// Run consolidates unconsolidated episodes into traces.
// Returns the number of traces created.
func (c *Consolidator) Run() (int, error) {
	// Get episodes that haven't been consolidated yet
	episodes, err := c.graph.GetUnconsolidatedEpisodes(500)
	if err != nil {
		return 0, fmt.Errorf("failed to get unconsolidated episodes: %w", err)
	}

	if len(episodes) == 0 {
		return 0, nil
	}

	log.Printf("[consolidate] Found %d unconsolidated episodes", len(episodes))

	// Group episodes
	groups := c.groupEpisodes(episodes)
	log.Printf("[consolidate] Formed %d groups", len(groups))

	// Create traces for each group
	created := 0
	for i, group := range groups {
		if err := c.consolidateGroup(group, i); err != nil {
			log.Printf("[consolidate] Failed to consolidate group %d: %v", i, err)
			continue
		}
		created++
	}

	return created, nil
}

// groupEpisodes groups related episodes together based on:
// 1. Time proximity (within TimeWindow)
// 2. Conversation thread (same channel + entity overlap)
//
// Embedding similarity is deliberately NOT used for grouping — it risks
// merging topically-similar but contextually-separate conversations (e.g.
// "coffee in Berlin" with Jane and "coffee in Tokyo" with Bob). Topical
// connections are handled at retrieval time via entity-bridged spreading
// activation instead.
func (c *Consolidator) groupEpisodes(episodes []*graph.Episode) []*episodeGroup {
	if len(episodes) == 0 {
		return nil
	}

	// Sort by timestamp
	sort.Slice(episodes, func(i, j int) bool {
		return episodes[i].TimestampEvent.Before(episodes[j].TimestampEvent)
	})

	// Get entities for each episode
	episodeEntities := make(map[string]map[string]bool)
	for _, ep := range episodes {
		entities, _ := c.graph.GetEpisodeEntities(ep.ID)
		entitySet := make(map[string]bool)
		for _, e := range entities {
			entitySet[e] = true
		}
		episodeEntities[ep.ID] = entitySet
	}

	var groups []*episodeGroup
	used := make(map[string]bool)

	for _, ep := range episodes {
		if used[ep.ID] {
			continue
		}

		// Start a new group with this episode
		group := &episodeGroup{
			episodes:  []*graph.Episode{ep},
			entityIDs: make(map[string]bool),
		}
		for e := range episodeEntities[ep.ID] {
			group.entityIDs[e] = true
		}
		used[ep.ID] = true

		// Try to add related episodes to this group
		for _, candidate := range episodes {
			if used[candidate.ID] {
				continue
			}

			if len(group.episodes) >= c.MaxGroupSize {
				break
			}

			// Check time proximity
			timeDiff := candidate.TimestampEvent.Sub(ep.TimestampEvent)
			if timeDiff < 0 {
				timeDiff = -timeDiff
			}
			if timeDiff > c.TimeWindow {
				continue
			}

			// Check conversation thread: same channel or entity overlap
			sameChannel := ep.Channel != "" && ep.Channel == candidate.Channel

			candidateEntities := episodeEntities[candidate.ID]
			hasEntityOverlap := false
			for e := range candidateEntities {
				if group.entityIDs[e] {
					hasEntityOverlap = true
					break
				}
			}

			// Add to group if in same conversation thread
			if sameChannel || hasEntityOverlap {
				group.episodes = append(group.episodes, candidate)
				for e := range candidateEntities {
					group.entityIDs[e] = true
				}
				used[candidate.ID] = true
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
			log.Printf("[consolidate] Summarization failed, using truncation: %v", err)
			summary = truncate(strings.Join(fragments, " "), 300)
		}
	} else {
		summary = truncate(strings.Join(fragments, " "), 300)
	}

	// Format summary with [Past] prefix for memory context
	if !strings.HasPrefix(summary, "[Past]") {
		// If single author, include it
		if len(group.episodes) == 1 && group.episodes[0].Author != "" {
			summary = fmt.Sprintf("[Past] %s: %s", group.episodes[0].Author, summary)
		} else {
			summary = "[Past] " + summary
		}
	}

	// Skip ephemeral/low-value content that shouldn't become long-term memories
	if isEphemeralContent(summary) || isAllLowInfo(group.episodes) {
		// Link episodes to sentinel trace so they aren't retried by GetUnconsolidatedEpisodes
		for _, ep := range group.episodes {
			c.graph.LinkTraceToSource("_ephemeral", ep.ID)
		}
		log.Printf("[consolidate] Skipped low-value content (%d episodes): %s",
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
		Activation: 0.8,
		Strength:   len(group.episodes), // Strength based on number of source episodes
		IsCore:     false,
		Embedding:  embedding,
		CreatedAt:  time.Now(),
	}

	if err := c.graph.AddTrace(trace); err != nil {
		return fmt.Errorf("failed to add trace: %w", err)
	}

	// Link trace to all source episodes
	for _, ep := range group.episodes {
		if err := c.graph.LinkTraceToSource(traceID, ep.ID); err != nil {
			log.Printf("[consolidate] Failed to link trace to episode %s: %v", ep.ID, err)
		}
	}

	// Link trace to all entities
	for entityID := range group.entityIDs {
		if err := c.graph.LinkTraceToEntity(traceID, entityID); err != nil {
			log.Printf("[consolidate] Failed to link trace to entity %s: %v", entityID, err)
		}
	}

	typeLabel := ""
	if traceType == graph.TraceTypeOperational {
		typeLabel = " [operational]"
	}
	log.Printf("[consolidate] Created trace %s from %d episodes%s: %s",
		traceID, len(group.episodes), typeLabel, truncate(summary, 80))

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
