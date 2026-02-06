package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQLite database connection for the memory graph
type DB struct {
	db   *sql.DB
	path string
}

// Open opens or creates the memory graph database
func Open(statePath string) (*DB, error) {
	dbPath := filepath.Join(statePath, "memory.db")

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	g := &DB{db: db, path: dbPath}

	// Run migrations
	if err := g.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}

	return g, nil
}

// Close closes the database connection
func (g *DB) Close() error {
	return g.db.Close()
}

// TestSetTraceTimestamp updates the last_accessed timestamp for a trace (for testing only)
func (g *DB) TestSetTraceTimestamp(traceID string, lastAccessed time.Time) error {
	_, err := g.db.Exec(`UPDATE traces SET last_accessed = ? WHERE id = ?`, lastAccessed, traceID)
	return err
}

// SetTraceType sets the trace type for a given trace (for testing and classification)
func (g *DB) SetTraceType(traceID string, traceType TraceType) error {
	_, err := g.db.Exec(`UPDATE traces SET trace_type = ? WHERE id = ?`, string(traceType), traceID)
	return err
}

// SetTraceActivation sets the activation level for a trace (for testing only)
func (g *DB) SetTraceActivation(traceID string, activation float64) error {
	_, err := g.db.Exec(`UPDATE traces SET activation = ? WHERE id = ?`, activation, traceID)
	return err
}

// migrate runs database migrations
func (g *DB) migrate() error {
	schema := `
	-- Schema version tracking
	CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- TIER 1: EPISODES (Non-lossy raw messages)
	CREATE TABLE IF NOT EXISTS episodes (
		id TEXT PRIMARY KEY,
		content TEXT NOT NULL,
		source TEXT NOT NULL,
		author TEXT,
		author_id TEXT,
		channel TEXT,
		timestamp_event DATETIME NOT NULL,
		timestamp_ingested DATETIME NOT NULL,
		dialogue_act TEXT,
		entropy_score REAL,
		embedding BLOB,
		reply_to TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_episodes_timestamp ON episodes(timestamp_event);
	CREATE INDEX IF NOT EXISTS idx_episodes_channel ON episodes(channel);
	CREATE INDEX IF NOT EXISTS idx_episodes_author ON episodes(author_id);
	CREATE INDEX IF NOT EXISTS idx_episodes_reply_to ON episodes(reply_to);

	-- Episode edges (REPLIES_TO, FOLLOWS)
	CREATE TABLE IF NOT EXISTS episode_edges (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		from_id TEXT NOT NULL,
		to_id TEXT NOT NULL,
		edge_type TEXT NOT NULL,
		weight REAL DEFAULT 1.0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (from_id) REFERENCES episodes(id) ON DELETE CASCADE,
		FOREIGN KEY (to_id) REFERENCES episodes(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_episode_edges_from ON episode_edges(from_id);
	CREATE INDEX IF NOT EXISTS idx_episode_edges_to ON episode_edges(to_id);
	CREATE INDEX IF NOT EXISTS idx_episode_edges_type ON episode_edges(edge_type);

	-- TIER 2: ENTITIES (Extracted named entities)
	CREATE TABLE IF NOT EXISTS entities (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		type TEXT NOT NULL,
		salience REAL DEFAULT 0.0,
		embedding BLOB,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_entities_name ON entities(name);
	CREATE INDEX IF NOT EXISTS idx_entities_type ON entities(type);
	CREATE INDEX IF NOT EXISTS idx_entities_salience ON entities(salience);

	-- Entity aliases (multiple names for same entity)
	CREATE TABLE IF NOT EXISTS entity_aliases (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		entity_id TEXT NOT NULL,
		alias TEXT NOT NULL,
		FOREIGN KEY (entity_id) REFERENCES entities(id) ON DELETE CASCADE,
		UNIQUE(entity_id, alias)
	);

	CREATE INDEX IF NOT EXISTS idx_entity_aliases_alias ON entity_aliases(alias);

	-- Episode mentions (episode -> entity)
	CREATE TABLE IF NOT EXISTS episode_mentions (
		episode_id TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		PRIMARY KEY (episode_id, entity_id),
		FOREIGN KEY (episode_id) REFERENCES episodes(id) ON DELETE CASCADE,
		FOREIGN KEY (entity_id) REFERENCES entities(id) ON DELETE CASCADE
	);

	-- Entity relations (entity <-> entity) with temporal validity
	CREATE TABLE IF NOT EXISTS entity_relations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		from_id TEXT NOT NULL,
		to_id TEXT NOT NULL,
		relation_type TEXT NOT NULL,
		weight REAL DEFAULT 1.0,
		valid_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		invalid_at DATETIME,
		invalidated_by INTEGER,
		source_episode_id TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (from_id) REFERENCES entities(id) ON DELETE CASCADE,
		FOREIGN KEY (to_id) REFERENCES entities(id) ON DELETE CASCADE,
		FOREIGN KEY (invalidated_by) REFERENCES entity_relations(id),
		FOREIGN KEY (source_episode_id) REFERENCES episodes(id)
	);

	CREATE INDEX IF NOT EXISTS idx_entity_relations_from ON entity_relations(from_id);
	CREATE INDEX IF NOT EXISTS idx_entity_relations_to ON entity_relations(to_id);
	CREATE INDEX IF NOT EXISTS idx_entity_relations_valid ON entity_relations(invalid_at);

	-- TIER 3: TRACES (Consolidated memories)
	CREATE TABLE IF NOT EXISTS traces (
		id TEXT PRIMARY KEY,
		summary TEXT NOT NULL,
		topic TEXT,
		activation REAL DEFAULT 0.5,
		strength INTEGER DEFAULT 1,
		is_core BOOLEAN DEFAULT FALSE,
		embedding BLOB,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_accessed DATETIME DEFAULT CURRENT_TIMESTAMP,
		labile_until DATETIME
	);

	CREATE INDEX IF NOT EXISTS idx_traces_activation ON traces(activation);
	CREATE INDEX IF NOT EXISTS idx_traces_is_core ON traces(is_core);
	CREATE INDEX IF NOT EXISTS idx_traces_last_accessed ON traces(last_accessed);

	-- Trace sources (trace -> episode)
	CREATE TABLE IF NOT EXISTS trace_sources (
		trace_id TEXT NOT NULL,
		episode_id TEXT NOT NULL,
		PRIMARY KEY (trace_id, episode_id),
		FOREIGN KEY (trace_id) REFERENCES traces(id) ON DELETE CASCADE,
		FOREIGN KEY (episode_id) REFERENCES episodes(id) ON DELETE CASCADE
	);

	-- Trace entities (trace -> entity)
	CREATE TABLE IF NOT EXISTS trace_entities (
		trace_id TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		PRIMARY KEY (trace_id, entity_id),
		FOREIGN KEY (trace_id) REFERENCES traces(id) ON DELETE CASCADE,
		FOREIGN KEY (entity_id) REFERENCES entities(id) ON DELETE CASCADE
	);

	-- Trace relations (trace <-> trace)
	CREATE TABLE IF NOT EXISTS trace_relations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		from_id TEXT NOT NULL,
		to_id TEXT NOT NULL,
		relation_type TEXT NOT NULL,
		weight REAL DEFAULT 1.0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (from_id) REFERENCES traces(id) ON DELETE CASCADE,
		FOREIGN KEY (to_id) REFERENCES traces(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_trace_relations_from ON trace_relations(from_id);
	CREATE INDEX IF NOT EXISTS idx_trace_relations_to ON trace_relations(to_id);
	CREATE INDEX IF NOT EXISTS idx_trace_relations_type ON trace_relations(relation_type);

	-- Record schema version
	INSERT OR IGNORE INTO schema_version (version) VALUES (1);
	`

	_, err := g.db.Exec(schema)
	if err != nil {
		return err
	}

	// Run incremental migrations
	return g.runMigrations()
}

