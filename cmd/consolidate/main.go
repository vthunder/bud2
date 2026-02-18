package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/vthunder/bud2/internal/consolidate"
	"github.com/vthunder/bud2/internal/embedding"
	"github.com/vthunder/bud2/internal/graph"
)

func main() {
	stateDir := flag.String("state", "state", "Path to state directory")
	wipe := flag.Bool("wipe", false, "Wipe all existing traces and trace_sources before consolidating")
	wipeEdges := flag.Bool("wipe-edges", false, "Also wipe episode-episode edges (only if --wipe is set)")
	pruneEdges := flag.Bool("prune-edges", false, "Delete edges involving unconsolidated episodes before running")
	deduplicateEpisodes := flag.Bool("deduplicate-episodes", false, "Remove duplicate episodes (same content) before consolidating")
	incremental := flag.Bool("incremental", false, "Only infer edges for windows containing unconsolidated episodes")
	dryRun := flag.Bool("dry-run", false, "Print stats without consolidating")
	backfillEpisodeTraceEdges := flag.Bool("backfill-episode-trace-edges", false, "Backfill episode_trace_edges for all existing consolidated episodes")
	claudeModel := flag.String("model", "claude-sonnet-4-5", "Claude model for inference (requires Claude Code)")
	verbose := flag.Bool("verbose", false, "Verbose output")
	flag.Parse()

	// Open database (graph wrapper)
	dbPath := filepath.Join(*stateDir, "system", "memory.db")
	graphDB, err := graph.Open(*stateDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer graphDB.Close()

	// Open raw SQL connection for wipe operations
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("Failed to open raw database connection: %v", err)
	}
	defer db.Close()

	log.Printf("Database: %s", dbPath)

	// Get stats before
	stats, err := graphDB.Stats()
	if err != nil {
		log.Fatalf("Failed to get stats: %v", err)
	}

	totalEpisodes := stats["episodes"]
	totalTraces := stats["traces"]
	totalTraceSources := stats["trace_sources"]

	log.Printf("Current state:")
	log.Printf("  Episodes: %d", totalEpisodes)
	log.Printf("  Traces: %d", totalTraces)
	log.Printf("  Trace-episode links: %d", totalTraceSources)

	// Check for unconsolidated episodes
	unconsolidated, err := graphDB.GetUnconsolidatedEpisodes(10000)
	if err != nil {
		log.Fatalf("Failed to get unconsolidated episodes: %v", err)
	}

	log.Printf("  Unconsolidated episodes: %d", len(unconsolidated))

	if *dryRun {
		log.Println("Dry run - exiting")
		return
	}

	// Wipe existing traces if requested
	if *wipe {
		if *wipeEdges {
			log.Println("\n⚠️  WIPING ALL TRACES, TRACE-EPISODE LINKS, AND EPISODE-EPISODE EDGES...")
		} else {
			log.Println("\n⚠️  WIPING ALL TRACES AND TRACE-EPISODE LINKS...")
			log.Println("    Episode-episode edges will be preserved.")
		}
		log.Println("    This will recreate all traces from scratch.")
		log.Println("    Press Ctrl+C within 3 seconds to cancel...")
		time.Sleep(3 * time.Second)

		if err := wipeTraces(db, *wipeEdges); err != nil {
			log.Fatalf("Failed to wipe traces: %v", err)
		}

		log.Println("✅ Traces wiped successfully")

		// Recount unconsolidated (should be all episodes now)
		unconsolidated, err = graphDB.GetUnconsolidatedEpisodes(10000)
		if err != nil {
			log.Fatalf("Failed to get unconsolidated episodes: %v", err)
		}
		log.Printf("  Unconsolidated episodes after wipe: %d", len(unconsolidated))
	}

	// Initialize LLM client for consolidation
	ollamaClient := embedding.NewClient("", "")
	ollamaClient.SetGenerationModel("qwen2.5:7b")

	// Initialize Claude inference (required)
	log.Printf("Initializing Claude inference (model: %s)", *claudeModel)
	claudeInference := consolidate.NewClaudeInference(*claudeModel, ".", *verbose)

	// Create consolidator
	consolidator := consolidate.NewConsolidator(graphDB, ollamaClient, claudeInference)

	// Deduplicate episodes if requested
	if *deduplicateEpisodes {
		log.Println("\n🔍 Checking for duplicate episodes...")
		deleted, err := removeDuplicateEpisodes(db, graphDB)
		if err != nil {
			log.Fatalf("Failed to deduplicate episodes: %v", err)
		}
		log.Printf("✅ Removed %d duplicate episodes", deleted)
	}

	// Prune edges for unconsolidated episodes if requested
	if *pruneEdges {
		log.Println("\nPruning edges for unconsolidated episodes...")
		count, err := pruneUnconsolidatedEdges(db, graphDB)
		if err != nil {
			log.Fatalf("Failed to prune edges: %v", err)
		}
		log.Printf("✅ Pruned %d edges", count)
	}

	// Backfill episode_trace_edges for all existing consolidated episodes
	if *backfillEpisodeTraceEdges {
		log.Println("\nBackfilling episode→trace cross-reference edges for all consolidated episodes...")
		start := time.Now()
		linked, err := consolidator.BackfillEpisodeTraceEdges(500)
		if err != nil {
			log.Fatalf("Backfill failed: %v", err)
		}
		log.Printf("✅ Backfill complete in %v: created %d episode→trace edges", time.Since(start).Round(time.Second), linked)
		return
	}

	// Configure incremental mode
	if *incremental {
		consolidator.IncrementalMode = true
		log.Println("Running in incremental mode - only processing windows with new episodes")
	}

	// Run consolidation
	log.Println("\nStarting consolidation...")
	start := time.Now()

	created, err := consolidator.Run()
	if err != nil {
		log.Fatalf("Consolidation failed: %v", err)
	}

	duration := time.Since(start)

	// Get token stats from Claude inference
	inputTokens, outputTokens, cacheReadTokens, cacheCreateTokens, sessionCount := claudeInference.GetTokenStats()
	totalTokens := inputTokens + outputTokens

	log.Printf("\n✅ Session complete in %v", duration.Round(time.Second))
	log.Printf("   Created %d new traces", created)
	if sessionCount > 0 {
		log.Printf("   Claude sessions: %d", sessionCount)
		log.Printf("   Tokens used: %d (input=%d output=%d cache_read=%d cache_create=%d)",
			totalTokens, inputTokens, outputTokens, cacheReadTokens, cacheCreateTokens)
	}

	// Get final stats
	statsAfter, err := graphDB.Stats()
	if err != nil {
		log.Fatalf("Failed to get final stats: %v", err)
	}

	finalTraces := statsAfter["traces"]
	finalTraceSources := statsAfter["trace_sources"]

	log.Printf("\nFinal state:")
	log.Printf("  Traces: %d (added: %d)", finalTraces, finalTraces-totalTraces)
	log.Printf("  Trace-episode links: %d (added: %d)", finalTraceSources, finalTraceSources-totalTraceSources)

	// Count remaining unconsolidated
	unconsolidatedAfter, err := graphDB.GetUnconsolidatedEpisodes(10000)
	if err != nil {
		log.Printf("Warning: failed to count unconsolidated episodes: %v", err)
	} else {
		log.Printf("  Unconsolidated episodes: %d", len(unconsolidatedAfter))
	}

	// Remind user to backfill pyramid summaries
	if created > 0 {
		log.Printf("\n💡 Tip: Run 'compress-traces' to generate full pyramid summaries (C64→C32→C16→C4)")
		log.Printf("   Consolidate only generates C8 for speed - compress-traces backfills the rest")
	}
}

