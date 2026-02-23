package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	sqlite_vec.Auto() // registers the vec0 virtual table with go-sqlite3
}

// entityCacheEntry holds an entity and its pre-compiled word-boundary patterns,
// one per name/alias. Built once and reused across FindEntitiesByText calls.
type entityCacheEntry struct {
	entity   *Entity
	patterns []*regexp.Regexp // pre-compiled patterns, one per name/alias (nil = skip short names)
}

// DB wraps the SQLite database connection for the memory graph
type DB struct {
	db           *sql.DB
	path         string
	vecAvailable bool
	vecDim       int // embedding dimension used in trace_vec (0 = not yet determined)

	// Entity lookup cache: rebuilt lazily, invalidated on entity writes.
	entityCacheMu sync.RWMutex
	entityCache   []entityCacheEntry // nil means cache needs rebuild
}

// Open opens or creates the memory graph database
func Open(statePath string) (*DB, error) {
	dbPath := filepath.Join(statePath, "system", "memory.db")

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

	// Check if sqlite-vec extension is available
	var vecVersion string
	if err := db.QueryRow("SELECT vec_version()").Scan(&vecVersion); err != nil {
		log.Printf("[graph] sqlite-vec not available: %v — falling back to full scan", err)
	} else {
		log.Printf("[graph] sqlite-vec %s loaded", vecVersion)
		g.vecAvailable = true
		// Ensure vec table exists and set vecDim from existing data (handles restarts
		// where migration v18 already ran but vecDim needs to be restored in memory).
		if g.vecDim == 0 {
			if err := g.initVecTableFromTraces(); err != nil {
				log.Printf("[graph] vec init warning: %v", err)
			}
		}
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
		short_id TEXT DEFAULT '',
		summary TEXT,
		topic TEXT,
		activation REAL DEFAULT 0.5,
		strength INTEGER DEFAULT 1,
		embedding BLOB,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_accessed DATETIME DEFAULT CURRENT_TIMESTAMP,
		labile_until DATETIME,
		trace_type TEXT DEFAULT 'knowledge'
	);

	CREATE INDEX IF NOT EXISTS idx_traces_activation ON traces(activation);
	CREATE INDEX IF NOT EXISTS idx_traces_short_id ON traces(short_id);
	CREATE INDEX IF NOT EXISTS idx_traces_last_accessed ON traces(last_accessed);
	CREATE INDEX IF NOT EXISTS idx_traces_trace_type ON traces(trace_type);

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

	// Migration v7: Add episode_summaries table for pyramid summaries
	if version < 7 {
		migrations := []string{
			`CREATE TABLE IF NOT EXISTS episode_summaries (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				episode_id TEXT NOT NULL,
				compression_level INTEGER NOT NULL,
				summary TEXT NOT NULL,
				tokens INTEGER NOT NULL,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (episode_id) REFERENCES episodes(id) ON DELETE CASCADE,
				UNIQUE(episode_id, compression_level)
			)`,
			"CREATE INDEX IF NOT EXISTS idx_episode_summaries_episode ON episode_summaries(episode_id)",
			"CREATE INDEX IF NOT EXISTS idx_episode_summaries_level ON episode_summaries(compression_level)",
		}
		for _, sql := range migrations {
			if _, err := g.db.Exec(sql); err != nil {
				return fmt.Errorf("migration v7 failed: %w", err)
			}
		}
		g.db.Exec("INSERT INTO schema_version (version) VALUES (7)")
	}

	// Migration v8: Add token_count and short_id to episodes, remove level 0 summaries
	if version < 8 {
		migrations := []string{
			"ALTER TABLE episodes ADD COLUMN token_count INTEGER DEFAULT 0",
			"ALTER TABLE episodes ADD COLUMN short_id TEXT DEFAULT ''",
			"CREATE INDEX IF NOT EXISTS idx_episodes_short_id ON episodes(short_id)",
			"DELETE FROM episode_summaries WHERE compression_level = 0",
		}
		for _, sql := range migrations {
			// Ignore errors for columns that already exist
			g.db.Exec(sql)
		}
		g.db.Exec("INSERT INTO schema_version (version) VALUES (8)")
	}

	// Migration v9: Add trace_summaries table for pyramid summaries
	if version < 9 {
		migrations := []string{
			`CREATE TABLE IF NOT EXISTS trace_summaries (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				trace_id TEXT NOT NULL,
				compression_level INTEGER NOT NULL,
				summary TEXT NOT NULL,
				tokens INTEGER NOT NULL,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (trace_id) REFERENCES traces(id) ON DELETE CASCADE,
				UNIQUE(trace_id, compression_level)
			)`,
			"CREATE INDEX IF NOT EXISTS idx_trace_summaries_trace ON trace_summaries(trace_id)",
			"CREATE INDEX IF NOT EXISTS idx_trace_summaries_level ON trace_summaries(compression_level)",
		}
		for _, sql := range migrations {
			if _, err := g.db.Exec(sql); err != nil {
				return fmt.Errorf("migration v9 failed: %w", err)
			}
		}
		g.db.Exec("INSERT INTO schema_version (version) VALUES (9)")
	}

	// Migration v10: Make traces.summary nullable (deprecated - use trace_summaries instead)
	// SQLite doesn't support ALTER COLUMN, so we need to recreate the table
	if version < 10 {
		// Check if is_core column exists (it was added in v4, but fresh DBs may not have it)
		hasIsCore := false
		pragmaRows, _ := g.db.Query("PRAGMA table_info(traces)")
		if pragmaRows != nil {
			for pragmaRows.Next() {
				var cid int
				var name, colType string
				var notNull int
				var dflt interface{}
				var pk int
				if err := pragmaRows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err == nil && name == "is_core" {
					hasIsCore = true
				}
			}
			pragmaRows.Close()
		}

		insertSQL := `INSERT INTO traces_new SELECT id, summary, topic, activation, strength, is_core, embedding, created_at, last_accessed, labile_until, trace_type FROM traces`
		if !hasIsCore {
			insertSQL = `INSERT INTO traces_new SELECT id, summary, topic, activation, strength, FALSE, embedding, created_at, last_accessed, labile_until, trace_type FROM traces`
		}

		migrations := []string{
			`CREATE TABLE IF NOT EXISTS traces_new (
				id TEXT PRIMARY KEY,
				summary TEXT,
				topic TEXT,
				activation REAL DEFAULT 0.5,
				strength INTEGER DEFAULT 1,
				is_core BOOLEAN DEFAULT FALSE,
				embedding BLOB,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				last_accessed DATETIME DEFAULT CURRENT_TIMESTAMP,
				labile_until DATETIME,
				trace_type TEXT DEFAULT 'knowledge'
			)`,
			insertSQL,
			`DROP TABLE traces`,
			`ALTER TABLE traces_new RENAME TO traces`,
			`CREATE INDEX IF NOT EXISTS idx_traces_activation ON traces(activation)`,
			`CREATE INDEX IF NOT EXISTS idx_traces_is_core ON traces(is_core)`,
			`CREATE INDEX IF NOT EXISTS idx_traces_last_accessed ON traces(last_accessed)`,
			`CREATE INDEX IF NOT EXISTS idx_traces_trace_type ON traces(trace_type)`,
		}
		for _, sql := range migrations {
			if _, err := g.db.Exec(sql); err != nil {
				return fmt.Errorf("migration v10 failed: %w", err)
			}
		}
		g.db.Exec("INSERT INTO schema_version (version) VALUES (10)")
	}

	// Migration v11: Backfill short_id for episodes missing it
	if version < 11 {
		// Get all episodes without short_id
		rows, err := g.db.Query("SELECT id FROM episodes WHERE short_id IS NULL OR short_id = ''")
		if err == nil {
			var ids []string
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()

			// Generate and update short_id for each episode
			for _, id := range ids {
				shortID := generateShortID(id)
				g.db.Exec("UPDATE episodes SET short_id = ? WHERE id = ?", shortID, id)
			}
			if len(ids) > 0 {
				log.Printf("[graph] Backfilled short_id for %d episodes", len(ids))
			}
		}
		g.db.Exec("INSERT INTO schema_version (version) VALUES (11)")
	}

	// Migration v12: Episode linking (episode-episode + episode-trace) for reconsolidation
	if version < 12 {
		log.Println("[graph] Migrating to schema v12: episode linking")

		// 1. Enhance episode_edges table with relationship descriptors
		migrations := []string{
			"ALTER TABLE episode_edges ADD COLUMN relationship_desc TEXT",
			"ALTER TABLE episode_edges ADD COLUMN confidence REAL DEFAULT 1.0",
		}
		for _, sql := range migrations {
			// Ignore errors for columns that already exist
			g.db.Exec(sql)
		}

		// 2. Create episode_trace_edges table
		_, err := g.db.Exec(`
			CREATE TABLE IF NOT EXISTS episode_trace_edges (
				episode_id TEXT NOT NULL,
				trace_id TEXT NOT NULL,
				relationship_desc TEXT NOT NULL,
				confidence REAL DEFAULT 1.0,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (episode_id) REFERENCES episodes(id) ON DELETE CASCADE,
				FOREIGN KEY (trace_id) REFERENCES traces(id) ON DELETE CASCADE,
				PRIMARY KEY (episode_id, trace_id)
			)
		`)
		if err != nil {
			return fmt.Errorf("migration v12 failed to create episode_trace_edges: %w", err)
		}

		// 3. Create indexes
		g.db.Exec("CREATE INDEX IF NOT EXISTS idx_episode_trace_trace ON episode_trace_edges(trace_id)")
		g.db.Exec("CREATE INDEX IF NOT EXISTS idx_episode_trace_episode ON episode_trace_edges(episode_id)")

			g.db.Exec("INSERT INTO schema_version (version) VALUES (12)")
		log.Println("[graph] Migration to v12 completed successfully")
	}

	// Migration v13: Add short_id to traces table
	if version < 13 {
		log.Println("[graph] Migrating to schema v13: add trace short_id")

		migrations := []string{
			"ALTER TABLE traces ADD COLUMN short_id TEXT DEFAULT ''",
			"CREATE INDEX IF NOT EXISTS idx_traces_short_id ON traces(short_id)",
		}
		for _, sql := range migrations {
			// Ignore errors for columns that already exist
			g.db.Exec(sql)
		}

		// Backfill short_id for existing traces
		rows, err := g.db.Query("SELECT id FROM traces WHERE short_id = '' OR short_id IS NULL")
		if err == nil {
			var ids []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()

			// Generate and update short_id for each trace
			for _, id := range ids {
				shortID := generateShortID(id)
				g.db.Exec("UPDATE traces SET short_id = ? WHERE id = ?", shortID, id)
			}
			if len(ids) > 0 {
				log.Printf("[graph] Backfilled short_id for %d traces", len(ids))
			}
		}

		g.db.Exec("INSERT INTO schema_version (version) VALUES (13)")
		log.Println("[graph] Migration to v13 completed successfully")
	}

	// Migration v14: Remove is_core column (core identity now loaded from state/system/core.md)
	if version < 14 {
		log.Println("[graph] Migrating to schema v14: remove is_core column")

		// SQLite doesn't support DROP COLUMN, so we need to recreate the table
		migrations := []string{
			`CREATE TABLE IF NOT EXISTS traces_new (
				id TEXT PRIMARY KEY,
				short_id TEXT DEFAULT '',
				summary TEXT,
				topic TEXT,
				activation REAL DEFAULT 0.5,
				strength INTEGER DEFAULT 1,
				embedding BLOB,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				last_accessed DATETIME DEFAULT CURRENT_TIMESTAMP,
				labile_until DATETIME,
				trace_type TEXT DEFAULT 'knowledge'
			)`,
			`INSERT INTO traces_new SELECT id, short_id, summary, topic, activation, strength, embedding, created_at, last_accessed, labile_until, trace_type FROM traces`,
			`DROP TABLE traces`,
			`ALTER TABLE traces_new RENAME TO traces`,
			`CREATE INDEX IF NOT EXISTS idx_traces_activation ON traces(activation)`,
			`CREATE INDEX IF NOT EXISTS idx_traces_short_id ON traces(short_id)`,
			`CREATE INDEX IF NOT EXISTS idx_traces_last_accessed ON traces(last_accessed)`,
			`CREATE INDEX IF NOT EXISTS idx_traces_trace_type ON traces(trace_type)`,
		}
		for _, sql := range migrations {
			if _, err := g.db.Exec(sql); err != nil {
				return fmt.Errorf("migration v14 failed: %w", err)
			}
		}
		g.db.Exec("INSERT INTO schema_version (version) VALUES (14)")
		log.Println("[graph] Migration to v14 completed successfully")
	}

	// Migration v15: Add needs_reconsolidation flag for incremental clustering
	if version < 15 {
		_, err := g.db.Exec(`ALTER TABLE traces ADD COLUMN needs_reconsolidation BOOLEAN DEFAULT 0`)
		if err != nil {
			// Ignore errors for columns that already exist
			g.db.Exec("ALTER TABLE traces ADD COLUMN needs_reconsolidation BOOLEAN DEFAULT 0")
		}
		g.db.Exec("INSERT INTO schema_version (version) VALUES (15)")
		log.Println("[graph] Migration to v15 completed successfully")
	}

	// Migration v16: Add authorization tracking columns to episodes
	if version < 16 {
		g.db.Exec(`ALTER TABLE episodes ADD COLUMN authorization_checked INTEGER DEFAULT 0`)
		g.db.Exec(`ALTER TABLE episodes ADD COLUMN has_authorization INTEGER DEFAULT 0`)
		g.db.Exec("INSERT INTO schema_version (version) VALUES (16)")
		log.Println("[graph] Migration to v16 completed successfully")
	}

	// Migration v17: Add FTS5 virtual table for trace keyword search.
	// Indexes level-32 summaries from trace_summaries for fast BM25 MATCH queries,
	// replacing the Go-side full table scan in FindTracesWithKeywords.
	if version < 17 {
		log.Println("[graph] Migrating to schema v17: FTS5 index for trace keyword search")
		migrations := []string{
			// Create FTS5 table with content= pointing to trace_summaries
			`CREATE VIRTUAL TABLE IF NOT EXISTS trace_fts USING fts5(
				trace_id UNINDEXED,
				summary,
				content=trace_summaries,
				content_rowid=id
			)`,
			// Populate FTS5 from existing level-32 summaries
			`INSERT INTO trace_fts(rowid, trace_id, summary)
				SELECT id, trace_id, summary FROM trace_summaries WHERE compression_level = 32`,
			// Trigger: keep FTS5 in sync when a summary is inserted
			`CREATE TRIGGER IF NOT EXISTS trace_summaries_ai
				AFTER INSERT ON trace_summaries
				WHEN NEW.compression_level = 32
				BEGIN
					INSERT INTO trace_fts(rowid, trace_id, summary) VALUES (NEW.id, NEW.trace_id, NEW.summary);
				END`,
			// Trigger: keep FTS5 in sync when a summary is updated
			`CREATE TRIGGER IF NOT EXISTS trace_summaries_au
				AFTER UPDATE ON trace_summaries
				WHEN NEW.compression_level = 32
				BEGIN
					INSERT INTO trace_fts(trace_fts, rowid, trace_id, summary) VALUES ('delete', OLD.id, OLD.trace_id, OLD.summary);
					INSERT INTO trace_fts(rowid, trace_id, summary) VALUES (NEW.id, NEW.trace_id, NEW.summary);
				END`,
			// Trigger: keep FTS5 in sync when a summary is deleted
			`CREATE TRIGGER IF NOT EXISTS trace_summaries_ad
				AFTER DELETE ON trace_summaries
				WHEN OLD.compression_level = 32
				BEGIN
					INSERT INTO trace_fts(trace_fts, rowid, trace_id, summary) VALUES ('delete', OLD.id, OLD.trace_id, OLD.summary);
				END`,
		}
		for _, sql := range migrations {
			if _, err := g.db.Exec(sql); err != nil {
				// Non-fatal: FTS5 may not be compiled in; fall back gracefully
				log.Printf("[graph] Migration v17 warning (FTS5 may be unavailable): %v", err)
				break
			}
		}
		g.db.Exec("INSERT INTO schema_version (version) VALUES (17)")
		log.Println("[graph] Migration to v17 completed successfully")
	}

	// Migration v18: Add sqlite-vec ANN index for trace embedding search.
	// Creates a vec0 virtual table for fast cosine KNN queries, replacing the O(n)
	// Go-side scan in FindSimilarTraces. Backfills from the traces table on first run.
	// Skipped gracefully if sqlite-vec extension is not compiled in or no embeddings exist.
	// The vec table dimension is determined dynamically from existing trace embeddings.
	if version < 18 {
		log.Println("[graph] Migrating to schema v18: sqlite-vec trace_vec index")
		// Detect embedding dimension from existing traces (if any)
		if err := g.initVecTableFromTraces(); err != nil {
			log.Printf("[graph] Migration v18 warning: %v — vec index deferred to first AddTrace", err)
		}
		g.db.Exec("INSERT INTO schema_version (version) VALUES (18)")
		log.Println("[graph] Migration to v18 completed successfully")
	}

	// Migration v19: Add index on trace_sources(episode_id) for efficient
	// unconsolidated-episode lookups. The composite PK (trace_id, episode_id)
	// can't be used for joins/lookups on episode_id alone, causing full-table scans
	// (~400ms per wake). This index brings the query to <10ms.
	if version < 19 {
		log.Println("[graph] Migrating to schema v19: idx_trace_sources_episode")
		g.db.Exec("CREATE INDEX IF NOT EXISTS idx_trace_sources_episode ON trace_sources(episode_id)")
		g.db.Exec("INSERT INTO schema_version (version) VALUES (19)")
		log.Println("[graph] Migration to v19 completed successfully")
	}

	// Migration v20: Repair FTS5 table if it was skipped during v17 due to missing build tag.
	// v17 marked itself complete even when FTS5 creation failed, leaving trace_fts absent.
	// This migration re-attempts FTS5 setup idempotently; it's a no-op if trace_fts exists.
	if version < 20 {
		log.Println("[graph] Migrating to schema v20: FTS5 repair (idempotent re-attempt)")
		migrations := []string{
			`CREATE VIRTUAL TABLE IF NOT EXISTS trace_fts USING fts5(
				trace_id UNINDEXED,
				summary,
				content=trace_summaries,
				content_rowid=id
			)`,
			`INSERT OR IGNORE INTO trace_fts(rowid, trace_id, summary)
				SELECT id, trace_id, summary FROM trace_summaries WHERE compression_level = 32`,
			`CREATE TRIGGER IF NOT EXISTS trace_summaries_ai
				AFTER INSERT ON trace_summaries
				WHEN NEW.compression_level = 32
				BEGIN
					INSERT INTO trace_fts(rowid, trace_id, summary) VALUES (NEW.id, NEW.trace_id, NEW.summary);
				END`,
			`CREATE TRIGGER IF NOT EXISTS trace_summaries_au
				AFTER UPDATE ON trace_summaries
				WHEN NEW.compression_level = 32
				BEGIN
					INSERT INTO trace_fts(trace_fts, rowid, trace_id, summary) VALUES ('delete', OLD.id, OLD.trace_id, OLD.summary);
					INSERT INTO trace_fts(rowid, trace_id, summary) VALUES (NEW.id, NEW.trace_id, NEW.summary);
				END`,
			`CREATE TRIGGER IF NOT EXISTS trace_summaries_ad
				AFTER DELETE ON trace_summaries
				WHEN OLD.compression_level = 32
				BEGIN
					INSERT INTO trace_fts(trace_fts, rowid, trace_id, summary) VALUES ('delete', OLD.id, OLD.trace_id, OLD.summary);
				END`,
		}
		ftsOK := true
		for _, sql := range migrations {
			if _, err := g.db.Exec(sql); err != nil {
				log.Printf("[graph] Migration v20 warning (FTS5 may be unavailable): %v", err)
				ftsOK = false
				break
			}
		}
		g.db.Exec("INSERT INTO schema_version (version) VALUES (20)")
		if ftsOK {
			log.Println("[graph] Migration to v20 completed: FTS5 table created/repaired")
		} else {
			log.Println("[graph] Migration to v20 skipped: FTS5 not available (rebuild with -tags fts5)")
		}
	}

	return nil
}

