package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

// AddTrace adds a new trace to the graph
func (g *DB) AddTrace(tr *Trace) error {
	if tr.ID == "" {
		return fmt.Errorf("trace ID is required")
	}

	// Generate short ID if not set
	if tr.ShortID == "" {
		tr.ShortID = generateShortID(tr.ID)
	}

	embeddingBytes, err := json.Marshal(tr.Embedding)
	if err != nil {
		embeddingBytes = nil
	}

	if tr.CreatedAt.IsZero() {
		tr.CreatedAt = time.Now()
	}
	if tr.LastAccessed.IsZero() {
		tr.LastAccessed = time.Now()
	}

	traceType := tr.TraceType
	if traceType == "" {
		traceType = TraceTypeKnowledge
	}

	_, err = g.db.Exec(`
		INSERT INTO traces (id, short_id, topic, trace_type, activation, strength,
			embedding, created_at, last_accessed, labile_until)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			short_id = excluded.short_id,
			trace_type = excluded.trace_type,
			activation = excluded.activation,
			strength = excluded.strength,
			embedding = excluded.embedding,
			last_accessed = excluded.last_accessed,
			labile_until = excluded.labile_until
	`,
		tr.ID, tr.ShortID, tr.Topic, string(traceType), tr.Activation, tr.Strength,
		embeddingBytes, tr.CreatedAt, tr.LastAccessed, nullableTime(tr.LabileUntil),
	)

	if err != nil {
		return fmt.Errorf("failed to insert trace: %w", err)
	}

	return nil
}

// GetTrace retrieves a trace by ID
// Tries compression levels preferring higher detail: 64, 32, 16, 8, 4 (most→least detail)
// Falls back to empty string if no summary is available
func (g *DB) GetTrace(id string) (*Trace, error) {
	// Try to get summary with fallback across compression levels
	row := g.db.QueryRow(`
		SELECT t.id, t.short_id,
			COALESCE(
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 64 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 32 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 16 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 8 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 4 LIMIT 1),
				''
			) as summary,
			t.topic, t.trace_type,
			t.activation, t.strength, t.embedding, t.created_at, t.last_accessed, t.labile_until
		FROM traces t
		WHERE t.id = ?
	`, id)

	return scanTrace(row)
}

// GetTraceByShortID retrieves a trace by its short ID
func (g *DB) GetTraceByShortID(shortID string) (*Trace, error) {
	row := g.db.QueryRow(`
		SELECT t.id, t.short_id,
			COALESCE(
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 64 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 32 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 16 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 8 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 4 LIMIT 1),
				''
			) as summary,
			t.topic, t.trace_type,
			t.activation, t.strength, t.embedding, t.created_at, t.last_accessed, t.labile_until
		FROM traces t
		WHERE t.short_id = ?
	`, shortID)

	return scanTrace(row)
}

// GetCoreTraces retrieves all core identity traces
func (g *DB) GetCoreTraces() ([]*Trace, error) {
	// Deprecated: Core identity now loaded from state/system/core.md
	return []*Trace{}, nil
}

// GetActivatedTraces retrieves traces with activation above threshold
// Tries compression levels preferring higher detail: 64, 32, 16, 8, 4 (most→least detail)
func (g *DB) GetActivatedTraces(threshold float64, limit int) ([]*Trace, error) {
	rows, err := g.db.Query(`
		SELECT t.id, t.short_id,
			COALESCE(
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 64 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 32 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 16 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 8 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 4 LIMIT 1),
				''
			) as summary,
			t.topic, t.trace_type,
			t.activation, t.strength, t.embedding, t.created_at, t.last_accessed, t.labile_until
		FROM traces t
		WHERE t.activation >= ?
		ORDER BY t.activation DESC
		LIMIT ?
	`, threshold, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query activated traces: %w", err)
	}
	defer rows.Close()

	return scanTraceRows(rows)
}

// UpdateTraceActivation updates the activation level of a trace
func (g *DB) UpdateTraceActivation(id string, activation float64) error {
	_, err := g.db.Exec(`
		UPDATE traces SET activation = ?, last_accessed = ? WHERE id = ?
	`, activation, time.Now(), id)
	return err
}

