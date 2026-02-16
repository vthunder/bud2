// Package consolidate handles memory consolidation - grouping related episodes
// into consolidated traces with LLM-generated summaries.
//
// This file provides the LLM inference interface for episode edge detection.
// Unlike the original consolidation which used Claude directly, this standalone
// version accepts any LLM that implements the InferenceClient interface.
package consolidate

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vthunder/bud2/memory-service/pkg/graph"
)

// InferenceClient abstracts LLM inference for edge detection.
// Implementations can use Claude, OpenAI, Ollama, or any other LLM.
type InferenceClient interface {
	// Infer sends a prompt and returns the LLM's text response.
	Infer(ctx context.Context, prompt string) (string, error)
}

// InferenceConfig configures the edge inference behavior.
type InferenceConfig struct {
	Verbose bool
}

// EdgeInference provides LLM-powered relationship inference during consolidation.
type EdgeInference struct {
	client  InferenceClient
	verbose bool
}

// NewEdgeInference creates a new edge inference engine.
func NewEdgeInference(client InferenceClient, cfg InferenceConfig) *EdgeInference {
	return &EdgeInference{
		client:  client,
		verbose: cfg.Verbose,
	}
}

// EpisodeForInference provides episode data for inference.
type EpisodeForInference interface {
	GetID() string
	GetShortID() string
	GetAuthor() string
	GetTimestamp() time.Time
	GetSummaryC16() string
}

// InferEpisodeEdges analyzes a batch of episodes and infers relationships between them.
func (ei *EdgeInference) InferEpisodeEdges(ctx context.Context, episodes []EpisodeForInference) ([]EpisodeEdge, error) {
	if len(episodes) == 0 {
		return nil, nil
	}

	prompt := buildEpisodeInferencePrompt(episodes)

	if ei.verbose {
		log.Printf("[inference] Sending %d episodes for edge inference", len(episodes))
	}

	output, err := ei.client.Infer(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("inference failed: %w", err)
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

	cleaned := extractJSON(output)
	if err := json.Unmarshal([]byte(cleaned), &response); err != nil {
		if ei.verbose {
			log.Printf("[inference] Warning: Failed to parse JSON response, skipping batch")
		}
		return nil, nil
	}

	edges := make([]EpisodeEdge, 0, len(response.Edges))
	for _, e := range response.Edges {
		edges = append(edges, EpisodeEdge{
			FromID:       e.FromID,
			ToID:         e.ToID,
			Relationship: e.Relationship,
			Confidence:   e.Confidence,
		})
	}

	if ei.verbose {
		log.Printf("[inference] Inferred %d episode edges", len(edges))
	}

	return edges, nil
}

// InferTraceRelationship analyzes an episode and a trace to determine their relationship.
func (ei *EdgeInference) InferTraceRelationship(ctx context.Context, ep *graph.Episode, trace *graph.Trace) (string, float64, error) {
	prompt := buildTraceRelationshipPrompt(ep, trace)

	output, err := ei.client.Infer(ctx, prompt)
	if err != nil {
		return "", 0, fmt.Errorf("inference failed: %w", err)
	}

	var response struct {
		Relationship string  `json:"relationship"`
		Confidence   float64 `json:"confidence"`
	}

	cleaned := extractJSON(output)
	if err := json.Unmarshal([]byte(cleaned), &response); err != nil {
		return "", 0, fmt.Errorf("failed to parse inference result: %w", err)
	}

	return response.Relationship, response.Confidence, nil
}

// EpisodeEdge represents a relationship between two episodes.
type EpisodeEdge struct {
	FromID       string
	ToID         string
	Relationship string  // Freeform semantic descriptor
	Confidence   float64 // 0.0-1.0
}

func buildEpisodeInferencePrompt(episodes []EpisodeForInference) string {
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

func buildTraceRelationshipPrompt(ep *graph.Episode, trace *graph.Trace) string {
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

// extractJSON extracts JSON from markdown code blocks or returns the input if no code block found.
func extractJSON(s string) string {
	if start := strings.Index(s, "```json"); start != -1 {
		start += 7
		if end := strings.Index(s[start:], "```"); end != -1 {
			return strings.TrimSpace(s[start : start+end])
		}
	}
	if start := strings.Index(s, "```"); start != -1 {
		start += 3
		if end := strings.Index(s[start:], "```"); end != -1 {
			content := strings.TrimSpace(s[start : start+end])
			if idx := strings.Index(content, "\n"); idx != -1 {
				content = content[idx+1:]
			}
			return strings.TrimSpace(content)
		}
	}
	return strings.TrimSpace(s)
}
