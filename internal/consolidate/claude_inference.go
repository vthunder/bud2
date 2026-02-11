package consolidate

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/executive"
	"github.com/vthunder/bud2/internal/graph"
)

// ClaudeInference provides Claude-powered relationship inference during consolidation.
// Uses RunCustomSession for stateless, non-Bud Claude sessions.
type ClaudeInference struct {
	model   string // e.g., "claude-sonnet-4-5"
	workDir string
	verbose bool
}

// NewClaudeInference creates a new Claude inference session
func NewClaudeInference(model string, workDir string, verbose bool) *ClaudeInference {
	if model == "" {
		model = "claude-sonnet-4-5" // Default to latest Sonnet
	}
	return &ClaudeInference{
		model:   model,
		workDir: workDir,
		verbose: verbose,
	}
}

// EpisodeForInference provides episode data for Claude inference
type EpisodeForInference interface {
	GetID() string
	GetShortID() string
	GetAuthor() string
	GetTimestamp() time.Time
	GetSummaryC16() string
}

// InferEpisodeEdges analyzes a batch of episodes and infers relationships between them.
// Returns a list of edges with semantic relationship descriptors.
func (c *ClaudeInference) InferEpisodeEdges(ctx context.Context, episodes []EpisodeForInference) ([]EpisodeEdge, error) {
	if len(episodes) == 0 {
		return nil, nil
	}

	// Build prompt for episode relationship inference
	prompt := c.buildEpisodeInferencePrompt(episodes)

	if c.verbose {
		log.Printf("[claude-inference] Sending %d episodes to Claude for edge inference", len(episodes))
		log.Printf("[claude-inference] Prompt length: %d chars, ~%d tokens", len(prompt), len(prompt)/4)
	}

	// Run custom Claude session
	cfg := executive.CustomSessionConfig{
		Model:   c.model,
		WorkDir: c.workDir,
		Verbose: c.verbose,
	}

	result := executive.RunCustomSession(ctx, prompt, cfg)
	if result.Error != nil {
		return nil, fmt.Errorf("claude inference failed: %w", result.Error)
	}

	// Parse JSON output
	var response struct {
		Edges []struct {
			FromID       string  `json:"from_id"`
			ToID         string  `json:"to_id"`
			Relationship string  `json:"relationship"`
			Confidence   float64 `json:"confidence"`
		} `json:"edges"`
	}

	// Extract JSON from markdown code blocks if present
	output := extractJSON(result.Output)
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		// Claude sometimes returns explanations instead of JSON
		// Log the issue but continue without edges for this batch
		if c.verbose {
			log.Printf("[claude-inference] Warning: Failed to parse JSON response, skipping batch")
			preview := result.Output
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			log.Printf("[claude-inference] Expected JSON format, got: %s", preview)
		}
		return nil, nil // Return empty edges rather than failing
	}

	// Convert to EpisodeEdge structs
	edges := make([]EpisodeEdge, 0, len(response.Edges))
	for _, e := range response.Edges {
		edges = append(edges, EpisodeEdge{
			FromID:       e.FromID,
			ToID:         e.ToID,
			Relationship: e.Relationship,
			Confidence:   e.Confidence,
		})
	}

	if c.verbose {
		log.Printf("[claude-inference] Inferred %d episode edges", len(edges))

		// Build episode data maps for display
		episodeSummaryMap := make(map[string]string)
		episodeShortIDMap := make(map[string]string)
		for _, ep := range episodes {
			episodeSummaryMap[ep.GetID()] = c.truncateSummary(ep.GetSummaryC16(), 8)
			episodeShortIDMap[ep.GetID()] = ep.GetShortID()
		}

		// Build a map of which episodes have outgoing edges
		hasOutgoingEdge := make(map[string]bool)
		for _, edge := range edges {
			hasOutgoingEdge[edge.FromID] = true
		}

		// Print episodes that don't link to anything first
		for _, ep := range episodes {
			if !hasOutgoingEdge[ep.GetID()] {
				shortID := episodeShortIDMap[ep.GetID()]
				summary := episodeSummaryMap[ep.GetID()]
				log.Printf("  [%s] %s", shortID, summary)
			}
		}

		// Print edges with episode summaries
		for _, edge := range edges {
			fromShort := episodeShortIDMap[edge.FromID]
			toShort := episodeShortIDMap[edge.ToID]
			fromSummary := episodeSummaryMap[edge.FromID]

			log.Printf("  [%s] %s -> %s: [%s]",
				fromShort, fromSummary, edge.Relationship, toShort)
		}
	}

	return edges, nil
}