// ReinforceTrace increments strength and updates embedding
func (g *DB) ReinforceTrace(id string, newEmbedding []float64, alpha float64) error {
	// Get current trace
	trace, err := g.GetTrace(id)
	if err != nil {
		return err
	}
	if trace == nil {
		return fmt.Errorf("trace not found: %s", id)
	}

	// Update embedding with exponential moving average
	if len(trace.Embedding) > 0 && len(newEmbedding) > 0 {
		for i := range trace.Embedding {
			if i < len(newEmbedding) {
				trace.Embedding[i] = alpha*newEmbedding[i] + (1-alpha)*trace.Embedding[i]
			}
		}
	} else if len(newEmbedding) > 0 {
		trace.Embedding = newEmbedding
	}

	embeddingBytes, _ := json.Marshal(trace.Embedding)

	_, err = g.db.Exec(`
		UPDATE traces SET
			strength = strength + 1,
			embedding = ?,
			last_accessed = ?
		WHERE id = ?
	`, embeddingBytes, time.Now(), id)

	return err
}

// DecayActivation decays all trace activations by the given factor
func (g *DB) DecayActivation(factor float64) error {
	_, err := g.db.Exec(`
		UPDATE traces SET activation = activation * ?
	`, factor)
	return err
}

// DecayActivationByAge applies time-based decay to all non-core traces based on
// time since last access. Uses exponential decay: activation *= exp(-lambda * hours_since_access)
// This differentiates traces by recency — recently accessed traces keep high activation,
// old untouched traces decay toward a floor.
// Operational traces decay 3x faster than knowledge traces.
func (g *DB) DecayActivationByAge(lambda float64, floor float64) (int, error) {
	now := time.Now()

	rows, err := g.db.Query(`
		SELECT id, activation, last_accessed, COALESCE(trace_type, 'knowledge')
		FROM traces WHERE activation > ?
	`, floor)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type update struct {
		id            string
		newActivation float64
	}
	var updates []update

	for rows.Next() {
		var id string
		var activation float64
		var lastAccessed time.Time
		var traceType string
		if err := rows.Scan(&id, &activation, &lastAccessed, &traceType); err != nil {
			continue
		}

		hoursSinceAccess := now.Sub(lastAccessed).Hours()
		if hoursSinceAccess < 0 {
			hoursSinceAccess = 0
		}

		// Operational traces decay 3x faster (~36%/day vs ~12%/day for knowledge)
		effectiveLambda := lambda
		if traceType == string(TraceTypeOperational) {
			effectiveLambda = lambda * 3
		}

		decayFactor := math.Exp(-effectiveLambda * hoursSinceAccess)
		newActivation := activation * decayFactor
		if newActivation < floor {
			newActivation = floor
		}

		if newActivation != activation {
			updates = append(updates, update{id: id, newActivation: newActivation})
		}
	}

	for _, u := range updates {
		g.db.Exec(`UPDATE traces SET activation = ? WHERE id = ?`, u.newActivation, u.id)
	}

	return len(updates), nil
}

// BoostTraceAccess updates last_accessed and boosts activation for traces that were
// retrieved and shown to the user. This keeps actively-used memories alive.
func (g *DB) BoostTraceAccess(traceIDs []string, boost float64) error {
	now := time.Now()
	for _, id := range traceIDs {
		_, err := g.db.Exec(`
			UPDATE traces SET
				last_accessed = ?,
				activation = MIN(1.0, activation + ?)
			WHERE id = ?
		`, now, boost, id)
		if err != nil {
			continue
		}
	}
	return nil
}

// LinkTraceToSource links a trace to a source episode
func (g *DB) LinkTraceToSource(traceID, episodeID string) error {
	_, err := g.db.Exec(`
		INSERT OR IGNORE INTO trace_sources (trace_id, episode_id)
		VALUES (?, ?)
	`, traceID, episodeID)
	return err
}

// LinkTraceToEntity links a trace to an entity
func (g *DB) LinkTraceToEntity(traceID, entityID string) error {
	_, err := g.db.Exec(`
		INSERT OR IGNORE INTO trace_entities (trace_id, entity_id)
		VALUES (?, ?)
	`, traceID, entityID)
	return err
}

// AddTraceRelation adds a relationship between two traces
func (g *DB) AddTraceRelation(fromID, toID string, relType EdgeType, weight float64) error {
	_, err := g.db.Exec(`
		INSERT INTO trace_relations (from_id, to_id, relation_type, weight)
		VALUES (?, ?, ?, ?)
	`, fromID, toID, relType, weight)
	return err
}