// wipeTraces deletes all traces and trace-episode links
// If wipeEdges is true, also deletes episode-episode edges
func wipeTraces(db *sql.DB, wipeEdges bool) error {
	// Delete all trace_sources entries
	if _, err := db.Exec("DELETE FROM trace_sources"); err != nil {
		return fmt.Errorf("failed to delete trace_sources: %w", err)
	}
	log.Println("  Deleted all trace-episode links")

	// Delete all trace-entity associations
	if _, err := db.Exec("DELETE FROM trace_entities"); err != nil {
		return fmt.Errorf("failed to delete trace entities: %w", err)
	}
	log.Println("  Deleted all trace-entity associations")

	// Delete all trace-entity relations
	if _, err := db.Exec("DELETE FROM trace_relations"); err != nil {
		return fmt.Errorf("failed to delete trace relations: %w", err)
	}
	log.Println("  Deleted all trace-entity relations")

	// Delete all episode-trace edges
	if _, err := db.Exec("DELETE FROM episode_trace_edges"); err != nil {
		return fmt.Errorf("failed to delete episode-trace edges: %w", err)
	}
	log.Println("  Deleted all episode-trace edges")

	// Delete all trace summaries
	if _, err := db.Exec("DELETE FROM trace_summaries"); err != nil {
		return fmt.Errorf("failed to delete trace summaries: %w", err)
	}
	log.Println("  Deleted all trace summaries")

	// Delete all traces
	if _, err := db.Exec("DELETE FROM traces"); err != nil {
		return fmt.Errorf("failed to delete traces: %w", err)
	}
	log.Println("  Deleted all traces")

	// Optionally delete episode-episode edges
	if wipeEdges {
		if _, err := db.Exec("DELETE FROM episode_edges"); err != nil {
			return fmt.Errorf("failed to delete episode edges: %w", err)
		}
		log.Println("  Deleted all episode-episode edges")
	}

	return nil
}

