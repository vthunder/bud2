package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AddEntity adds a new entity to the graph
func (g *DB) AddEntity(e *Entity) error {
	if e.ID == "" {
		return fmt.Errorf("entity ID is required")
	}

	embeddingBytes, err := json.Marshal(e.Embedding)
	if err != nil {
		embeddingBytes = nil
	}

	now := time.Now()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = now
	}

	_, err = g.db.Exec(`
		INSERT INTO entities (id, name, type, salience, embedding, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			salience = MAX(entities.salience, excluded.salience),
			embedding = excluded.embedding,
			updated_at = excluded.updated_at
	`,
		e.ID, e.Name, e.Type, e.Salience, embeddingBytes, e.CreatedAt, e.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to insert entity: %w", err)
	}

	// Add aliases
	for _, alias := range e.Aliases {
		g.AddEntityAlias(e.ID, alias)
	}

	return nil
}

// GetAllEntities retrieves all entities ordered by salience desc
func (g *DB) GetAllEntities(limit int) ([]*Entity, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := g.db.Query(`
		SELECT id, name, type, salience, embedding, created_at, updated_at
		FROM entities
		ORDER BY salience DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query entities: %w", err)
	}
	defer rows.Close()

	entities, err := scanEntityRows(rows)
	if err != nil {
		return nil, err
	}

	// Load aliases for each
	for _, e := range entities {
		e.Aliases, _ = g.GetEntityAliases(e.ID)
	}

	return entities, nil
}

// CountEntities returns the total number of entities
func (g *DB) CountEntities() (int, error) {
	var count int
	err := g.db.QueryRow(`SELECT COUNT(*) FROM entities`).Scan(&count)
	return count, err
}

// GetEntity retrieves an entity by ID
func (g *DB) GetEntity(id string) (*Entity, error) {
	row := g.db.QueryRow(`
		SELECT id, name, type, salience, embedding, created_at, updated_at
		FROM entities WHERE id = ?
	`, id)

	e, err := scanEntity(row)
	if err != nil || e == nil {
		return e, err
	}

	// Load aliases
	e.Aliases, _ = g.GetEntityAliases(id)
	return e, nil
}

// FindEntityByName finds an entity by canonical name or alias
func (g *DB) FindEntityByName(name string) (*Entity, error) {
	// Try canonical name first
	row := g.db.QueryRow(`
		SELECT id, name, type, salience, embedding, created_at, updated_at
		FROM entities WHERE name = ?
	`, name)

	e, err := scanEntity(row)
	if err == nil && e != nil {
		e.Aliases, _ = g.GetEntityAliases(e.ID)
		return e, nil
	}

	// Try alias
	var entityID string
	err = g.db.QueryRow(`
		SELECT entity_id FROM entity_aliases WHERE alias = ?
	`, name).Scan(&entityID)

	if err != nil {
		return nil, nil // Not found
	}

	return g.GetEntity(entityID)
}

// FindSimilarEntity finds an entity similar to the given embedding
func (g *DB) FindSimilarEntity(embedding []float64, threshold float64) (*Entity, error) {
	rows, err := g.db.Query(`
		SELECT id, name, type, salience, embedding, created_at, updated_at
		FROM entities WHERE embedding IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bestEntity *Entity
	bestScore := 0.0

	for rows.Next() {
		e, err := scanEntityRow(rows)
		if err != nil {
			continue
		}

		sim := cosineSimilarity(embedding, e.Embedding)
		if sim >= threshold && sim > bestScore {
			bestScore = sim
			bestEntity = e
		}
	}

	return bestEntity, nil
}

// AddEntityAlias adds an alias for an entity
func (g *DB) AddEntityAlias(entityID, alias string) error {
	_, err := g.db.Exec(`
		INSERT OR IGNORE INTO entity_aliases (entity_id, alias)
		VALUES (?, ?)
	`, entityID, alias)
	return err
}

// GetEntityAliases returns all aliases for an entity
func (g *DB) GetEntityAliases(entityID string) ([]string, error) {
	rows, err := g.db.Query(`
		SELECT alias FROM entity_aliases WHERE entity_id = ?
	`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var aliases []string
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			continue
		}
		aliases = append(aliases, alias)
	}
	return aliases, nil
}

