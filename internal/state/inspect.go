package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/graph"
	"github.com/vthunder/bud2/internal/profiling"
	"github.com/vthunder/bud2/internal/types"
)

// Embedder generates embeddings for text
type Embedder interface {
	Embed(text string) ([]float64, error)
}

// Inspector provides state introspection capabilities
type Inspector struct {
	statePath string
	graphDB   *graph.DB
	embedder  Embedder
}

// NewInspector creates a new state inspector
func NewInspector(statePath string, graphDB *graph.DB) *Inspector {
	return &Inspector{statePath: statePath, graphDB: graphDB}
}

// SetEmbedder sets the embedder for memory search (optional)
func (i *Inspector) SetEmbedder(e Embedder) {
	i.embedder = e
}

// ComponentSummary holds summary for one state component
type ComponentSummary struct {
	Total int `json:"total"`
}

// StateSummary holds summary of all state
type StateSummary struct {
	Traces   ComponentSummary `json:"traces"`
	Percepts ComponentSummary `json:"percepts"`
	Threads  ComponentSummary `json:"threads"`
	Activity int              `json:"activity_entries"`
	Inbox    int              `json:"inbox_entries"`
	Outbox   int              `json:"outbox_entries"`
	Signals  int              `json:"signals_entries"`
}

// HealthReport holds health check results
type HealthReport struct {
	Status          string   `json:"status"` // "healthy", "warnings", "issues"
	Warnings        []string `json:"warnings,omitempty"`
	Recommendations []string `json:"recommendations,omitempty"`
}

// Summary returns a summary of all state components
func (i *Inspector) Summary() (*StateSummary, error) {
	summary := &StateSummary{}

	// Count traces from graph DB
	if i.graphDB != nil {
		total, err := i.graphDB.CountTraces()
		if err == nil {
			summary.Traces.Total = total
		}
	}

	// Count percepts
	percepts, err := i.loadPercepts()
	if err == nil {
		summary.Percepts.Total = len(percepts)
	}

	// Count threads
	threads, err := i.loadThreads()
	if err == nil {
		summary.Threads.Total = len(threads)
	}

	// Count JSONL files
	summary.Activity = i.countJSONL("system/activity.jsonl")
	summary.Inbox = 0 // Inbox is now in-memory only, not a file
	summary.Outbox = i.countJSONL("system/queues/outbox.jsonl")
	summary.Signals = i.countJSONL("system/queues/signals.jsonl")

	return summary, nil
}

// Health runs health checks and returns a report
func (i *Inspector) Health() (*HealthReport, error) {
	report := &HealthReport{Status: "healthy"}

	summary, _ := i.Summary()

	// Check for potential issues
	if summary.Traces.Total > 1000 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("High trace count: %d", summary.Traces.Total))
		report.Recommendations = append(report.Recommendations, "Consider pruning old traces")
	}

	if summary.Percepts.Total > 100 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("High percept count: %d", summary.Percepts.Total))
		report.Recommendations = append(report.Recommendations, "Percepts should decay; check consolidation")
	}

	if summary.Activity > 10000 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Large activity log: %d entries", summary.Activity))
		report.Recommendations = append(report.Recommendations, "Consider truncating old activity entries")
	}

	if len(report.Warnings) > 0 {
		report.Status = "warnings"
	}

	return report, nil
}

// TraceSummary is a condensed view of a trace
type TraceSummary struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Strength  int       `json:"strength"`
	CreatedAt time.Time `json:"created_at"`
}

