package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// EpisodeEdgeRow represents a row from the episode_edges table
type EpisodeEdgeRow struct {
	FromID       string
	ToID         string
	Relationship string
	Confidence   float64
}

// QueryEpisodeEdges retrieves episode edges where both endpoints are in the given set
func (g *DB) QueryEpisodeEdges(episodeIDs map[string]bool) ([]EpisodeEdgeRow, error) {
	if len(episodeIDs) == 0 {
		return nil, nil
	}

	// Build IN clause placeholders
	ids := make([]string, 0, len(episodeIDs))
	for id := range episodeIDs {
		ids = append(ids, id)
	}

	// Query edges where both from and to are in the set
	query := `
		SELECT from_id, to_id, relationship_desc, confidence
		FROM episode_edges
		WHERE from_id IN (` + buildPlaceholders(len(ids)) + `)
		  AND to_id IN (` + buildPlaceholders(len(ids)) + `)`

	args := make([]interface{}, len(ids)*2)
	for i, id := range ids {
		args[i] = id
		args[len(ids)+i] = id
	}

	rows, err := g.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []EpisodeEdgeRow
	for rows.Next() {
		var edge EpisodeEdgeRow
		if err := rows.Scan(&edge.FromID, &edge.ToID, &edge.Relationship, &edge.Confidence); err != nil {
			continue
		}
		edges = append(edges, edge)
	}

	return edges, nil
}

func buildPlaceholders(n int) string {
	if n == 0 {
		return ""
	}
	placeholders := make([]string, n)
	for i := range placeholders {
		placeholders[i] = "?"
	}
	return strings.Join(placeholders, ",")
}

// AddEpisodeEpisodeEdge creates semantic link between episodes
func (g *DB) AddEpisodeEpisodeEdge(fromID, toID, edgeType, relationshipDesc string, confidence float64) error {
	_, err := g.db.Exec(`
		INSERT INTO episode_edges (from_id, to_id, edge_type, relationship_desc, confidence, created_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT DO NOTHING
	`, fromID, toID, edgeType, relationshipDesc, confidence)
	return err
}

// AddEpisodeTraceEdge creates link from episode to trace
func (g *DB) AddEpisodeTraceEdge(episodeID, traceID, relationshipDesc string, confidence float64) error {
	_, err := g.db.Exec(`
		INSERT INTO episode_trace_edges (episode_id, trace_id, relationship_desc, confidence)
		VALUES (?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`, episodeID, traceID, relationshipDesc, confidence)
	return err
}

