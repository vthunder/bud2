// Package graphdb provides graph database inspection capabilities for bud state.
// It extends the base state.Inspector with direct graph DB access.
// Used by cmd/bud-state CLI; cmd/bud does not import this package.
package graphdb

import (
	"fmt"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/graph"
	"github.com/vthunder/bud2/internal/profiling"
	"github.com/vthunder/bud2/internal/state"
	"github.com/vthunder/bud2/internal/types"
)

// Embedder generates embeddings for text
type Embedder interface {
	Embed(text string) ([]float64, error)
}

// Inspector extends state.Inspector with graph database access.
// Used by cmd/bud-state CLI for direct inspection of the local graph DB.
type Inspector struct {
	*state.Inspector
	graphDB  *graph.DB
	embedder Embedder
}

// NewInspector creates an inspector with graph database access.
func NewInspector(statePath string, graphDB *graph.DB) *Inspector {
	return &Inspector{
		Inspector: state.NewInspector(statePath),
		graphDB:   graphDB,
	}
}

// SetEmbedder sets the embedder for memory search (optional)
func (g *Inspector) SetEmbedder(e Embedder) {
	g.embedder = e
}

// Summary overrides state.Inspector.Summary to include trace count from graph DB.
func (g *Inspector) Summary() (*state.StateSummary, error) {
	s, err := g.Inspector.Summary()
	if err != nil {
		return nil, err
	}
	if total, err := g.graphDB.CountTraces(); err == nil {
		s.Traces.Total = total
	}
	return s, nil
}

// TraceSummary is a condensed view of a trace
type TraceSummary struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Strength  int       `json:"strength"`
	CreatedAt time.Time `json:"created_at"`
}

// ListTraces returns summaries of all traces
func (g *Inspector) ListTraces() ([]TraceSummary, error) {
	traces, err := g.graphDB.GetAllTraces()
	if err != nil {
		return nil, err
	}

	result := make([]TraceSummary, 0, len(traces))
	for _, t := range traces {
		content := t.Summary
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		result = append(result, TraceSummary{
			ID:        t.ID,
			Content:   content,
			Strength:  t.Strength,
			CreatedAt: t.CreatedAt,
		})
	}
	return result, nil
}

// GetTrace returns full trace by ID
func (g *Inspector) GetTrace(id string) (*types.Trace, error) {
	gt, err := g.graphDB.GetTrace(id)
	if err != nil {
		return nil, err
	}
	if gt == nil {
		return nil, fmt.Errorf("trace not found: %s", id)
	}

	return &types.Trace{
		ID:         gt.ID,
		Content:    gt.Summary,
		Embedding:  gt.Embedding,
		Activation: gt.Activation,
		Strength:   gt.Strength,
		CreatedAt:  gt.CreatedAt,
		LastAccess: gt.LastAccessed,
	}, nil
}

// DeleteTrace removes a trace by ID
func (g *Inspector) DeleteTrace(id string) error {
	return g.graphDB.DeleteTrace(id)
}