// ListTraces returns summaries of all traces
func (i *Inspector) ListTraces() ([]TraceSummary, error) {
	if i.graphDB == nil {
		return nil, fmt.Errorf("graph database not initialized")
	}

	traces, err := i.graphDB.GetAllTraces()
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
func (i *Inspector) GetTrace(id string) (*types.Trace, error) {
	if i.graphDB == nil {
		return nil, fmt.Errorf("graph database not initialized")
	}

	gt, err := i.graphDB.GetTrace(id)
	if err != nil {
		return nil, err
	}
	if gt == nil {
		return nil, fmt.Errorf("trace not found: %s", id)
	}

	// Convert graph.Trace to types.Trace
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
func (i *Inspector) DeleteTrace(id string) error {
	if i.graphDB == nil {
		return fmt.Errorf("graph database not initialized")
	}
	return i.graphDB.DeleteTrace(id)
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
func (i *Inspector) ListEpisodes(limit int) ([]EpisodeSummary, error) {
	if i.graphDB == nil {
		return nil, fmt.Errorf("graph database not initialized")
	}

	episodes, err := i.graphDB.GetAllEpisodes(limit)
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
func (i *Inspector) GetEpisode(id string) (*graph.Episode, error) {
	if i.graphDB == nil {
		return nil, fmt.Errorf("graph database not initialized")
	}
	return i.graphDB.GetEpisode(id)
}

// CountEpisodes returns the total number of episodes
func (i *Inspector) CountEpisodes() (int, error) {
	if i.graphDB == nil {
		return 0, fmt.Errorf("graph database not initialized")
	}
	return i.graphDB.CountEpisodes()
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
func (i *Inspector) ListEntities(limit int) ([]EntitySummary, error) {
	if i.graphDB == nil {
		return nil, fmt.Errorf("graph database not initialized")
	}

	entities, err := i.graphDB.GetAllEntities(limit)
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
func (i *Inspector) GetEntity(id string) (*graph.Entity, error) {
	if i.graphDB == nil {
		return nil, fmt.Errorf("graph database not initialized")
	}
	return i.graphDB.GetEntity(id)
}

// CountEntities returns the total number of entities
func (i *Inspector) CountEntities() (int, error) {
	if i.graphDB == nil {
		return 0, fmt.Errorf("graph database not initialized")
	}
	return i.graphDB.CountEntities()
}

// NodeInfo holds information about a node and its relationships
type NodeInfo struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"` // "trace", "episode", "entity"
	Data     any               `json:"data"` // The actual node data
	Links    map[string][]Link `json:"links,omitempty"`
}

// Link represents a relationship to another node
type Link struct {
	ID       string  `json:"id"`
	Type     string  `json:"type,omitempty"`
	Weight   float64 `json:"weight,omitempty"`
	Preview  string  `json:"preview,omitempty"`
}

// GetNodeInfo returns information about a node and its relationships
func (i *Inspector) GetNodeInfo(id string) (*NodeInfo, error) {
	if i.graphDB == nil {
		return nil, fmt.Errorf("graph database not initialized")
	}

	// Try to find the node in each table
	// Check traces first
	if trace, _ := i.graphDB.GetTrace(id); trace != nil {
		info := &NodeInfo{
			ID:   id,
			Type: "trace",
			Data: trace,
			Links: make(map[string][]Link),
		}

		// Get source episodes
		if sources, _ := i.graphDB.GetTraceSources(id); len(sources) > 0 {
			var links []Link
			for _, srcID := range sources {
				link := Link{ID: srcID}
				if ep, _ := i.graphDB.GetEpisode(srcID); ep != nil {
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

		// Get linked entities
		if entityIDs, _ := i.graphDB.GetTraceEntities(id); len(entityIDs) > 0 {
			var links []Link
			for _, entID := range entityIDs {
				link := Link{ID: entID}
				if ent, _ := i.graphDB.GetEntity(entID); ent != nil {
					link.Preview = ent.Name
					link.Type = string(ent.Type)
				}
				links = append(links, link)
			}
			info.Links["entities"] = links
		}

		// Get related traces
		if neighbors, _ := i.graphDB.GetTraceNeighbors(id); len(neighbors) > 0 {
			var links []Link
			for _, n := range neighbors {
				link := Link{ID: n.ID, Type: string(n.Type), Weight: n.Weight}
				if t, _ := i.graphDB.GetTrace(n.ID); t != nil {
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
	if episode, _ := i.graphDB.GetEpisode(id); episode != nil {
		info := &NodeInfo{
			ID:   id,
			Type: "episode",
			Data: episode,
			Links: make(map[string][]Link),
		}

		// Get mentioned entities
		if entities, _ := i.graphDB.GetEntitiesForEpisode(id); len(entities) > 0 {
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

		// Get episode neighbors (replies)
		if neighbors, _ := i.graphDB.GetEpisodeNeighbors(id); len(neighbors) > 0 {
			var links []Link
			for _, n := range neighbors {
				link := Link{ID: n.ID, Type: string(n.Type), Weight: n.Weight}
				if ep, _ := i.graphDB.GetEpisode(n.ID); ep != nil {
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
	if entity, _ := i.graphDB.GetEntity(id); entity != nil {
		info := &NodeInfo{
			ID:   id,
			Type: "entity",
			Data: entity,
			Links: make(map[string][]Link),
		}

		// Get episodes mentioning this entity
		if episodeIDs, _ := i.graphDB.GetEpisodesForEntity(id); len(episodeIDs) > 0 {
			var links []Link
			for _, epID := range episodeIDs {
				link := Link{ID: epID}
				if ep, _ := i.graphDB.GetEpisode(epID); ep != nil {
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

		// Get traces involving this entity
		if traceIDs, _ := i.graphDB.GetTracesForEntity(id); len(traceIDs) > 0 {
			var links []Link
			for _, tID := range traceIDs {
				link := Link{ID: tID}
				if t, _ := i.graphDB.GetTrace(tID); t != nil {
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

		// Get related entities
		if neighbors, _ := i.graphDB.GetEntityRelations(id); len(neighbors) > 0 {
			var links []Link
			for _, n := range neighbors {
				link := Link{ID: n.ID, Type: string(n.Type), Weight: n.Weight}
				if e, _ := i.graphDB.GetEntity(n.ID); e != nil {
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

// PerceptSummary is a condensed view of a percept
type PerceptSummary struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
	Preview   string    `json:"preview"`
}

// ListPercepts returns summaries of all percepts
func (i *Inspector) ListPercepts() ([]PerceptSummary, error) {
	percepts, err := i.loadPercepts()
	if err != nil {
		return nil, err
	}

	result := make([]PerceptSummary, 0, len(percepts))
	for _, p := range percepts {
		preview := ""
		if content, ok := p.Data["content"].(string); ok {
			preview = content
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
		}
		result = append(result, PerceptSummary{
			ID:        p.ID,
			Type:      p.Type,
			Source:    p.Source,
			Timestamp: p.Timestamp,
			Preview:   preview,
		})
	}
	return result, nil
}

// ClearPercepts removes all percepts, optionally filtering by age
func (i *Inspector) ClearPercepts(olderThan time.Duration) (int, error) {
	if olderThan == 0 {
		// Clear all
		if err := i.savePercepts(nil); err != nil {
			return 0, err
		}
		return 0, nil // We don't know how many were there
	}

	percepts, err := i.loadPercepts()
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-olderThan)
	var keep []*types.Percept
	cleared := 0
	for _, p := range percepts {
		if p.Timestamp.Before(cutoff) {
			cleared++
		} else {
			keep = append(keep, p)
		}
	}

	if err := i.savePercepts(keep); err != nil {
		return 0, err
	}
	return cleared, nil
}

// ThreadSummary is a condensed view of a thread
type ThreadSummary struct {
	ID           string             `json:"id"`
	Status       types.ThreadStatus `json:"status"`
	SessionState types.SessionState `json:"session_state"`
	PerceptCount int                `json:"percept_count"`
}

// ListThreads returns summaries of all threads
func (i *Inspector) ListThreads() ([]ThreadSummary, error) {
	threads, err := i.loadThreads()
	if err != nil {
		return nil, err
	}

	result := make([]ThreadSummary, 0, len(threads))
	for _, t := range threads {
		result = append(result, ThreadSummary{
			ID:           t.ID,
			Status:       t.Status,
			SessionState: t.SessionState,
			PerceptCount: len(t.PerceptRefs),
		})
	}
	return result, nil
}

// GetThread returns full thread by ID
func (i *Inspector) GetThread(id string) (*types.Thread, error) {
	threads, err := i.loadThreads()
	if err != nil {
		return nil, err
	}

	for _, t := range threads {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("thread not found: %s", id)
}

// ClearThreads removes threads, optionally by status
func (i *Inspector) ClearThreads(status *types.ThreadStatus) (int, error) {
	if status == nil {
		// Clear all
		if err := i.saveThreads(nil); err != nil {
			return 0, err
		}
		return 0, nil
	}

	threads, err := i.loadThreads()
	if err != nil {
		return 0, err
	}

	var keep []*types.Thread
	cleared := 0
	for _, t := range threads {
		if t.Status == *status {
			cleared++
		} else {
			keep = append(keep, t)
		}
	}

	if err := i.saveThreads(keep); err != nil {
		return 0, err
	}
	return cleared, nil
}

// TailLogs returns recent entries from activity log
func (i *Inspector) TailLogs(count int) ([]map[string]any, error) {
	return i.tailJSONL("system/activity.jsonl", count), nil
}

// TruncateLogs keeps only the last N entries in the activity log
func (i *Inspector) TruncateLogs(keep int) error {
	if err := i.truncateJSONL("system/activity.jsonl", keep); err != nil {
		return fmt.Errorf("failed to truncate activity.jsonl: %w", err)
	}
	return nil
}

// QueuesSummary holds queue counts
type QueuesSummary struct {
	Inbox   int `json:"inbox"`
	Outbox  int `json:"outbox"`
	Signals int `json:"signals"`
}

// ListQueues returns queue entry counts
func (i *Inspector) ListQueues() (*QueuesSummary, error) {
	return &QueuesSummary{
		Inbox:   0, // Inbox is now in-memory only, not a file
		Outbox:  i.countJSONL("system/queues/outbox.jsonl"),
		Signals: i.countJSONL("system/queues/signals.jsonl"),
	}, nil
}

// ClearQueues clears all queue files
func (i *Inspector) ClearQueues() error {
	queuesPath := filepath.Join(i.statePath, "system", "queues")
	// Note: inbox.jsonl removed - inbox is now in-memory only
	for _, name := range []string{"outbox.jsonl", "signals.jsonl"} {
		path := filepath.Join(queuesPath, name)
		if err := os.WriteFile(path, []byte{}, 0644); err != nil {
			return fmt.Errorf("failed to clear %s: %w", name, err)
		}
	}
	return nil
}

// SessionInfo represents a session entry
type SessionInfo struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
}

// ListSessions returns session info
func (i *Inspector) ListSessions() ([]SessionInfo, error) {
	path := filepath.Join(i.statePath, "system", "sessions.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var sessions map[string]any
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, err
	}

	// Convert to list
	var result []SessionInfo
	for id, v := range sessions {
		info := SessionInfo{ID: id}
		if m, ok := v.(map[string]any); ok {
			if status, ok := m["status"].(string); ok {
				info.Status = status
			}
		}
		result = append(result, info)
	}
	return result, nil
}

// ClearSessions clears session tracking
func (i *Inspector) ClearSessions() error {
	path := filepath.Join(i.statePath, "system", "sessions.json")
	return os.WriteFile(path, []byte("{}"), 0644)
}


// SearchResult represents a memory search result
type SearchResult struct {
	ID         string    `json:"id"`
	Summary    string    `json:"summary"`
	Activation float64   `json:"activation"`
	CreatedAt  time.Time `json:"created_at"`
}

// SearchMemory searches long-term memory for traces matching the query.
// If useContext is true, also considers currently-activated traces as additional seeds,
// biasing results toward memories connected to the current working context.
func (i *Inspector) SearchMemory(query string, limit int) ([]SearchResult, error) {
	return i.SearchMemoryWithContext(query, limit, true)
}

// SearchMemoryWithContext searches memory with optional context awareness.
func (i *Inspector) SearchMemoryWithContext(query string, limit int, useContext bool) ([]SearchResult, error) {
	// Track total search time
	defer profiling.Get().StartWithMetadata("search_memory", "memory_search.total", map[string]interface{}{
		"query_length": len(query),
		"limit":        limit,
		"use_context":  useContext,
	})()

	if i.graphDB == nil {
		return nil, fmt.Errorf("graph database not initialized")
	}
	if i.embedder == nil {
		return nil, fmt.Errorf("embedder not configured - cannot search memory")
	}

	// Default limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	// Generate embedding for query (track timing)
	var queryEmb []float64
	{
		defer profiling.Get().Start("search_memory", "memory_search.embedding")()
		var err error
		queryEmb, err = i.embedder.Embed(query)
		if err != nil {
			return nil, fmt.Errorf("failed to embed query: %w", err)
		}
	}

	var result *graph.RetrievalResult

	if useContext {
		// Get currently-activated traces to use as additional context
		var activatedTraces []*graph.Trace
		{
			defer profiling.Get().Start("search_memory", "memory_search.get_activated")()
			var err error
			activatedTraces, err = i.graphDB.GetActivatedTraces(0.3, 10)
			if err != nil {
				// Fall back to regular retrieval if we can't get activated traces
				defer profiling.Get().StartWithMetadata("search_memory", "memory_search.retrieve", map[string]interface{}{
					"mode": "fallback",
				})()
				result, err = i.graphDB.Retrieve(queryEmb, query, limit)
				if err != nil {
					return nil, fmt.Errorf("retrieval failed: %w", err)
				}
			} else {
				// Extract IDs from activated traces
				contextIDs := make([]string, 0, len(activatedTraces))
				for _, t := range activatedTraces {
					contextIDs = append(contextIDs, t.ID)
				}
				// Use context-aware retrieval
				{
					defer profiling.Get().StartWithMetadata("search_memory", "memory_search.retrieve_with_context", map[string]interface{}{
						"context_count": len(contextIDs),
					})()
					var err error
					result, err = i.graphDB.RetrieveWithContext(queryEmb, query, contextIDs, limit)
					if err != nil {
						return nil, fmt.Errorf("context retrieval failed: %w", err)
					}
				}
			}
		}
	} else {
		// Use standard retrieval without context
		defer profiling.Get().StartWithMetadata("search_memory", "memory_search.retrieve", map[string]interface{}{
			"mode": "standard",
		})()
		var err error
		result, err = i.graphDB.Retrieve(queryEmb, query, limit)
		if err != nil {
			return nil, fmt.Errorf("retrieval failed: %w", err)
		}
	}

	// Convert to SearchResult (track timing for result processing)
	var results []SearchResult
	{
		defer profiling.Get().StartWithMetadata("search_memory", "memory_search.format_results", map[string]interface{}{
			"result_count": len(result.Traces),
		})()
		for _, t := range result.Traces {
			results = append(results, SearchResult{
				ID:         t.ID,
				Summary:    t.Summary,
				Activation: t.Activation,
				CreatedAt:  t.CreatedAt,
			})
		}
	}

	return results, nil
}

// TraceContext provides detailed context for a trace including source episodes and linked entities
type TraceContext struct {
	Trace    TraceSummary     `json:"trace"`
	Episodes []EpisodeSummary `json:"source_episodes"`
	Entities []EntitySummary  `json:"linked_entities"`
}

// GetTraceContext retrieves detailed context for a trace
func (i *Inspector) GetTraceContext(traceID string) (*TraceContext, error) {
	if i.graphDB == nil {
		return nil, fmt.Errorf("graph database not initialized")
	}

	// Get the trace
	trace, err := i.graphDB.GetTrace(traceID)
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

	// Get source episodes
	episodeIDs, err := i.graphDB.GetTraceSources(traceID)
	if err == nil {
		for _, epID := range episodeIDs {
			ep, err := i.graphDB.GetEpisode(epID)
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

	// Get linked entities
	entityIDs, err := i.graphDB.GetTraceEntities(traceID)
	if err == nil {
		for _, entID := range entityIDs {
			ent, err := i.graphDB.GetEntity(entID)
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

// QueryTrace runs a question against the source episodes of a trace using the LLM.
// level parameter controls compression: 0=raw episodes, 1=L1 summary (default), 2=L2 summary
func (i *Inspector) QueryTrace(traceID, question string, level int) (string, error) {
	if i.graphDB == nil {
		return "", fmt.Errorf("graph database not initialized")
	}

	// Get source episodes for context
	episodeIDs, err := i.graphDB.GetTraceSources(traceID)
	if err != nil {
		return "", fmt.Errorf("failed to get trace sources: %w", err)
	}

	if len(episodeIDs) == 0 {
		return "", fmt.Errorf("trace has no source episodes")
	}

	// Validate compression level
	if level < 0 || level > 2 {
		return "", fmt.Errorf("invalid compression level %d (must be 0-2)", level)
	}

	// Gather episode content (raw or compressed)
	var context strings.Builder
	var usedCompression bool
	for _, epID := range episodeIDs {
		ep, err := i.graphDB.GetEpisode(epID)
		if err != nil || ep == nil {
			continue
		}

		var content string
		if level > 0 {
			// Try to get compressed summary
			summary, err := i.graphDB.GetEpisodeSummary(epID, level)
			if err == nil && summary != nil {
				content = summary.Summary
				usedCompression = true
			} else {
				// Fall back to raw content if summary not available
				content = ep.Content
			}
		} else {
			// Level 0: use raw content
			content = ep.Content
		}

		context.WriteString(fmt.Sprintf("[%s] %s: %s\n",
			ep.TimestampEvent.Format("2006-01-02 15:04"), ep.Author, content))
	}

	if context.Len() == 0 {
		return "", fmt.Errorf("no episode content available")
	}

	// Build result string
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Context from %d source episodes", len(episodeIDs)))
	if usedCompression {
		result.WriteString(fmt.Sprintf(" (compression level %d)", level))
	}
	result.WriteString(":\n\n")
	result.WriteString(context.String())

	if question != "" {
		result.WriteString("\n(LLM query not yet implemented - add Generator interface to Inspector)\n")
		result.WriteString(fmt.Sprintf("Question: %s\n", question))
	}

	return result.String(), nil
}

// Helper methods

func (i *Inspector) loadPercepts() ([]*types.Percept, error) {
	path := filepath.Join(i.statePath, "system", "queues", "percepts.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var file struct {
		Percepts []*types.Percept `json:"percepts"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.Percepts, nil
}

func (i *Inspector) savePercepts(percepts []*types.Percept) error {
	path := filepath.Join(i.statePath, "system", "queues", "percepts.json")
	file := struct {
		Percepts []*types.Percept `json:"percepts"`
	}{Percepts: percepts}
	if file.Percepts == nil {
		file.Percepts = []*types.Percept{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (i *Inspector) loadThreads() ([]*types.Thread, error) {
	path := filepath.Join(i.statePath, "system", "threads.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var file struct {
		Threads []*types.Thread `json:"threads"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.Threads, nil
}

func (i *Inspector) saveThreads(threads []*types.Thread) error {
	path := filepath.Join(i.statePath, "system", "threads.json")
	file := struct {
		Threads []*types.Thread `json:"threads"`
	}{Threads: threads}
	if file.Threads == nil {
		file.Threads = []*types.Thread{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (i *Inspector) countJSONL(name string) int {
	path := filepath.Join(i.statePath, name)
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if len(strings.TrimSpace(scanner.Text())) > 0 {
			count++
		}
	}
	return count
}

func (i *Inspector) tailJSONL(name string, count int) []map[string]any {
	path := filepath.Join(i.statePath, name)
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}

	// Take last N
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}

	var result []map[string]any
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err == nil {
			result = append(result, entry)
		}
	}
	return result
}

func (i *Inspector) truncateJSONL(name string, keep int) error {
	path := filepath.Join(i.statePath, name)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	file.Close()

	// Keep last N
	if len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}

	// Rewrite file
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}
