package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// EntityExists checks if an entity with the given ID exists
func (g *DB) EntityExists(id string) (bool, error) {
	var count int
	err := g.db.QueryRow("SELECT COUNT(*) FROM entities WHERE id = ?", id).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

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

	g.invalidateEntityCache()
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

// GetEntitiesByType retrieves entities of a specific type
func (g *DB) GetEntitiesByType(entityType EntityType, limit int) ([]*Entity, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := g.db.Query(`
		SELECT id, name, type, salience, embedding, created_at, updated_at
		FROM entities
		WHERE type = ?
		ORDER BY salience DESC
		LIMIT ?
	`, entityType, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query entities by type: %w", err)
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

// FindEntityByName finds an entity by canonical name or alias (case-insensitive)
func (g *DB) FindEntityByName(name string) (*Entity, error) {
	// Try canonical name first (case-insensitive)
	row := g.db.QueryRow(`
		SELECT id, name, type, salience, embedding, created_at, updated_at
		FROM entities WHERE LOWER(name) = LOWER(?)
	`, name)

	e, err := scanEntity(row)
	if err == nil && e != nil {
		e.Aliases, _ = g.GetEntityAliases(e.ID)
		return e, nil
	}

	// Try alias (case-insensitive)
	var entityID string
	err = g.db.QueryRow(`
		SELECT entity_id FROM entity_aliases WHERE LOWER(alias) = LOWER(?)
	`, name).Scan(&entityID)

	if err != nil {
		return nil, nil // Not found
	}

	return g.GetEntity(entityID)
}

// AddEntityAlias adds an alias for an entity
func (g *DB) AddEntityAlias(entityID, alias string) error {
	_, err := g.db.Exec(`
		INSERT OR IGNORE INTO entity_aliases (entity_id, alias)
		VALUES (?, ?)
	`, entityID, alias)
	if err == nil {
		g.invalidateEntityCache()
	}
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
	_, err := g.AddEntityRelationWithSource(fromID, toID, relType, weight, "")
	return err
}

// AddEntityRelationWithSource adds a relationship with source episode tracking.
// Returns the relation ID for use in invalidation tracking.
// Deduplicates: if an identical active (from_id, to_id, relation_type) already exists,
// returns the existing relation ID without creating a new row.
func (g *DB) AddEntityRelationWithSource(fromID, toID string, relType EdgeType, weight float64, sourceEpisodeID string) (int64, error) {
	// Check for existing active relation with same (from, to, type)
	var existingID int64
	err := g.db.QueryRow(`
		SELECT id FROM entity_relations
		WHERE from_id = ? AND to_id = ? AND relation_type = ? AND invalid_at IS NULL
		LIMIT 1
	`, fromID, toID, relType).Scan(&existingID)
	if err == nil {
		// Duplicate found — return existing ID, no insert
		return existingID, nil
	}
	// err == sql.ErrNoRows means no duplicate; proceed with insert

	var result sql.Result
	if sourceEpisodeID == "" {
		result, err = g.db.Exec(`
			INSERT INTO entity_relations (from_id, to_id, relation_type, weight)
			VALUES (?, ?, ?, ?)
		`, fromID, toID, relType, weight)
	} else {
		result, err = g.db.Exec(`
			INSERT INTO entity_relations (from_id, to_id, relation_type, weight, source_episode_id)
			VALUES (?, ?, ?, ?, ?)
		`, fromID, toID, relType, weight, sourceEpisodeID)
	}

	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// EntityRelation represents a relationship between entities with temporal validity
type EntityRelation struct {
	ID              int64
	FromID          string
	ToID            string
	RelationType    EdgeType
	Weight          float64
	ValidAt         time.Time
	InvalidAt       *time.Time
	InvalidatedBy   *int64
	SourceEpisodeID string
	CreatedAt       time.Time
}

// GetValidRelationsFor returns active (non-invalidated) relations involving an entity
func (g *DB) GetValidRelationsFor(entityID string) ([]EntityRelation, error) {
	rows, err := g.db.Query(`
		SELECT id, from_id, to_id, relation_type, weight, valid_at, invalid_at, invalidated_by, source_episode_id, created_at
		FROM entity_relations
		WHERE (from_id = ? OR to_id = ?) AND invalid_at IS NULL
	`, entityID, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var relations []EntityRelation
	for rows.Next() {
		var r EntityRelation
		var relType string
		var invalidAt, sourceEpisodeID sql.NullString
		var invalidatedBy sql.NullInt64
		var validAt sql.NullTime

		err := rows.Scan(&r.ID, &r.FromID, &r.ToID, &relType, &r.Weight,
			&validAt, &invalidAt, &invalidatedBy, &sourceEpisodeID, &r.CreatedAt)
		if err != nil {
			continue
		}

		r.RelationType = EdgeType(relType)
		if validAt.Valid {
			r.ValidAt = validAt.Time
		}
		if invalidAt.Valid {
			t, _ := time.Parse(time.RFC3339, invalidAt.String)
			r.InvalidAt = &t
		}
		if invalidatedBy.Valid {
			r.InvalidatedBy = &invalidatedBy.Int64
		}
		r.SourceEpisodeID = sourceEpisodeID.String

		relations = append(relations, r)
	}
	return relations, nil
}

// FindInvalidationCandidates finds existing relations that might be invalidated by a new relation
// Returns relations of the same type involving the same subject entity
func (g *DB) FindInvalidationCandidates(subjectID string, relType EdgeType) ([]EntityRelation, error) {
	rows, err := g.db.Query(`
		SELECT id, from_id, to_id, relation_type, weight, valid_at, invalid_at, invalidated_by, source_episode_id, created_at
		FROM entity_relations
		WHERE from_id = ? AND relation_type = ? AND invalid_at IS NULL
	`, subjectID, relType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var relations []EntityRelation
	for rows.Next() {
		var r EntityRelation
		var relTypeStr string
		var invalidAt, sourceEpisodeID sql.NullString
		var invalidatedBy sql.NullInt64
		var validAt sql.NullTime

		err := rows.Scan(&r.ID, &r.FromID, &r.ToID, &relTypeStr, &r.Weight,
			&validAt, &invalidAt, &invalidatedBy, &sourceEpisodeID, &r.CreatedAt)
		if err != nil {
			continue
		}

		r.RelationType = EdgeType(relTypeStr)
		if validAt.Valid {
			r.ValidAt = validAt.Time
		}
		if invalidAt.Valid {
			t, _ := time.Parse(time.RFC3339, invalidAt.String)
			r.InvalidAt = &t
		}
		if invalidatedBy.Valid {
			r.InvalidatedBy = &invalidatedBy.Int64
		}
		r.SourceEpisodeID = sourceEpisodeID.String

		relations = append(relations, r)
	}
	return relations, nil
}

// InvalidateRelation marks a relation as invalid
func (g *DB) InvalidateRelation(relationID int64, invalidatedByID int64) error {
	_, err := g.db.Exec(`
		UPDATE entity_relations
		SET invalid_at = CURRENT_TIMESTAMP, invalidated_by = ?
		WHERE id = ?
	`, invalidatedByID, relationID)
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

// GetTracesForEntitiesBatch returns up to capPerEntity trace IDs for each of the given
// entity IDs in a single SQL query, deduplicated.
func (g *DB) GetTracesForEntitiesBatch(entityIDs []string, capPerEntity int) ([]string, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(entityIDs))
	args := make([]interface{}, len(entityIDs))
	for i, id := range entityIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`
		SELECT entity_id, trace_id FROM trace_entities
		WHERE entity_id IN (%s)
		ORDER BY entity_id
	`, strings.Join(placeholders, ","))

	rows, err := g.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Collect up to capPerEntity per entity, deduplicate overall
	seenTraces := make(map[string]bool)
	countPerEntity := make(map[string]int)
	var result []string
	for rows.Next() {
		var entityID, traceID string
		if err := rows.Scan(&entityID, &traceID); err != nil {
			continue
		}
		if countPerEntity[entityID] >= capPerEntity {
			continue
		}
		if seenTraces[traceID] {
			continue
		}
		countPerEntity[entityID]++
		seenTraces[traceID] = true
		result = append(result, traceID)
	}
	return result, nil
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

// EntityContext holds an entity and its known relationships for context injection into prompts.
type EntityContext struct {
	Entity    *Entity
	Relations []EntityContextRelation
}

// EntityContextRelation is a simplified view of an entity_relation for prompt injection.
type EntityContextRelation struct {
	RelationType string
	OtherName    string
	OtherType    string
	Direction    string // "outgoing" or "incoming"
}

// GetEntityContext returns an entity by name with its top N valid relationships.
// Used for context injection during relationship extraction.
func (g *DB) GetEntityContext(name string, maxRelationships int) (*EntityContext, error) {
	entity, err := g.FindEntityByName(name)
	if err != nil || entity == nil {
		return nil, err
	}

	relations, err := g.GetValidRelationsFor(entity.ID)
	if err != nil {
		return nil, err
	}

	// Collect other entity IDs we need to look up
	otherIDs := make(map[string]bool)
	for _, r := range relations {
		if r.FromID == entity.ID {
			otherIDs[r.ToID] = true
		} else {
			otherIDs[r.FromID] = true
		}
	}

	// Fetch other entity names in one pass
	nameByID := make(map[string]string)
	typeByID := make(map[string]string)
	for id := range otherIDs {
		other, err := g.GetEntity(id)
		if err == nil && other != nil {
			nameByID[id] = other.Name
			typeByID[id] = string(other.Type)
		}
	}

	// Build context relations, capped at maxRelationships
	ctx := &EntityContext{Entity: entity}
	for i, r := range relations {
		if maxRelationships > 0 && i >= maxRelationships {
			break
		}
		ecr := EntityContextRelation{RelationType: string(r.RelationType)}
		if r.FromID == entity.ID {
			ecr.Direction = "outgoing"
			ecr.OtherName = nameByID[r.ToID]
			ecr.OtherType = typeByID[r.ToID]
		} else {
			ecr.Direction = "incoming"
			ecr.OtherName = nameByID[r.FromID]
			ecr.OtherType = typeByID[r.FromID]
		}
		if ecr.OtherName != "" {
			ctx.Relations = append(ctx.Relations, ecr)
		}
	}

	return ctx, nil
}

// IncrementEntitySalience increases the salience of an entity
func (g *DB) IncrementEntitySalience(entityID string, increment float64) error {
	_, err := g.db.Exec(`
		UPDATE entities SET salience = salience + ?, updated_at = ? WHERE id = ?
	`, increment, time.Now(), entityID)
	if err == nil {
		g.invalidateEntityCache()
	}
	return err
}

// invalidateEntityCache marks the entity cache as stale. Called on any entity write.
func (g *DB) invalidateEntityCache() {
	g.entityCacheMu.Lock()
	g.entityCache = nil
	g.entityCacheMu.Unlock()
}

// getEntityCache returns the entity cache, rebuilding it if stale.
func (g *DB) getEntityCache() ([]entityCacheEntry, error) {
	g.entityCacheMu.RLock()
	cache := g.entityCache
	g.entityCacheMu.RUnlock()
	if cache != nil {
		return cache, nil
	}

	g.entityCacheMu.Lock()
	defer g.entityCacheMu.Unlock()
	// Double-check after acquiring write lock.
	if g.entityCache != nil {
		return g.entityCache, nil
	}

	entities, err := g.getAllEntitiesWithAliasesBatch(500)
	if err != nil {
		return nil, err
	}

	cache = make([]entityCacheEntry, 0, len(entities))
	for _, e := range entities {
		names := append([]string{e.Name}, e.Aliases...)
		var patterns []*regexp.Regexp
		for _, name := range names {
			if len(name) < 3 {
				patterns = append(patterns, nil) // skip but keep index aligned
				continue
			}
			escaped := regexp.QuoteMeta(strings.ToLower(name))
			re, err := regexp.Compile(`\b` + escaped + `\b`)
			if err != nil {
				patterns = append(patterns, nil)
				continue
			}
			patterns = append(patterns, re)
		}
		cache = append(cache, entityCacheEntry{entity: e, patterns: patterns})
	}
	g.entityCache = cache
	return cache, nil
}

// getAllEntitiesWithAliasesBatch loads up to limit entities and their aliases
// using two queries instead of 1+N.
func (g *DB) getAllEntitiesWithAliasesBatch(limit int) ([]*Entity, error) {
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
	entities, err := scanEntityRows(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(entities) == 0 {
		return entities, nil
	}

	// Build entity index for alias assignment
	byID := make(map[string]*Entity, len(entities))
	ids := make([]string, len(entities))
	for i, e := range entities {
		byID[e.ID] = e
		ids[i] = e.ID
	}

	// Single batch query for all aliases
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	aliasRows, err := g.db.Query(
		`SELECT entity_id, alias FROM entity_aliases WHERE entity_id IN (`+placeholders+`)`,
		args...,
	)
	if err == nil {
		defer aliasRows.Close()
		for aliasRows.Next() {
			var entityID, alias string
			if aliasRows.Scan(&entityID, &alias) == nil {
				if e, ok := byID[entityID]; ok {
					e.Aliases = append(e.Aliases, alias)
				}
			}
		}
	}

	return entities, nil
}

// FindEntitiesByText matches entity names and aliases against query text using
// word-boundary awareness. Returns up to maxResults entities sorted by salience.
// Uses a cached list of pre-compiled patterns to avoid per-call DB queries and regex compilation.
func (g *DB) FindEntitiesByText(queryText string, maxResults int) ([]*Entity, error) {
	if maxResults <= 0 {
		maxResults = 5
	}

	cache, err := g.getEntityCache()
	if err != nil {
		return nil, err
	}

	queryLower := strings.ToLower(queryText)

	var matches []*Entity
	for _, entry := range cache {
		for _, re := range entry.patterns {
			if re != nil && re.MatchString(queryLower) {
				matches = append(matches, entry.entity)
				break
			}
		}
	}

	// Cache is already salience-sorted; just cap the result.
	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}

	return matches, nil
}

// containsWholeWord checks if text contains word as a whole word (word-boundary aware).
// Kept for external use; internal FindEntitiesByText now uses pre-compiled cache.
func containsWholeWord(text, word string) bool {
	escaped := regexp.QuoteMeta(word)
	pattern := `(?i)\b` + escaped + `\b`
	re, err := regexp.Compile(pattern)
	if err != nil {
		return strings.Contains(text, word)
	}
	return re.MatchString(text)
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
