package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AddEpisode adds a new episode to the graph
func (g *DB) AddEpisode(ep *Episode) error {
	if ep.ID == "" {
		return fmt.Errorf("episode ID is required")
	}

	embeddingBytes, err := json.Marshal(ep.Embedding)
	if err != nil {
		embeddingBytes = nil
	}

	if ep.TimestampIngested.IsZero() {
		ep.TimestampIngested = time.Now()
	}
	if ep.CreatedAt.IsZero() {
		ep.CreatedAt = time.Now()
	}

	_, err = g.db.Exec(`
		INSERT INTO episodes (id, content, source, author, author_id, channel,
			timestamp_event, timestamp_ingested, dialogue_act, entropy_score,
			embedding, reply_to, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			embedding = excluded.embedding,
			entropy_score = excluded.entropy_score
	`,
		ep.ID, ep.Content, ep.Source, ep.Author, ep.AuthorID, ep.Channel,
		ep.TimestampEvent, ep.TimestampIngested, ep.DialogueAct, ep.EntropyScore,
		embeddingBytes, ep.ReplyTo, ep.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to insert episode: %w", err)
	}

	// Create reply edge if applicable
	if ep.ReplyTo != "" {
		_, _ = g.db.Exec(`
			INSERT OR IGNORE INTO episode_edges (from_id, to_id, edge_type, weight)
			VALUES (?, ?, ?, 1.0)
		`, ep.ID, ep.ReplyTo, EdgeRepliesTo)
	}

	return nil
}