// initVecTableFromTraces reads the embedding dimension from existing traces, creates the
// trace_vec virtual table with that dimension (if it doesn't already exist), and backfills
// all existing trace embeddings. No-ops if no traces with embeddings exist yet.
func (g *DB) initVecTableFromTraces() error {
	// Read one embedding to determine dimension
	var embBytes []byte
	err := g.db.QueryRow(`SELECT embedding FROM traces WHERE embedding IS NOT NULL AND LENGTH(embedding) > 4 LIMIT 1`).Scan(&embBytes)
	if err != nil {
		return nil // no traces with embeddings yet; defer to first AddTrace
	}
	var emb64 []float64
	if err := json.Unmarshal(embBytes, &emb64); err != nil || len(emb64) == 0 {
		return nil
	}
	return g.ensureVecTable(len(emb64))
}

// ensureVecTable creates the trace_vec virtual table for the given embedding dimension
// (if not yet created) and backfills all existing traces. Idempotent for the same dim.
//
// Schema uses integer rowid (from the traces table) + auxiliary +trace_id column,
// avoiding vec0's TEXT PRIMARY KEY partitioning behaviour which breaks KNN queries.
func (g *DB) ensureVecTable(dim int) error {
	if g.vecDim == dim {
		return nil // already set up for this dimension
	}
	if g.vecDim != 0 && g.vecDim != dim {
		// Dimension mismatch — can't use vec for this embedding
		return fmt.Errorf("embedding dim %d doesn't match vec table dim %d", dim, g.vecDim)
	}

	// Create the vec table with the correct dimension.
	// Use integer rowid (mapped from traces.rowid) and +trace_id as auxiliary text.
	_, err := g.db.Exec(fmt.Sprintf(`
		CREATE VIRTUAL TABLE IF NOT EXISTS trace_vec USING vec0(
			embedding float[%d],
			+trace_id TEXT
		)
	`, dim))
	if err != nil {
		return fmt.Errorf("failed to create trace_vec(float[%d]): %w", dim, err)
	}
	g.vecDim = dim

	// Backfill all existing traces into the new index.
	// Use the traces.rowid as the vec0 rowid for stable integer keying.
	rows, err := g.db.Query(`SELECT rowid, id, embedding FROM traces WHERE embedding IS NOT NULL`)
	if err != nil {
		return nil // backfill failure is non-fatal
	}
	defer rows.Close()

	tx, err := g.db.Begin()
	if err != nil {
		return nil
	}

	var count int
	for rows.Next() {
		var rowid int64
		var id string
		var emb []byte
		if err := rows.Scan(&rowid, &id, &emb); err != nil {
			continue
		}
		var emb64 []float64
		if err := json.Unmarshal(emb, &emb64); err != nil || len(emb64) != dim {
			continue
		}
		emb32 := normalizeFloat32(float64ToFloat32(emb64)) // normalize for cosine-compatible L2
		serialized, serErr := sqlite_vec.SerializeFloat32(emb32)
		if serErr != nil {
			continue
		}
		// vec0 does not reliably support INSERT OR REPLACE; use DELETE + INSERT.
		tx.Exec(`DELETE FROM trace_vec WHERE rowid = ?`, rowid)
		if _, err := tx.Exec(`INSERT INTO trace_vec(rowid, embedding, trace_id) VALUES (?, ?, ?)`, rowid, serialized, id); err != nil {
			log.Printf("[graph] vec backfill failed for %s: %v", id, err)
			continue
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return nil
	}
	if count > 0 {
		log.Printf("[graph] vec backfill: indexed %d traces (dim=%d)", count, dim)
	}
	return nil
}

// float64ToFloat32 converts a float64 slice to float32
func float64ToFloat32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

// normalizeFloat32 returns a unit-length copy of the vector.
// Normalizing before storing in vec0 makes L2 distance equivalent to cosine distance:
//   cosine_dist = L2_dist² / 2   (for unit vectors)
//   L2_threshold = sqrt(2 * cosine_dist_threshold)
func normalizeFloat32(v []float32) []float32 {
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		return v
	}
	norm = math.Sqrt(norm)
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / norm)
	}
	return out
}

// cosineDistToL2 converts a cosine distance threshold to an L2 distance threshold
// for unit-normalized vectors: L2_threshold = sqrt(2 * cosine_dist_threshold)
func cosineDistToL2(cosineDist float64) float64 {
	return math.Sqrt(2.0 * cosineDist)
}

// l2ToCosineSim converts an L2 distance (on normalized vectors) to cosine similarity:
// cosine_sim = 1 - L2²/2
func l2ToCosineSim(l2dist float64) float64 {
	return 1.0 - (l2dist*l2dist)/2.0
}

// Stats returns database statistics
func (g *DB) Stats() (map[string]int, error) {
	stats := make(map[string]int)

	tables := []string{"episodes", "episode_summaries", "entities", "traces", "trace_sources", "episode_edges", "entity_relations", "trace_relations"}
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
		"episode_edges", "episode_summaries", "episodes",
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