// GetEpisodesReferencingTrace returns episodes linked to a trace
func (g *DB) GetEpisodesReferencingTrace(traceID string) ([]Episode, error) {
	rows, err := g.db.Query(`
		SELECT ep.id, ep.content, ep.timestamp_event, ep.author, ep.channel, ep.source
		FROM episodes ep
		JOIN episode_trace_edges et ON ep.id = et.episode_id
		WHERE et.trace_id = ?
		ORDER BY ep.timestamp_event ASC
	`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var episodes []Episode
	for rows.Next() {
		var ep Episode
		var timestampStr string
		err := rows.Scan(&ep.ID, &ep.Content, &timestampStr, &ep.Author, &ep.Channel, &ep.Source)
		if err != nil {
			return nil, err
		}
		// Parse timestamp
		ep.TimestampEvent, _ = time.Parse(time.RFC3339, timestampStr)
		episodes = append(episodes, ep)
	}
	return episodes, nil
}

// GetTracesReferencedByEpisode returns traces an episode links to
func (g *DB) GetTracesReferencedByEpisode(episodeID string) ([]string, error) {
	rows, err := g.db.Query(`
		SELECT trace_id FROM episode_trace_edges WHERE episode_id = ?
	`, episodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var traceIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		traceIDs = append(traceIDs, id)
	}
	return traceIDs, nil
}

// GetEpisodeTraceEdges returns all episode-trace edges for a given trace
func (g *DB) GetEpisodeTraceEdges(traceID string) ([]struct {
	EpisodeID        string
	TraceID          string
	RelationshipDesc string
	Confidence       float64
	CreatedAt        time.Time
}, error) {
	rows, err := g.db.Query(`
		SELECT episode_id, trace_id, relationship_desc, confidence, created_at
		FROM episode_trace_edges
		WHERE trace_id = ?
		ORDER BY created_at DESC
	`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []struct {
		EpisodeID        string
		TraceID          string
		RelationshipDesc string
		Confidence       float64
		CreatedAt        time.Time
	}

	for rows.Next() {
		var edge struct {
			EpisodeID        string
			TraceID          string
			RelationshipDesc string
			Confidence       float64
			CreatedAt        time.Time
		}
		if err := rows.Scan(&edge.EpisodeID, &edge.TraceID, &edge.RelationshipDesc, &edge.Confidence, &edge.CreatedAt); err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}

	return edges, nil
}

// GetEpisodeEdges returns all episode-episode edges for a given episode
func (g *DB) GetEpisodeEdges(episodeID string) ([]struct {
	FromID           string
	ToID             string
	EdgeType         string
	RelationshipDesc sql.NullString
	Confidence       sql.NullFloat64
	Weight           float64
	CreatedAt        time.Time
}, error) {
	rows, err := g.db.Query(`
		SELECT from_id, to_id, edge_type, relationship_desc, confidence, weight, created_at
		FROM episode_edges
		WHERE from_id = ? OR to_id = ?
		ORDER BY created_at DESC
	`, episodeID, episodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []struct {
		FromID           string
		ToID             string
		EdgeType         string
		RelationshipDesc sql.NullString
		Confidence       sql.NullFloat64
		Weight           float64
		CreatedAt        time.Time
	}

	for rows.Next() {
		var edge struct {
			FromID           string
			ToID             string
			EdgeType         string
			RelationshipDesc sql.NullString
			Confidence       sql.NullFloat64
			Weight           float64
			CreatedAt        time.Time
		}
		if err := rows.Scan(&edge.FromID, &edge.ToID, &edge.EdgeType, &edge.RelationshipDesc, &edge.Confidence, &edge.Weight, &edge.CreatedAt); err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}

	return edges, nil
}

// GetTraceSourceEpisodes retrieves all episode objects for a trace's source episodes
func (g *DB) GetTraceSourceEpisodes(traceID string) ([]Episode, error) {
	episodeIDs, err := g.GetTraceSources(traceID)
	if err != nil {
		return nil, err
	}

	var episodes []Episode
	for _, id := range episodeIDs {
		ep, err := g.GetEpisode(id)
		if err != nil {
			log.Printf("[reconsolidation] Warning: failed to get episode %s: %v", id, err)
			continue
		}
		if ep != nil {
			episodes = append(episodes, *ep)
		}
	}
	return episodes, nil
}

// ReconsolidateTrace updates a trace with new context from additional episodes
// llmSummarize should take combined episodes and generate a new summary
// llmEmbed should generate an embedding for the new summary
func (g *DB) ReconsolidateTrace(
	traceID string,
	newEpisodes []Episode,
	llmSummarize func([]Episode) (string, error),
	llmEmbed func(string) ([]float64, error),
) error {
	// 1. Get existing trace
	trace, err := g.GetTrace(traceID)
	if err != nil {
		return fmt.Errorf("failed to get trace: %w", err)
	}
	if trace == nil {
		return fmt.Errorf("trace not found: %s", traceID)
	}

	// 2. Get original source episodes
	originalEpisodes, err := g.GetTraceSourceEpisodes(traceID)
	if err != nil {
		return fmt.Errorf("failed to get source episodes: %w", err)
	}

	// 3. Combine old + new (preserve original context)
	allEpisodes := append(originalEpisodes, newEpisodes...)

	// 4. Generate updated summary
	summary, err := llmSummarize(allEpisodes)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}

	// 5. Generate new embedding
	embedding, err := llmEmbed(summary)
	if err != nil {
		return fmt.Errorf("failed to generate embedding: %w", err)
	}

	// 6. Update trace (preserves ID and created_at)
	err = g.updateTraceContent(traceID, summary, embedding)
	if err != nil {
		return fmt.Errorf("failed to update trace: %w", err)
	}

	// 7. Link new episodes to trace (via trace_sources)
	for _, ep := range newEpisodes {
		err = g.LinkTraceToSource(traceID, ep.ID)
		if err != nil {
			log.Printf("[reconsolidation] Failed to add trace source: %v", err)
		}
	}

	// 8. Regenerate pyramid summaries
	err = g.RegeneratePyramidSummaries(traceID, summary)
	if err != nil {
		return fmt.Errorf("failed to regenerate pyramid: %w", err)
	}

	log.Printf("[reconsolidation] Updated trace %s with %d new episodes", traceID, len(newEpisodes))
	return nil
}

// updateTraceContent updates trace summary and embedding (internal helper)
func (g *DB) updateTraceContent(traceID string, summary string, embedding []float64) error {
	embeddingJSON, err := json.Marshal(embedding)
	if err != nil {
		return err
	}

	_, err = g.db.Exec(`
		UPDATE traces
		SET embedding = ?, last_accessed = CURRENT_TIMESTAMP
		WHERE id = ?
	`, embeddingJSON, traceID)
	return err
}

// RegeneratePyramidSummaries recreates all compression levels for a trace
// baseSummary is the full (level 0) summary text
func (g *DB) RegeneratePyramidSummaries(traceID string, baseSummary string) error {
	// Delete existing summaries
	_, err := g.db.Exec(`DELETE FROM trace_summaries WHERE trace_id = ?`, traceID)
	if err != nil {
		return err
	}

	// Store level 0 (verbatim)
	_, err = g.db.Exec(`
		INSERT INTO trace_summaries (trace_id, compression_level, summary, tokens)
		VALUES (?, 0, ?, ?)
	`, traceID, baseSummary, len(baseSummary)/4) // rough token estimate
	if err != nil {
		return fmt.Errorf("failed to store level 0 summary: %w", err)
	}

	// Note: Higher compression levels (4, 8, 16, 32, 64) should be generated
	// via the existing pyramid compression system in consolidation
	// This function just stores the base level - the consolidation process
	// will handle regenerating compressed versions on next cycle

	return nil
}