// GetAllEpisodes retrieves episodes with optional limit, ordered by timestamp desc
func (g *DB) GetAllEpisodes(limit int) ([]*Episode, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := g.db.Query(`
		SELECT id, content, source, author, author_id, channel,
			timestamp_event, timestamp_ingested, dialogue_act, entropy_score,
			embedding, reply_to, created_at
		FROM episodes
		ORDER BY timestamp_event DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query episodes: %w", err)
	}
	defer rows.Close()

	var episodes []*Episode
	for rows.Next() {
		ep, err := scanEpisodeRow(rows)
		if err != nil {
			continue
		}
		episodes = append(episodes, ep)
	}

	return episodes, nil
}

// CountEpisodes returns the total number of episodes
func (g *DB) CountEpisodes() (int, error) {
	var count int
	err := g.db.QueryRow(`SELECT COUNT(*) FROM episodes`).Scan(&count)
	return count, err
}

// GetEpisode retrieves an episode by ID
func (g *DB) GetEpisode(id string) (*Episode, error) {
	row := g.db.QueryRow(`
		SELECT id, content, source, author, author_id, channel,
			timestamp_event, timestamp_ingested, dialogue_act, entropy_score,
			embedding, reply_to, created_at
		FROM episodes WHERE id = ?
	`, id)

	return scanEpisode(row)
}

// GetEpisodes retrieves multiple episodes by ID
func (g *DB) GetEpisodes(ids []string) ([]*Episode, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	// Build query with placeholders
	query := `SELECT id, content, source, author, author_id, channel,
		timestamp_event, timestamp_ingested, dialogue_act, entropy_score,
		embedding, reply_to, created_at FROM episodes WHERE id IN (`
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		if i > 0 {
			query += ","
		}
		query += "?"
		args[i] = id
	}
	query += ")"

	rows, err := g.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query episodes: %w", err)
	}
	defer rows.Close()

	var episodes []*Episode
	for rows.Next() {
		ep, err := scanEpisodeRow(rows)
		if err != nil {
			continue
		}
		episodes = append(episodes, ep)
	}

	return episodes, nil
}

// GetRecentEpisodes retrieves episodes from the last duration, optionally filtered by channel
func (g *DB) GetRecentEpisodes(since time.Duration, channel string, limit int) ([]*Episode, error) {
	cutoff := time.Now().Add(-since)

	var rows *sql.Rows
	var err error

	if channel != "" {
		rows, err = g.db.Query(`
			SELECT id, content, source, author, author_id, channel,
				timestamp_event, timestamp_ingested, dialogue_act, entropy_score,
				embedding, reply_to, created_at
			FROM episodes
			WHERE timestamp_event > ? AND channel = ?
			ORDER BY timestamp_event DESC
			LIMIT ?
		`, cutoff, channel, limit)
	} else {
		rows, err = g.db.Query(`
			SELECT id, content, source, author, author_id, channel,
				timestamp_event, timestamp_ingested, dialogue_act, entropy_score,
				embedding, reply_to, created_at
			FROM episodes
			WHERE timestamp_event > ?
			ORDER BY timestamp_event DESC
			LIMIT ?
		`, cutoff, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query episodes: %w", err)
	}
	defer rows.Close()

	var episodes []*Episode
	for rows.Next() {
		ep, err := scanEpisodeRow(rows)
		if err != nil {
			continue
		}
		episodes = append(episodes, ep)
	}

	return episodes, nil
}

// GetEpisodeReplies returns all episodes that reply to the given episode
func (g *DB) GetEpisodeReplies(id string) ([]*Episode, error) {
	rows, err := g.db.Query(`
		SELECT e.id, e.content, e.source, e.author, e.author_id, e.channel,
			e.timestamp_event, e.timestamp_ingested, e.dialogue_act, e.entropy_score,
			e.embedding, e.reply_to, e.created_at
		FROM episodes e
		INNER JOIN episode_edges ee ON ee.from_id = e.id
		WHERE ee.to_id = ? AND ee.edge_type = ?
		ORDER BY e.timestamp_event ASC
	`, id, EdgeRepliesTo)

	if err != nil {
		return nil, fmt.Errorf("failed to query replies: %w", err)
	}
	defer rows.Close()

	var episodes []*Episode
	for rows.Next() {
		ep, err := scanEpisodeRow(rows)
		if err != nil {
			continue
		}
		episodes = append(episodes, ep)
	}

	return episodes, nil
}

// AddEpisodeEdge adds an edge between two episodes
func (g *DB) AddEpisodeEdge(fromID, toID string, edgeType EdgeType, weight float64) error {
	_, err := g.db.Exec(`
		INSERT INTO episode_edges (from_id, to_id, edge_type, weight)
		VALUES (?, ?, ?, ?)
	`, fromID, toID, edgeType, weight)
	return err
}

// GetEpisodeNeighbors returns neighbors of an episode for spreading activation
func (g *DB) GetEpisodeNeighbors(id string) ([]Neighbor, error) {
	rows, err := g.db.Query(`
		SELECT to_id, weight, edge_type FROM episode_edges WHERE from_id = ?
		UNION ALL
		SELECT from_id, weight, edge_type FROM episode_edges WHERE to_id = ?
	`, id, id)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var neighbors []Neighbor
	for rows.Next() {
		var n Neighbor
		var edgeType string
		if err := rows.Scan(&n.ID, &n.Weight, &edgeType); err != nil {
			continue
		}
		n.Type = EdgeType(edgeType)
		neighbors = append(neighbors, n)
	}

	return neighbors, nil
}

// GetUnconsolidatedEpisodes returns episodes that haven't been linked to any trace yet.
// These are candidates for consolidation.
func (g *DB) GetUnconsolidatedEpisodes(limit int) ([]*Episode, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := g.db.Query(`
		SELECT e.id, e.content, e.source, e.author, e.author_id, e.channel,
			e.timestamp_event, e.timestamp_ingested, e.dialogue_act, e.entropy_score,
			e.embedding, e.reply_to, e.created_at
		FROM episodes e
		LEFT JOIN trace_sources ts ON ts.episode_id = e.id
		WHERE ts.trace_id IS NULL
		ORDER BY e.timestamp_event ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query unconsolidated episodes: %w", err)
	}
	defer rows.Close()

	var episodes []*Episode
	for rows.Next() {
		ep, err := scanEpisodeRow(rows)
		if err != nil {
			continue
		}
		episodes = append(episodes, ep)
	}

	return episodes, nil
}

// GetEpisodeEntities returns the entity IDs mentioned in an episode
func (g *DB) GetEpisodeEntities(episodeID string) ([]string, error) {
	rows, err := g.db.Query(`
		SELECT entity_id FROM episode_mentions WHERE episode_id = ?
	`, episodeID)
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

// scanEpisode scans a single row into an Episode
func scanEpisode(row *sql.Row) (*Episode, error) {
	var ep Episode
	var embeddingBytes []byte
	var author, authorID, channel, dialogueAct, replyTo sql.NullString
	var entropyScore sql.NullFloat64

	err := row.Scan(
		&ep.ID, &ep.Content, &ep.Source, &author, &authorID, &channel,
		&ep.TimestampEvent, &ep.TimestampIngested, &dialogueAct, &entropyScore,
		&embeddingBytes, &replyTo, &ep.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	ep.Author = author.String
	ep.AuthorID = authorID.String
	ep.Channel = channel.String
	ep.DialogueAct = dialogueAct.String
	ep.ReplyTo = replyTo.String
	ep.EntropyScore = entropyScore.Float64

	if len(embeddingBytes) > 0 {
		json.Unmarshal(embeddingBytes, &ep.Embedding)
	}

	return &ep, nil
}

// scanEpisodeRow scans from rows (multiple rows)
func scanEpisodeRow(rows *sql.Rows) (*Episode, error) {
	var ep Episode
	var embeddingBytes []byte
	var author, authorID, channel, dialogueAct, replyTo sql.NullString
	var entropyScore sql.NullFloat64

	err := rows.Scan(
		&ep.ID, &ep.Content, &ep.Source, &author, &authorID, &channel,
		&ep.TimestampEvent, &ep.TimestampIngested, &dialogueAct, &entropyScore,
		&embeddingBytes, &replyTo, &ep.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	ep.Author = author.String
	ep.AuthorID = authorID.String
	ep.Channel = channel.String
	ep.DialogueAct = dialogueAct.String
	ep.ReplyTo = replyTo.String
	ep.EntropyScore = entropyScore.Float64

	if len(embeddingBytes) > 0 {
		json.Unmarshal(embeddingBytes, &ep.Embedding)
	}

	return &ep, nil
}