// runMigrations applies incremental schema changes
func (g *DB) runMigrations() error {
	// Get current version
	var version int
	err := g.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&version)
	if err != nil {
		version = 1 // Assume v1 if can't read
	}

	// Migration v2: Add temporal columns to entity_relations
	if version < 2 {
		migrations := []string{
			"ALTER TABLE entity_relations ADD COLUMN valid_at DATETIME DEFAULT CURRENT_TIMESTAMP",
			"ALTER TABLE entity_relations ADD COLUMN invalid_at DATETIME",
			"ALTER TABLE entity_relations ADD COLUMN invalidated_by INTEGER",
			"ALTER TABLE entity_relations ADD COLUMN source_episode_id TEXT",
			"CREATE INDEX IF NOT EXISTS idx_entity_relations_valid ON entity_relations(invalid_at)",
		}
		for _, sql := range migrations {
			// Ignore errors for columns that already exist
			g.db.Exec(sql)
		}
		g.db.Exec("INSERT INTO schema_version (version) VALUES (2)")
	}

	// Migration v3: Add index on trace_entities(entity_id) for entity-bridged activation
	if version < 3 {
		g.db.Exec("CREATE INDEX IF NOT EXISTS idx_trace_entities_entity ON trace_entities(entity_id)")
		g.db.Exec("INSERT INTO schema_version (version) VALUES (3)")
	}

	// Migration v4: Add trace_type column for operational vs knowledge classification
	if version < 4 {
		g.db.Exec("ALTER TABLE traces ADD COLUMN trace_type TEXT DEFAULT 'knowledge'")
		g.db.Exec("CREATE INDEX IF NOT EXISTS idx_traces_trace_type ON traces(trace_type)")
		// Backfill: tag existing traces that look operational
		g.db.Exec(`UPDATE traces SET trace_type = 'operational' WHERE
			(LOWER(summary) LIKE '%upcoming meeting%' OR
			 LOWER(summary) LIKE '%sprint planning%starts%' OR
			 LOWER(summary) LIKE '%heads up%meeting%' OR
			 LOWER(summary) LIKE '%state sync%' OR
			 LOWER(summary) LIKE '%synced state%' OR
			 LOWER(summary) LIKE '%no actionable work%' OR
			 LOWER(summary) LIKE '%idle wake%' OR
			 LOWER(summary) LIKE '%rebuilt binaries%')
			AND is_core = FALSE`)
		g.db.Exec("INSERT INTO schema_version (version) VALUES (4)")
	}

	// Migration v5: Expanded operational classification for meeting reminders and dev work notes
	if version < 5 {
		// Meeting reminders: "starts soon", "meeting starts", "meet.google.com"
		g.db.Exec(`UPDATE traces SET trace_type = 'operational' WHERE
			trace_type = 'knowledge' AND
			is_core = FALSE AND
			(LOWER(summary) LIKE '%starts soon%' OR
			 LOWER(summary) LIKE '%meeting starts%' OR
			 LOWER(summary) LIKE '%meet.google.com%' OR
			 LOWER(summary) LIKE '%starts in%' AND LOWER(summary) LIKE '%minute%')
			AND LOWER(summary) NOT LIKE '%discussed%'
			AND LOWER(summary) NOT LIKE '%decided%'`)

		// Dev work notes: past-tense implementation verbs without knowledge indicators
		// This is a simplified version - catches obvious cases
		g.db.Exec(`UPDATE traces SET trace_type = 'operational' WHERE
			trace_type = 'knowledge' AND
			is_core = FALSE AND
			(LOWER(summary) LIKE '%i updated %' OR
			 LOWER(summary) LIKE '%i implemented %' OR
			 LOWER(summary) LIKE '%i made%commit%' OR
			 LOWER(summary) LIKE '%i prepared%change%' OR
			 LOWER(summary) LIKE '%i proposed%' OR
			 LOWER(summary) LIKE 'explored %' OR
			 LOWER(summary) LIKE 'researched %')
			AND LOWER(summary) NOT LIKE '%because%'
			AND LOWER(summary) NOT LIKE '%decided%'
			AND LOWER(summary) NOT LIKE '%root cause%'
			AND LOWER(summary) NOT LIKE '%finding%'
			AND LOWER(summary) NOT LIKE '%learned%'
			AND LOWER(summary) NOT LIKE '%conclusion%'`)

		g.db.Exec("INSERT INTO schema_version (version) VALUES (5)")
	}

	// Migration v6: Populate trace_relations with similarity-based edges
	if version < 6 {
		if err := g.populateTraceRelations(0.85); err != nil {
			// Log but don't fail - migration is a best-effort optimization
			fmt.Printf("[migration v6] warning: failed to populate trace_relations: %v\n", err)
		}
		g.db.Exec("INSERT INTO schema_version (version) VALUES (6)")
	}

	return nil
}