// LinkEpisodeToEntity links an episode to an entity (mention)
func (g *DB) LinkEpisodeToEntity(episodeID, entityID string) error {
	_, err := g.db.Exec(`
		INSERT OR IGNORE INTO episode_mentions (episode_id, entity_id)
		VALUES (?, ?)
	`, episodeID, entityID)
	return err
}

// AddEntityRelation adds a relationship between two entities
func (g *DB) AddEntityRelation(fromID, toID string, relType EdgeType, weight float64) error {
	_, err := g.db.Exec(`
		INSERT INTO entity_relations (from_id, to_id, relation_type, weight)
		VALUES (?, ?, ?, ?)
	`, fromID, toID, relType, weight)
	return err
}

// GetEntitiesForEpisode returns all entities mentioned in an episode
func (g *DB) GetEntitiesForEpisode(episodeID string) ([]*Entity, error) {
	rows, err := g.db.Query(`
		SELECT e.id, e.name, e.type, e.salience, e.embedding, e.created_at, e.updated_at
		FROM entities e
		INNER JOIN episode_mentions em ON em.entity_id = e.id
		WHERE em.episode_id = ?
	`, episodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEntityRows(rows)
}

// GetEpisodesForEntity returns all episodes that mention an entity
func (g *DB) GetEpisodesForEntity(entityID string) ([]string, error) {
	rows, err := g.db.Query(`
		SELECT episode_id FROM episode_mentions WHERE entity_id = ?
	`, entityID)
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

// GetTracesForEntity returns all traces that involve an entity
func (g *DB) GetTracesForEntity(entityID string) ([]string, error) {
	rows, err := g.db.Query(`
		SELECT trace_id FROM trace_entities WHERE entity_id = ?
	`, entityID)
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

// GetEntityRelations returns relations to/from an entity
func (g *DB) GetEntityRelations(entityID string) ([]Neighbor, error) {
	rows, err := g.db.Query(`
		SELECT to_id, weight, relation_type FROM entity_relations WHERE from_id = ?
		UNION ALL
		SELECT from_id, weight, relation_type FROM entity_relations WHERE to_id = ?
	`, entityID, entityID)

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

// IncrementEntitySalience increases the salience of an entity
func (g *DB) IncrementEntitySalience(entityID string, increment float64) error {
	_, err := g.db.Exec(`
		UPDATE entities SET salience = salience + ?, updated_at = ? WHERE id = ?
	`, increment, time.Now(), entityID)
	return err
}

// scanEntity scans a single row into an Entity
func scanEntity(row *sql.Row) (*Entity, error) {
	var e Entity
	var embeddingBytes []byte
	var entityType string

	err := row.Scan(
		&e.ID, &e.Name, &entityType, &e.Salience, &embeddingBytes,
		&e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	e.Type = EntityType(entityType)

	if len(embeddingBytes) > 0 {
		json.Unmarshal(embeddingBytes, &e.Embedding)
	}

	return &e, nil
}

// scanEntityRow scans from rows (multiple rows)
func scanEntityRow(rows *sql.Rows) (*Entity, error) {
	var e Entity
	var embeddingBytes []byte
	var entityType string

	err := rows.Scan(
		&e.ID, &e.Name, &entityType, &e.Salience, &embeddingBytes,
		&e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	e.Type = EntityType(entityType)

	if len(embeddingBytes) > 0 {
		json.Unmarshal(embeddingBytes, &e.Embedding)
	}

	return &e, nil
}

// scanEntityRows scans multiple rows into Entities
func scanEntityRows(rows *sql.Rows) ([]*Entity, error) {
	var entities []*Entity
	for rows.Next() {
		e, err := scanEntityRow(rows)
		if err != nil {
			continue
		}
		entities = append(entities, e)
	}
	return entities, nil
}
