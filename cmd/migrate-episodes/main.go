// migrate-episodes: One-time migration of episodes from bud2's local SQLite DB into Engram.
//
// Usage:
//
//	migrate-episodes -state /path/to/state -engram-url http://localhost:8080 [-dry-run] [-batch 50]
//
// The tool reads all episodes from memory.db in chronological order and ingests each
// into Engram via POST /v1/episodes. Already-ingested episodes are not de-duplicated by
// Engram (it uses a random UUID per ingest), so this tool should only be run once.
// Use -dry-run to preview what would be sent without actually ingesting.
package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/vthunder/bud2/internal/engram"
	"github.com/vthunder/bud2/internal/graph"
)

func main() {
	stateDir := flag.String("state", "state", "Path to state directory")
	engramURL := flag.String("engram-url", "", "Engram base URL (overrides ENGRAM_URL env var)")
	engramKey := flag.String("engram-key", "", "Engram API key (overrides ENGRAM_API_KEY env var)")
	dryRun := flag.Bool("dry-run", false, "Print episodes that would be ingested without sending")
	batchSize := flag.Int("batch", 50, "Log progress every N episodes")
	flag.Parse()

	// Resolve Engram credentials
	url := *engramURL
	if url == "" {
		url = os.Getenv("ENGRAM_URL")
	}
	if url == "" {
		log.Fatal("Engram URL required: set ENGRAM_URL or pass -engram-url")
	}
	key := *engramKey
	if key == "" {
		key = os.Getenv("ENGRAM_API_KEY")
	}

	// Open SQLite DB — graph.Open expects the state directory (appends system/memory.db)
	db, err := graph.Open(*stateDir)
	if err != nil {
		log.Fatalf("Failed to open memory.db in %s: %v", *stateDir, err)
	}
	defer db.Close()

	total, err := db.CountEpisodes()
	if err != nil {
		log.Fatalf("Failed to count episodes: %v", err)
	}
	log.Printf("Found %d episodes in SQLite DB", total)

	if *dryRun {
		log.Printf("[dry-run] Would ingest %d episodes into %s", total, url)
		// Show last 5 (most recent) as sample
		sample, err := db.GetAllEpisodes(5)
		if err != nil {
			log.Fatalf("Failed to fetch episodes: %v", err)
		}
		for i, ep := range sample {
			idPreview := ep.ID
			if len(idPreview) > 8 {
				idPreview = idPreview[:8]
			}
			log.Printf("  [%d] %s | %s | %s | %q", i+1, idPreview, ep.Author, ep.TimestampEvent.Format(time.RFC3339), truncate(ep.Content, 80))
		}
		return
	}

	client := engram.NewClient(url, key)

	// Fetch all episodes oldest-first (reverse of GetAllEpisodes order)
	// GetAllEpisodes returns newest-first; we ingest oldest-first for correct ordering
	episodes, err := db.GetAllEpisodes(total + 100) // +100 buffer for any new episodes during migration
	if err != nil {
		log.Fatalf("Failed to fetch episodes: %v", err)
	}

	// Reverse to get oldest-first
	for i, j := 0, len(episodes)-1; i < j; i, j = i+1, j-1 {
		episodes[i], episodes[j] = episodes[j], episodes[i]
	}

	log.Printf("Ingesting %d episodes into Engram at %s...", len(episodes), url)
	ingested := 0
	skipped := 0
	start := time.Now()

	for i, ep := range episodes {
		// Skip episodes with empty content
		if ep.Content == "" {
			skipped++
			continue
		}

		ts := ep.TimestampEvent
		if ts.IsZero() {
			ts = ep.TimestampIngested
		}
		req := engram.IngestEpisodeRequest{
			Content:        ep.Content,
			Source:         ep.Source,
			Author:         ep.Author,
			AuthorID:       ep.AuthorID,
			Channel:        ep.Channel,
			TimestampEvent: ts,
			ReplyTo:        ep.ReplyTo,
		}
		// Include embedding if available (saves Engram from re-embedding)
		if len(ep.Embedding) > 0 {
			req.Embedding = ep.Embedding
		}

		if _, err := client.IngestEpisode(req); err != nil {
			epID := ep.ID
			if len(epID) > 8 {
				epID = epID[:8]
			}
			log.Printf("  [%d/%d] ERROR ingesting %s: %v", i+1, len(episodes), epID, err)
			// Continue — don't abort on single failure
			skipped++
			continue
		}

		ingested++
		if ingested%*batchSize == 0 {
			elapsed := time.Since(start)
			rate := float64(ingested) / elapsed.Seconds()
			eta := time.Duration(float64(len(episodes)-ingested)/rate) * time.Second
			log.Printf("  Progress: %d/%d ingested (%.1f/s, ETA %s)", ingested, len(episodes), rate, eta.Round(time.Second))
		}
	}

	elapsed := time.Since(start)
	log.Printf("Done: ingested=%d skipped=%d total=%d in %s", ingested, skipped, len(episodes), elapsed.Round(time.Millisecond))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