// GetTraceNeighbors returns neighbors of a trace for spreading activation.
// Merges direct trace-to-trace relations with entity-bridged neighbors.
func (g *DB) GetTraceNeighbors(id string) ([]Neighbor, error) {
	// Get direct trace-to-trace neighbors
	direct, err := g.getDirectTraceNeighbors(id)
	if err != nil {
		return nil, err
	}

	// Get entity-bridged neighbors
	bridged, err := g.GetTraceNeighborsThroughEntities(id, MaxEdgesPerNode)
	if err != nil {
		// Non-fatal: fall back to direct only
		bridged = nil
	}

	// Merge: direct edges take priority, entity-bridged fill in
	seen := make(map[string]bool)
	var merged []Neighbor
	for _, n := range direct {
		seen[n.ID] = true
		merged = append(merged, n)
	}
	for _, n := range bridged {
		if !seen[n.ID] {
			seen[n.ID] = true
			merged = append(merged, n)
		}
	}

	// Cap at MaxEdgesPerNode
	if len(merged) > MaxEdgesPerNode {
		// Sort by weight descending to keep strongest edges
		sort.Slice(merged, func(i, j int) bool {
			return merged[i].Weight > merged[j].Weight
		})
		merged = merged[:MaxEdgesPerNode]
	}

	return merged, nil
}

// getDirectTraceNeighbors returns neighbors from direct trace_relations edges.
func (g *DB) getDirectTraceNeighbors(id string) ([]Neighbor, error) {
	rows, err := g.db.Query(`
		SELECT to_id, weight, relation_type FROM trace_relations WHERE from_id = ?
		UNION ALL
		SELECT from_id, weight, relation_type FROM trace_relations WHERE to_id = ?
	`, id, id)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var neighbors []Neighbor
	for rows.Next() {
		var n Neighbor
		var relType string
		if err := rows.Scan(&n.ID, &n.Weight, &relType); err != nil {
			continue
		}
		n.Type = EdgeType(relType)
		neighbors = append(neighbors, n)
	}

	return neighbors, nil
}

// GetTraceNeighborsThroughEntities finds traces that share entities with the given trace.
// Edge weight is min(1.0, shared_count * 0.3).
func (g *DB) GetTraceNeighborsThroughEntities(traceID string, maxNeighbors int) ([]Neighbor, error) {
	if maxNeighbors <= 0 {
		maxNeighbors = MaxEdgesPerNode
	}

	rows, err := g.db.Query(`
		SELECT te2.trace_id, COUNT(DISTINCT te1.entity_id) as shared, AVG(e.salience) as sal
		FROM trace_entities te1
		JOIN trace_entities te2 ON te1.entity_id = te2.entity_id
		JOIN entities e ON e.id = te1.entity_id
		WHERE te1.trace_id = ? AND te2.trace_id != ?
		GROUP BY te2.trace_id
		ORDER BY shared DESC, sal DESC
		LIMIT ?
	`, traceID, traceID, maxNeighbors)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var neighbors []Neighbor
	for rows.Next() {
		var neighborID string
		var sharedCount int
		var salience float64
		if err := rows.Scan(&neighborID, &sharedCount, &salience); err != nil {
			continue
		}

		weight := float64(sharedCount) * 0.3
		if weight > 1.0 {
			weight = 1.0
		}

		neighbors = append(neighbors, Neighbor{
			ID:     neighborID,
			Weight: weight,
			Type:   EdgeSharedEntity,
		})
	}

	return neighbors, nil
}