// EpisodeSummary is a condensed view of an episode
type EpisodeSummary struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Source    string    `json:"source"`
	Author    string    `json:"author,omitempty"`
	Channel   string    `json:"channel,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// ListEpisodes returns summaries of recent episodes
func (g *Inspector) ListEpisodes(limit int) ([]EpisodeSummary, error) {
	episodes, err := g.graphDB.GetAllEpisodes(limit)
	if err != nil {
		return nil, err
	}

	result := make([]EpisodeSummary, 0, len(episodes))
	for _, ep := range episodes {
		content := ep.Content
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		result = append(result, EpisodeSummary{
			ID:        ep.ID,
			Content:   content,
			Source:    ep.Source,
			Author:    ep.Author,
			Channel:   ep.Channel,
			Timestamp: ep.TimestampEvent,
		})
	}
	return result, nil
}

// GetEpisode returns full episode by ID
func (g *Inspector) GetEpisode(id string) (*graph.Episode, error) {
	return g.graphDB.GetEpisode(id)
}

// CountEpisodes returns the total number of episodes
func (g *Inspector) CountEpisodes() (int, error) {
	return g.graphDB.CountEpisodes()
}

// EntitySummary is a condensed view of an entity
type EntitySummary struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Salience float64  `json:"salience"`
	Aliases  []string `json:"aliases,omitempty"`
}

// ListEntities returns summaries of entities ordered by salience
func (g *Inspector) ListEntities(limit int) ([]EntitySummary, error) {
	entities, err := g.graphDB.GetAllEntities(limit)
	if err != nil {
		return nil, err
	}

	result := make([]EntitySummary, 0, len(entities))
	for _, e := range entities {
		result = append(result, EntitySummary{
			ID:       e.ID,
			Name:     e.Name,
			Type:     string(e.Type),
			Salience: e.Salience,
			Aliases:  e.Aliases,
		})
	}
	return result, nil
}

// GetEntity returns full entity by ID
func (g *Inspector) GetEntity(id string) (*graph.Entity, error) {
	return g.graphDB.GetEntity(id)
}

// CountEntities returns the total number of entities
func (g *Inspector) CountEntities() (int, error) {
	return g.graphDB.CountEntities()
}

// NodeInfo holds information about a node and its relationships
type NodeInfo struct {
	ID    string            `json:"id"`
	Type  string            `json:"type"` // "trace", "episode", "entity"
	Data  any               `json:"data"` // The actual node data
	Links map[string][]Link `json:"links,omitempty"`
}

// Link represents a relationship to another node
type Link struct {
	ID      string  `json:"id"`
	Type    string  `json:"type,omitempty"`
	Weight  float64 `json:"weight,omitempty"`
	Preview string  `json:"preview,omitempty"`
}

// GetNodeInfo returns information about a node and its relationships
func (g *Inspector) GetNodeInfo(id string) (*NodeInfo, error) {
	// Check traces first
	if trace, _ := g.graphDB.GetTrace(id); trace != nil {
		info := &NodeInfo{
			ID:    id,
			Type:  "trace",
			Data:  trace,
			Links: make(map[string][]Link),
		}

		if sources, _ := g.graphDB.GetTraceSources(id); len(sources) > 0 {
			var links []Link
			for _, srcID := range sources {
				link := Link{ID: srcID}
				if ep, _ := g.graphDB.GetEpisode(srcID); ep != nil {
					preview := ep.Content
					if len(preview) > 50 {
						preview = preview[:50] + "..."
					}
					link.Preview = preview
				}
				links = append(links, link)
			}
			info.Links["source_episodes"] = links
		}

		if entityIDs, _ := g.graphDB.GetTraceEntities(id); len(entityIDs) > 0 {
			var links []Link
			for _, entID := range entityIDs {
				link := Link{ID: entID}
				if ent, _ := g.graphDB.GetEntity(entID); ent != nil {
					link.Preview = ent.Name
					link.Type = string(ent.Type)
				}
				links = append(links, link)
			}
			info.Links["entities"] = links
		}

		if neighbors, _ := g.graphDB.GetTraceNeighbors(id); len(neighbors) > 0 {
			var links []Link
			for _, n := range neighbors {
				link := Link{ID: n.ID, Type: string(n.Type), Weight: n.Weight}
				if t, _ := g.graphDB.GetTrace(n.ID); t != nil {
					preview := t.Summary
					if len(preview) > 50 {
						preview = preview[:50] + "..."
					}
					link.Preview = preview
				}
				links = append(links, link)
			}
			info.Links["related_traces"] = links
		}

		return info, nil
	}

	// Check episodes
	if episode, _ := g.graphDB.GetEpisode(id); episode != nil {
		info := &NodeInfo{
			ID:    id,
			Type:  "episode",
			Data:  episode,
			Links: make(map[string][]Link),
		}

		if entities, _ := g.graphDB.GetEntitiesForEpisode(id); len(entities) > 0 {
			var links []Link
			for _, e := range entities {
				links = append(links, Link{
					ID:      e.ID,
					Preview: e.Name,
					Type:    string(e.Type),
				})
			}
			info.Links["mentions"] = links
		}

		if neighbors, _ := g.graphDB.GetEpisodeNeighbors(id); len(neighbors) > 0 {
			var links []Link
			for _, n := range neighbors {
				link := Link{ID: n.ID, Type: string(n.Type), Weight: n.Weight}
				if ep, _ := g.graphDB.GetEpisode(n.ID); ep != nil {
					preview := ep.Content
					if len(preview) > 50 {
						preview = preview[:50] + "..."
					}
					link.Preview = preview
				}
				links = append(links, link)
			}
			info.Links["replies"] = links
		}

		return info, nil
	}

	// Check entities
	if entity, _ := g.graphDB.GetEntity(id); entity != nil {
		info := &NodeInfo{
			ID:    id,
			Type:  "entity",
			Data:  entity,
			Links: make(map[string][]Link),
		}

		if episodeIDs, _ := g.graphDB.GetEpisodesForEntity(id); len(episodeIDs) > 0 {
			var links []Link
			for _, epID := range episodeIDs {
				link := Link{ID: epID}
				if ep, _ := g.graphDB.GetEpisode(epID); ep != nil {
					preview := ep.Content
					if len(preview) > 50 {
						preview = preview[:50] + "..."
					}
					link.Preview = preview
				}
				links = append(links, link)
			}
			info.Links["mentioned_in"] = links
		}

		if traceIDs, _ := g.graphDB.GetTracesForEntity(id); len(traceIDs) > 0 {
			var links []Link
			for _, tID := range traceIDs {
				link := Link{ID: tID}
				if t, _ := g.graphDB.GetTrace(tID); t != nil {
					preview := t.Summary
					if len(preview) > 50 {
						preview = preview[:50] + "..."
					}
					link.Preview = preview
				}
				links = append(links, link)
			}
			info.Links["in_traces"] = links
		}

		if neighbors, _ := g.graphDB.GetEntityRelations(id); len(neighbors) > 0 {
			var links []Link
			for _, n := range neighbors {
				link := Link{ID: n.ID, Type: string(n.Type), Weight: n.Weight}
				if e, _ := g.graphDB.GetEntity(n.ID); e != nil {
					link.Preview = e.Name
				}
				links = append(links, link)
			}
			info.Links["related_entities"] = links
		}

		return info, nil
	}

	return nil, fmt.Errorf("node not found: %s", id)
}

// SearchResult represents a memory search result
type SearchResult struct {
	ID         string    `json:"id"`
	Summary    string    `json:"summary"`
	Activation float64   `json:"activation"`
	CreatedAt  time.Time `json:"created_at"`
}

// SearchMemory searches long-term memory for traces matching the query.
func (g *Inspector) SearchMemory(query string, limit int) ([]SearchResult, error) {
	return g.SearchMemoryWithContext(query, limit, true)
}

// SearchMemoryWithContext searches memory with optional context awareness.
func (g *Inspector) SearchMemoryWithContext(query string, limit int, useContext bool) ([]SearchResult, error) {
	defer profiling.Get().StartWithMetadata("search_memory", "memory_search.total", map[string]interface{}{
		"query_length": len(query),
		"limit":        limit,
		"use_context":  useContext,
	})()

	if g.embedder == nil {
		return nil, fmt.Errorf("embedder not configured - cannot search memory")
	}

	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	var queryEmb []float64
	{
		stopEmbedding := profiling.Get().Start("search_memory", "memory_search.embedding")
		var err error
		queryEmb, err = g.embedder.Embed(query)
		stopEmbedding()
		if err != nil {
			return nil, fmt.Errorf("failed to embed query: %w", err)
		}
	}

	var result *graph.RetrievalResult

	if useContext {
		stopGetActivated := profiling.Get().Start("search_memory", "memory_search.get_activated")
		activatedTraces, err := g.graphDB.GetActivatedTraces(0.3, 10)
		stopGetActivated()
		if err != nil {
			stopRetrieve := profiling.Get().StartWithMetadata("search_memory", "memory_search.retrieve", map[string]interface{}{
				"mode": "fallback",
			})
			result, err = g.graphDB.Retrieve(queryEmb, query, limit)
			stopRetrieve()
			if err != nil {
				return nil, fmt.Errorf("retrieval failed: %w", err)
			}
		} else {
			contextIDs := make([]string, 0, len(activatedTraces))
			for _, t := range activatedTraces {
				contextIDs = append(contextIDs, t.ID)
			}
			stopRetrieve := profiling.Get().StartWithMetadata("search_memory", "memory_search.retrieve_with_context", map[string]interface{}{
				"context_count": len(contextIDs),
			})
			var err error
			result, err = g.graphDB.RetrieveWithContext(queryEmb, query, contextIDs, limit)
			stopRetrieve()
			if err != nil {
				return nil, fmt.Errorf("context retrieval failed: %w", err)
			}
		}
	} else {
		stopRetrieve := profiling.Get().StartWithMetadata("search_memory", "memory_search.retrieve", map[string]interface{}{
			"mode": "standard",
		})
		var err error
		result, err = g.graphDB.Retrieve(queryEmb, query, limit)
		stopRetrieve()
		if err != nil {
			return nil, fmt.Errorf("retrieval failed: %w", err)
		}
	}

	stopFormat := profiling.Get().StartWithMetadata("search_memory", "memory_search.format_results", map[string]interface{}{
		"result_count": len(result.Traces),
	})
	var results []SearchResult
	for _, t := range result.Traces {
		results = append(results, SearchResult{
			ID:         t.ID,
			Summary:    t.Summary,
			Activation: t.Activation,
			CreatedAt:  t.CreatedAt,
		})
	}
	stopFormat()

	return results, nil
}

// TraceContext provides detailed context for a trace including source episodes and linked entities
type TraceContext struct {
	Trace    TraceSummary     `json:"trace"`
	Episodes []EpisodeSummary `json:"source_episodes"`
	Entities []EntitySummary  `json:"linked_entities"`
}

// GetTraceContext retrieves detailed context for a trace
func (g *Inspector) GetTraceContext(traceID string) (*TraceContext, error) {
	trace, err := g.graphDB.GetTrace(traceID)
	if err != nil {
		return nil, err
	}
	if trace == nil {
		return nil, fmt.Errorf("trace not found: %s", traceID)
	}

	ctx := &TraceContext{
		Trace: TraceSummary{
			ID:        trace.ID,
			Content:   trace.Summary,
			Strength:  trace.Strength,
			CreatedAt: trace.CreatedAt,
		},
	}

	episodeIDs, err := g.graphDB.GetTraceSources(traceID)
	if err == nil {
		for _, epID := range episodeIDs {
			ep, err := g.graphDB.GetEpisode(epID)
			if err != nil || ep == nil {
				continue
			}
			content := ep.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			ctx.Episodes = append(ctx.Episodes, EpisodeSummary{
				ID:        ep.ID,
				Content:   content,
				Source:    ep.Source,
				Author:    ep.Author,
				Channel:   ep.Channel,
				Timestamp: ep.TimestampEvent,
			})
		}
	}

	entityIDs, err := g.graphDB.GetTraceEntities(traceID)
	if err == nil {
		for _, entID := range entityIDs {
			ent, err := g.graphDB.GetEntity(entID)
			if err != nil || ent == nil {
				continue
			}
			ctx.Entities = append(ctx.Entities, EntitySummary{
				ID:       ent.ID,
				Name:     ent.Name,
				Type:     string(ent.Type),
				Salience: ent.Salience,
				Aliases:  ent.Aliases,
			})
		}
	}

	return ctx, nil
}

// QueryTrace returns the source episodes for a trace at the given compression level.
// level parameter: 0=raw episodes, 1=L1 summary (default), 2=L2 summary
func (g *Inspector) QueryTrace(traceID, question string, level int) (string, error) {
	episodeIDs, err := g.graphDB.GetTraceSources(traceID)
	if err != nil {
		return "", fmt.Errorf("failed to get trace sources: %w", err)
	}

	if len(episodeIDs) == 0 {
		return "", fmt.Errorf("trace has no source episodes")
	}

	if level < 0 || level > 2 {
		return "", fmt.Errorf("invalid compression level %d (must be 0-2)", level)
	}

	var context strings.Builder
	var usedCompression bool
	for _, epID := range episodeIDs {
		ep, err := g.graphDB.GetEpisode(epID)
		if err != nil || ep == nil {
			continue
		}

		var content string
		if level > 0 {
			summary, err := g.graphDB.GetEpisodeSummary(epID, level)
			if err == nil && summary != nil {
				content = summary.Summary
				usedCompression = true
			} else {
				content = ep.Content
			}
		} else {
			content = ep.Content
		}

		context.WriteString(fmt.Sprintf("[%s] %s: %s\n",
			ep.TimestampEvent.Format("2006-01-02 15:04"), ep.Author, content))
	}

	if context.Len() == 0 {
		return "", fmt.Errorf("no episode content available")
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Context from %d source episodes", len(episodeIDs)))
	if usedCompression {
		result.WriteString(fmt.Sprintf(" (compression level %d)", level))
	}
	result.WriteString(":\n\n")
	result.WriteString(context.String())

	if question != "" {
		result.WriteString("\n(LLM query not yet implemented - add Generator interface to GraphInspector)\n")
		result.WriteString(fmt.Sprintf("Question: %s\n", question))
	}

	return result.String(), nil
}