// InferTraceRelationship analyzes an episode and a trace to determine their relationship.
// Returns a semantic descriptor and confidence score.
func (c *ClaudeInference) InferTraceRelationship(ctx context.Context, ep *graph.Episode, trace *graph.Trace) (string, float64, error) {
	prompt := c.buildTraceRelationshipPrompt(ep, trace)

	if c.verbose {
		log.Printf("[claude-inference] Analyzing episode-trace relationship")
	}

	cfg := executive.CustomSessionConfig{
		Model:   c.model,
		WorkDir: c.workDir,
		Verbose: c.verbose,
	}

	result := executive.RunCustomSession(ctx, prompt, cfg)
	if result.Error != nil {
		return "", 0, fmt.Errorf("claude inference failed: %w", result.Error)
	}

	var response struct {
		Relationship string  `json:"relationship"`
		Confidence   float64 `json:"confidence"`
	}

	output := extractJSON(result.Output)
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		return "", 0, fmt.Errorf("failed to parse inference result: %w\nOutput: %s", err, result.Output)
	}

	return response.Relationship, response.Confidence, nil
}

// buildEpisodeInferencePrompt constructs the prompt for episode relationship inference
func (c *ClaudeInference) buildEpisodeInferencePrompt(episodes []EpisodeForInference) string {
	var sb strings.Builder

	sb.WriteString(`You are analyzing conversation episodes to identify semantic relationships between them.

For each pair of related episodes, determine:
1. The semantic relationship (e.g., "elaborates on", "answers", "asks about", "follows up on", "contradicts", "agrees with")
2. Confidence score (0.0-1.0)

Only return relationships with confidence >= 0.7.

Episodes (16-word summaries):
`)

	for _, ep := range episodes {
		author := ep.GetAuthor()
		if author == "" {
			author = "unknown"
		}
		sb.WriteString(fmt.Sprintf("\nID: %s\n", ep.GetID()))
		sb.WriteString(fmt.Sprintf("Author: %s\n", author))
		sb.WriteString(fmt.Sprintf("Timestamp: %s\n", ep.GetTimestamp().Format("2006-01-02 15:04:05")))

		// Use C16 summary (16-word compression) for efficient token usage
		// Note: Caller is responsible for filtering episodes with C16 available
		sb.WriteString(fmt.Sprintf("Summary: %s\n", ep.GetSummaryC16()))
	}

	sb.WriteString(`
Return your analysis as JSON:

{
  "edges": [
    {
      "from_id": "episode-123",
      "to_id": "episode-456",
      "relationship": "asks about",
      "confidence": 0.9
    }
  ]
}

Link episodes that:
- Are part of the same conversation turn (same author, close in time)
- Discuss the same specific event or topic
- One elaborates on, responds to, or continues the other

Use high confidence (0.8+) for same-author sequential episodes about the same topic.
Use medium confidence (0.6-0.7) for cross-author semantic relationships.
`)

	return sb.String()
}

// buildTraceRelationshipPrompt constructs the prompt for episode-trace relationship inference
func (c *ClaudeInference) buildTraceRelationshipPrompt(ep *graph.Episode, trace *graph.Trace) string {
	var sb strings.Builder

	sb.WriteString(`You are analyzing the relationship between a new conversation episode and an existing memory trace.

Determine:
1. The semantic relationship (e.g., "provides example of", "updates", "contradicts", "reinforces", "relates to")
2. Confidence score (0.0-1.0)

Episode:
`)

	author := ep.Author
	if author == "" {
		author = "unknown"
	}
	sb.WriteString(fmt.Sprintf("Author: %s\n", author))
	sb.WriteString(fmt.Sprintf("Timestamp: %s\n", ep.TimestampEvent.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("Content: %s\n\n", ep.Content))

	sb.WriteString(fmt.Sprintf(`Memory Trace:
Summary: %s

Return your analysis as JSON:

{
  "relationship": "provides example of",
  "confidence": 0.85
}

If there's no meaningful relationship, set confidence to 0.0.
`, trace.Summary))

	return sb.String()
}

// extractJSON extracts JSON from markdown code blocks or returns the input if no code block found
func extractJSON(s string) string {
	// Look for ```json or ``` code blocks
	if start := strings.Index(s, "```json"); start != -1 {
		start += 7 // Skip past ```json
		if end := strings.Index(s[start:], "```"); end != -1 {
			return strings.TrimSpace(s[start : start+end])
		}
	}
	if start := strings.Index(s, "```"); start != -1 {
		start += 3 // Skip past ```
		if end := strings.Index(s[start:], "```"); end != -1 {
			content := strings.TrimSpace(s[start : start+end])
			// Skip language identifier line if present
			if idx := strings.Index(content, "\n"); idx != -1 {
				content = content[idx+1:]
			}
			return strings.TrimSpace(content)
		}
	}
	return strings.TrimSpace(s)
}

// EpisodeEdge represents a relationship between two episodes
type EpisodeEdge struct {
	FromID       string
	ToID         string
	Relationship string  // Freeform semantic descriptor
	Confidence   float64 // 0.0-1.0
}

// truncateSummary truncates a summary to approximately N words
func (c *ClaudeInference) truncateSummary(summary string, maxWords int) string {
	words := strings.Fields(summary)
	if len(words) <= maxWords {
		return summary
	}
	return strings.Join(words[:maxWords], " ") + "..."
}