// Stats returns database statistics
func (g *DB) Stats() (map[string]int, error) {
	stats := make(map[string]int)

	tables := []string{"episodes", "entities", "traces", "episode_edges", "entity_relations", "trace_relations"}
	for _, table := range tables {
		var count int
		err := g.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count)
		if err != nil {
			return nil, err
		}
		stats[table] = count
	}

	return stats, nil
}

// Clear removes all data (for testing/reset)
func (g *DB) Clear() error {
	tables := []string{
		"trace_relations", "trace_entities", "trace_sources", "traces",
		"entity_relations", "episode_mentions", "entity_aliases", "entities",
		"episode_edges", "episodes",
	}

	for _, table := range tables {
		if _, err := g.db.Exec(fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			return fmt.Errorf("failed to clear %s: %w", table, err)
		}
	}

	return nil
}

// populateTraceRelations computes pairwise similarity for all traces and creates
// SIMILAR_TO edges for pairs above the given threshold. Called during migration v6.
func (g *DB) populateTraceRelations(threshold float64) error {
	// Load all traces with embeddings
	rows, err := g.db.Query(`SELECT id, embedding FROM traces WHERE embedding IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("failed to query traces: %w", err)
	}
	defer rows.Close()

	type traceEmb struct {
		id        string
		embedding []float64
	}
	var traces []traceEmb

	for rows.Next() {
		var id string
		var embBytes []byte
		if err := rows.Scan(&id, &embBytes); err != nil {
			continue
		}
		var embedding []float64
		if err := json.Unmarshal(embBytes, &embedding); err != nil {
			continue
		}
		traces = append(traces, traceEmb{id: id, embedding: embedding})
	}

	if len(traces) < 2 {
		return nil // Nothing to link
	}

	// Compute pairwise similarities and insert edges above threshold
	var edgesAdded int
	for i := 0; i < len(traces); i++ {
		for j := i + 1; j < len(traces); j++ {
			sim := cosineSim(traces[i].embedding, traces[j].embedding)
			if sim >= threshold {
				// Add bidirectional edge (stored once, queried both ways)
				err := g.AddTraceRelation(traces[i].id, traces[j].id, EdgeSimilarTo, sim)
				if err == nil {
					edgesAdded++
				}
			}
		}
	}

	fmt.Printf("[migration v6] Populated trace_relations: %d SIMILAR_TO edges (threshold %.2f, %d traces)\n",
		edgesAdded, threshold, len(traces))
	return nil
}

// cosineSim computes cosine similarity between two embeddings
func cosineSim(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}
