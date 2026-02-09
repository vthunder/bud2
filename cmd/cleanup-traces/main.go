package main

import (
	"database/sql"
	"flag"
	"log"
	"path/filepath"

	"github.com/vthunder/bud2/internal/graph"
)

func main() {
	stateDir := flag.String("state", "state", "Path to state directory")
	dryRun := flag.Bool("dry-run", false, "Show what would be deleted without actually deleting")
	deleteOrphaned := flag.Bool("delete-orphaned", false, "Delete orphaned traces that have summaries but no sources")
	flag.Parse()

	// Open database
	dbPath := filepath.Join(*stateDir, "system", "memory.db")
	graphDB, err := graph.Open(*stateDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer graphDB.Close()

	// Get raw SQL connection
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("Failed to open raw database connection: %v", err)
	}
	defer db.Close()

	log.Printf("Database: %s", dbPath)

	// Step 1: Find and delete obsolete core-* traces
	log.Println("\n=== Step 1: Checking for obsolete core-* traces ===")

	coreTraces, err := db.Query(`
		SELECT id, summary, created_at
		FROM traces
		WHERE id LIKE 'core-%'
		ORDER BY created_at DESC
	`)
	if err != nil {
		log.Fatalf("Failed to query core traces: %v", err)
	}

	coreCount := 0
	for coreTraces.Next() {
		var id, summary string
		var createdAt string
		if err := coreTraces.Scan(&id, &summary, &createdAt); err != nil {
			continue
		}
		coreCount++
		if coreCount <= 5 {
			summaryPreview := summary
			if len(summaryPreview) > 60 {
				summaryPreview = summaryPreview[:60] + "..."
			}
			log.Printf("  - %s (%s): %s", id, createdAt, summaryPreview)
		}
	}
	coreTraces.Close()

	if coreCount > 0 {
		if coreCount > 5 {
			log.Printf("  ... and %d more", coreCount-5)
		}
		log.Printf("Found %d obsolete core-* traces (now using state/core.md)", coreCount)

		if !*dryRun {
			result, err := db.Exec("DELETE FROM traces WHERE id LIKE 'core-%'")
			if err != nil {
				log.Fatalf("Failed to delete core traces: %v", err)
			}
			deleted, _ := result.RowsAffected()
			log.Printf("✓ Deleted %d core-* traces", deleted)
		} else {
			log.Printf("DRY RUN: Would delete %d core-* traces", coreCount)
		}
	} else {
		log.Println("No core-* traces found")
	}

	// Step 2: Find orphaned traces (no source episodes)
	log.Println("\n=== Step 2: Checking for orphaned traces (no source episodes) ===")

	orphanedRows, err := db.Query(`
		SELECT t.id, t.summary, t.is_core, t.created_at, t.last_accessed
		FROM traces t
		WHERE t.id NOT IN (SELECT DISTINCT trace_id FROM trace_sources)
		AND t.id NOT LIKE 'core-%'
		ORDER BY t.last_accessed DESC
	`)
	if err != nil {
		log.Fatalf("Failed to query orphaned traces: %v", err)
	}

	var orphanedWithSummary []string
	var orphanedWithoutSummary []string

	for orphanedRows.Next() {
		var id string
		var summary *string
		var isCore bool
		var createdAt, lastAccessed string

		if err := orphanedRows.Scan(&id, &summary, &isCore, &createdAt, &lastAccessed); err != nil {
			continue
		}

		if summary != nil && *summary != "" {
			orphanedWithSummary = append(orphanedWithSummary, id)
		} else {
			orphanedWithoutSummary = append(orphanedWithoutSummary, id)
		}
	}
	orphanedRows.Close()

	log.Printf("Found %d orphaned traces:", len(orphanedWithSummary)+len(orphanedWithoutSummary))
	log.Printf("  - %d with summary (can potentially be compressed)", len(orphanedWithSummary))
	log.Printf("  - %d without summary (should be deleted)", len(orphanedWithoutSummary))

	// Step 3: Delete traces without summary (definitely broken)
	if len(orphanedWithoutSummary) > 0 {
		log.Println("\n=== Step 3: Deleting broken traces (no summary, no sources) ===")

		if !*dryRun {
			deleted := 0
			for _, id := range orphanedWithoutSummary {
				result, err := db.Exec("DELETE FROM traces WHERE id = ?", id)
				if err != nil {
					log.Printf("Failed to delete trace %s: %v", id, err)
					continue
				}
				rows, _ := result.RowsAffected()
				deleted += int(rows)
			}
			log.Printf("✓ Deleted %d broken traces", deleted)
		} else {
			log.Printf("DRY RUN: Would delete %d broken traces", len(orphanedWithoutSummary))
		}
	}

	// Step 4: Check orphaned traces that DO have summaries
	if len(orphanedWithSummary) > 0 {
		log.Println("\n=== Step 4: Checking orphaned traces with summaries ===")

		// Show sample
		limit := 5
		if len(orphanedWithSummary) < limit {
			limit = len(orphanedWithSummary)
		}

		for i := 0; i < limit; i++ {
			id := orphanedWithSummary[i]
			row := db.QueryRow("SELECT substr(summary, 1, 60), created_at FROM traces WHERE id = ?", id)
			var summaryPreview, createdAt string
			if err := row.Scan(&summaryPreview, &createdAt); err == nil {
				log.Printf("  - %s (%s): %s...", id, createdAt, summaryPreview)
			}
		}

		if len(orphanedWithSummary) > limit {
			log.Printf("  ... and %d more", len(orphanedWithSummary)-limit)
		}

		log.Printf("\nThese %d traces have summaries but no source episodes.", len(orphanedWithSummary))
		log.Printf("Options:")
		log.Printf("  A) Delete them (they can't be properly compressed without sources)")
		log.Printf("  B) Keep them (they might have useful cached summaries)")
		log.Printf("\nRecommendation: Delete them. Without source episodes, they can't be")
		log.Printf("refreshed or compressed to different levels. Run with --delete-orphaned")

		if *deleteOrphaned && !*dryRun {
			deleted := 0
			for _, id := range orphanedWithSummary {
				result, err := db.Exec("DELETE FROM traces WHERE id = ?", id)
				if err != nil {
					log.Printf("Failed to delete trace %s: %v", id, err)
					continue
				}
				rows, _ := result.RowsAffected()
				deleted += int(rows)
			}
			log.Printf("\n✓ Deleted %d orphaned traces with summaries", deleted)
		} else if *deleteOrphaned && *dryRun {
			log.Printf("\nDRY RUN: Would delete %d orphaned traces with summaries", len(orphanedWithSummary))
		}
	}

	// Final summary
	log.Println("\n=== Summary ===")
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM traces").Scan(&count); err == nil {
		log.Printf("Traces remaining in database: %d", count)
	}

	var withSourcesCount int
	if err := db.QueryRow("SELECT COUNT(DISTINCT trace_id) FROM trace_sources").Scan(&withSourcesCount); err == nil {
		log.Printf("Traces with source episodes: %d", withSourcesCount)
	}

	log.Println("\nNext step: Run compress-traces to generate summaries for valid traces")
}