// GetTraceEntities returns the entity IDs linked to a trace
func (g *DB) GetTraceEntities(traceID string) ([]string, error) {
	rows, err := g.db.Query(`
		SELECT entity_id FROM trace_entities WHERE trace_id = ?
	`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetTraceSources returns the source episode IDs for a trace
func (g *DB) GetTraceSources(traceID string) ([]string, error) {
	rows, err := g.db.Query(`
		SELECT episode_id FROM trace_sources WHERE trace_id = ?
	`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetAllTraces retrieves all traces
// Tries compression levels preferring higher detail: 64, 32, 16, 8, 4 (most→least detail)
// Falls back to empty string if no summary is available
func (g *DB) GetAllTraces() ([]*Trace, error) {
	rows, err := g.db.Query(`
		SELECT t.id, t.short_id,
			COALESCE(
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 64 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 32 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 16 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 8 LIMIT 1),
				(SELECT summary FROM trace_summaries WHERE trace_id = t.id AND compression_level = 4 LIMIT 1),
				''
			) as summary,
			t.topic, t.trace_type,
			t.activation, t.strength, t.embedding, t.created_at, t.last_accessed, t.labile_until
		FROM traces t
		ORDER BY t.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query traces: %w", err)
	}
	defer rows.Close()

	return scanTraceRows(rows)
}


// DeleteTrace deletes a trace by ID
func (g *DB) DeleteTrace(id string) error {
	// Delete related data first
	g.db.Exec(`DELETE FROM trace_relations WHERE from_id = ? OR to_id = ?`, id, id)
	g.db.Exec(`DELETE FROM trace_entities WHERE trace_id = ?`, id)
	g.db.Exec(`DELETE FROM trace_sources WHERE trace_id = ?`, id)

	result, err := g.db.Exec(`DELETE FROM traces WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete trace: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("trace not found: %s", id)
	}
	return nil
}

// ClearTraces deletes traces. If coreOnly is true, only clears core traces.
// If coreOnly is false, clears non-core traces.
func (g *DB) ClearTraces(coreOnly bool) (int, error) {
	// Get IDs to delete
	var rows *sql.Rows
	var err error
	if coreOnly {
		rows, err = g.db.Query(`SELECT id FROM traces`)
	} else {
		rows, err = g.db.Query(`SELECT id FROM traces`)
	}
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}

	// Delete each trace (handles related data)
	for _, id := range ids {
		g.DeleteTrace(id)
	}

	return len(ids), nil
}

// CountTraces returns the count of traces (total and core)
func (g *DB) CountTraces() (total int, core int, err error) {
	err = g.db.QueryRow(`SELECT COUNT(*) FROM traces`).Scan(&total)
	if err != nil {
		return 0, 0, err
	}
	return total, 0, nil
}

// scanTrace scans a single row into a Trace
func scanTrace(row *sql.Row) (*Trace, error) {
	var tr Trace
	var embeddingBytes []byte
	var summary sql.NullString
	var topic sql.NullString
	var traceType sql.NullString
	var labileUntil sql.NullTime

	err := row.Scan(
		&tr.ID, &tr.ShortID, &summary, &topic, &traceType, &tr.Activation, &tr.Strength,
		&embeddingBytes, &tr.CreatedAt, &tr.LastAccessed, &labileUntil,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	tr.Summary = summary.String
	tr.Topic = topic.String
	tr.TraceType = TraceType(traceType.String)
	if tr.TraceType == "" {
		tr.TraceType = TraceTypeKnowledge
	}
	if labileUntil.Valid {
		tr.LabileUntil = labileUntil.Time
	}

	if len(embeddingBytes) > 0 {
		json.Unmarshal(embeddingBytes, &tr.Embedding)
	}

	return &tr, nil
}

// scanTraceRows scans multiple rows into Traces
func scanTraceRows(rows *sql.Rows) ([]*Trace, error) {
	var traces []*Trace
	for rows.Next() {
		var tr Trace
		var embeddingBytes []byte
		var summary sql.NullString
		var topic sql.NullString
		var traceType sql.NullString
		var labileUntil sql.NullTime

		err := rows.Scan(
			&tr.ID, &tr.ShortID, &summary, &topic, &traceType, &tr.Activation, &tr.Strength,
			&embeddingBytes, &tr.CreatedAt, &tr.LastAccessed, &labileUntil,
		)
		if err != nil {
			continue
		}

		tr.Summary = summary.String
		tr.Topic = topic.String
		tr.TraceType = TraceType(traceType.String)
		if tr.TraceType == "" {
			tr.TraceType = TraceTypeKnowledge
		}
		if labileUntil.Valid {
			tr.LabileUntil = labileUntil.Time
		}

		if len(embeddingBytes) > 0 {
			json.Unmarshal(embeddingBytes, &tr.Embedding)
		}

		traces = append(traces, &tr)
	}
	return traces, nil
}

// nullableTime converts a time.Time to sql.NullTime
func nullableTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{Valid: false}
	}
	return sql.NullTime{Time: t, Valid: true}
}