// pruneUnconsolidatedEdges deletes episode-episode edges involving unconsolidated episodes
func pruneUnconsolidatedEdges(db *sql.DB, graphDB *graph.DB) (int, error) {
	// Get all unconsolidated episode IDs
	unconsolidated, err := graphDB.GetUnconsolidatedEpisodes(10000)
	if err != nil {
		return 0, fmt.Errorf("failed to get unconsolidated episodes: %w", err)
	}

	if len(unconsolidated) == 0 {
		return 0, nil
	}

	// Build list of IDs to prune
	ids := make([]string, len(unconsolidated))
	for i, ep := range unconsolidated {
		ids[i] = ep.ID
	}

	log.Printf("  Deleting edges involving %d unconsolidated episodes", len(ids))

	// Build IN clause
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids)*2)
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
		args[len(ids)+i] = id
	}

	query := fmt.Sprintf(`
		DELETE FROM episode_edges
		WHERE from_id IN (%s) OR to_id IN (%s)
	`, strings.Join(placeholders, ","), strings.Join(placeholders, ","))

	result, err := db.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to delete edges: %w", err)
	}

	count, _ := result.RowsAffected()
	return int(count), nil
}

// removeDuplicateEpisodes removes episodes with duplicate content, keeping the earliest one
func removeDuplicateEpisodes(db *sql.DB, graphDB *graph.DB) (int, error) {
	log.Println("  Finding duplicate episodes by content...")

	// Find groups of episodes with duplicate content
	rows, err := db.Query(`
		SELECT content, COUNT(*) as count, GROUP_CONCAT(id) as ids
		FROM episodes
		GROUP BY content
		HAVING COUNT(*) > 1
		ORDER BY COUNT(*) DESC
	`)
	if err != nil {
		return 0, fmt.Errorf("failed to find duplicates: %w", err)
	}
	defer rows.Close()

	var duplicateGroups []struct {
		content string
		count   int
		ids     string
	}

	for rows.Next() {
		var group struct {
			content string
			count   int
			ids     string
		}
		if err := rows.Scan(&group.content, &group.count, &group.ids); err != nil {
			continue
		}
		duplicateGroups = append(duplicateGroups, group)
	}

	if len(duplicateGroups) == 0 {
		log.Println("  No duplicates found")
		return 0, nil
	}

	log.Printf("  Found %d groups of duplicate content", len(duplicateGroups))
	totalDeleted := 0

	// For each duplicate group, keep the earliest and delete the rest
	for _, group := range duplicateGroups {
		ids := strings.Split(group.ids, ",")
		if len(ids) < 2 {
			continue
		}

		// Get full episode details to determine which to keep
		query := fmt.Sprintf(`
			SELECT id, timestamp_event
			FROM episodes
			WHERE id IN (%s)
			ORDER BY timestamp_event ASC, id ASC
		`, strings.Join(makeNPlaceholders(len(ids)), ","))

		args := make([]interface{}, len(ids))
		for i, id := range ids {
			args[i] = id
		}

		episodeRows, err := db.Query(query, args...)
		if err != nil {
			log.Printf("  Warning: failed to query duplicate group: %v", err)
			continue
		}

		var episodeIDs []string
		for episodeRows.Next() {
			var id string
			var timestamp string
			if err := episodeRows.Scan(&id, &timestamp); err != nil {
				continue
			}
			episodeIDs = append(episodeIDs, id)
		}
		episodeRows.Close()

		if len(episodeIDs) < 2 {
			continue
		}

		// Keep first (earliest), delete the rest
		keepID := episodeIDs[0]
		deleteIDs := episodeIDs[1:]

		log.Printf("  Keeping %s, deleting %d duplicates", keepID[:min(len(keepID), 20)], len(deleteIDs))

		// Delete duplicate episodes
		deletePlaceholders := strings.Join(makeNPlaceholders(len(deleteIDs)), ",")
		deleteArgs := make([]interface{}, len(deleteIDs))
		for i, id := range deleteIDs {
			deleteArgs[i] = id
		}

		// Delete from episodes table (cascades should handle edges)
		result, err := db.Exec(
			fmt.Sprintf("DELETE FROM episodes WHERE id IN (%s)", deletePlaceholders),
			deleteArgs...,
		)
		if err != nil {
			log.Printf("  Warning: failed to delete duplicates: %v", err)
			continue
		}

		deleted, _ := result.RowsAffected()
		totalDeleted += int(deleted)
	}

	return totalDeleted, nil
}

func makeNPlaceholders(n int) []string {
	placeholders := make([]string, n)
	for i := range placeholders {
		placeholders[i] = "?"
	}
	return placeholders
}
