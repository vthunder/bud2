package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AddTrace adds a new trace to the graph
func (g *DB) AddTrace(tr *Trace) error {
	if tr.ID == "" {
		return fmt.Errorf("trace ID is required")
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

	_, err = g.db.Exec(`
		INSERT INTO traces (id, summary, topic, activation, strength, is_core,
			embedding, created_at, last_accessed, labile_until)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			summary = excluded.summary,
			activation = excluded.activation,
			strength = excluded.strength,
			embedding = excluded.embedding,
			last_accessed = excluded.last_accessed,
			labile_until = excluded.labile_until
	`,
		tr.ID, tr.Summary, tr.Topic, tr.Activation, tr.Strength, tr.IsCore,
		embeddingBytes, tr.CreatedAt, tr.LastAccessed, nullableTime(tr.LabileUntil),
	)

	if err != nil {
		return fmt.Errorf("failed to insert trace: %w", err)
	}

	return nil
}

// GetTrace retrieves a trace by ID
func (g *DB) GetTrace(id string) (*Trace, error) {
	row := g.db.QueryRow(`
		SELECT id, summary, topic, activation, strength, is_core,
			embedding, created_at, last_accessed, labile_until
		FROM traces WHERE id = ?
	`, id)

	return scanTrace(row)
}

// GetCoreTraces retrieves all core identity traces
func (g *DB) GetCoreTraces() ([]*Trace, error) {
	rows, err := g.db.Query(`
		SELECT id, summary, topic, activation, strength, is_core,
			embedding, created_at, last_accessed, labile_until
		FROM traces WHERE is_core = TRUE
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query core traces: %w", err)
	}
	defer rows.Close()

	return scanTraceRows(rows)
}

// GetActivatedTraces retrieves traces with activation above threshold
func (g *DB) GetActivatedTraces(threshold float64, limit int) ([]*Trace, error) {
	rows, err := g.db.Query(`
		SELECT id, summary, topic, activation, strength, is_core,
			embedding, created_at, last_accessed, labile_until
		FROM traces
		WHERE activation >= ? AND is_core = FALSE
		ORDER BY activation DESC
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
		UPDATE traces SET activation = activation * ? WHERE is_core = FALSE
	`, factor)
	return err
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

// GetTraceNeighbors returns neighbors of a trace for spreading activation
func (g *DB) GetTraceNeighbors(id string) ([]Neighbor, error) {
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
func (g *DB) GetAllTraces() ([]*Trace, error) {
	rows, err := g.db.Query(`
		SELECT id, summary, topic, activation, strength, is_core,
			embedding, created_at, last_accessed, labile_until
		FROM traces
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query traces: %w", err)
	}
	defer rows.Close()

	return scanTraceRows(rows)
}

// SetTraceCore updates the is_core flag of a trace
func (g *DB) SetTraceCore(id string, isCore bool) error {
	result, err := g.db.Exec(`
		UPDATE traces SET is_core = ? WHERE id = ?
	`, isCore, id)
	if err != nil {
		return fmt.Errorf("failed to update trace: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("trace not found: %s", id)
	}
	return nil
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
		rows, err = g.db.Query(`SELECT id FROM traces WHERE is_core = TRUE`)
	} else {
		rows, err = g.db.Query(`SELECT id FROM traces WHERE is_core = FALSE`)
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
	err = g.db.QueryRow(`SELECT COUNT(*) FROM traces WHERE is_core = TRUE`).Scan(&core)
	if err != nil {
		return total, 0, err
	}
	return total, core, nil
}

// scanTrace scans a single row into a Trace
func scanTrace(row *sql.Row) (*Trace, error) {
	var tr Trace
	var embeddingBytes []byte
	var topic sql.NullString
	var labileUntil sql.NullTime

	err := row.Scan(
		&tr.ID, &tr.Summary, &topic, &tr.Activation, &tr.Strength, &tr.IsCore,
		&embeddingBytes, &tr.CreatedAt, &tr.LastAccessed, &labileUntil,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	tr.Topic = topic.String
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
		var topic sql.NullString
		var labileUntil sql.NullTime

		err := rows.Scan(
			&tr.ID, &tr.Summary, &topic, &tr.Activation, &tr.Strength, &tr.IsCore,
			&embeddingBytes, &tr.CreatedAt, &tr.LastAccessed, &labileUntil,
		)
		if err != nil {
			continue
		}

		tr.Topic = topic.String
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
