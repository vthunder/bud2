package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vthunder/bud2/internal/embedding"
	"github.com/vthunder/bud2/internal/graph"
)

func main() {
	stateDir := flag.String("state", "state", "Path to state directory")
	workers := flag.Int("workers", 4, "Number of parallel workers")
	dryRun := flag.Bool("dry-run", false, "Print stats without compressing")
	flag.Parse()

	// Open database
	dbPath := filepath.Join(*stateDir, "system", "memory.db")
	db, err := graph.Open(*stateDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	log.Printf("Database: %s", dbPath)

	// Wipe existing trace summaries for fresh start
	log.Println("Wiping existing trace summaries...")
	if err := db.DeleteAllTraceSummaries(); err != nil {
		log.Fatalf("Failed to delete existing trace summaries: %v", err)
	}

	// Get all traces
	traces, err := db.GetAllTraces()
	if err != nil {
		log.Fatalf("Failed to get traces: %v", err)
	}

	log.Printf("Total traces: %d", len(traces))

	if *dryRun {
		log.Println("Dry run - exiting")
		return
	}

	// Initialize Ollama client for compression
	ollamaClient := embedding.NewClient("", "")
	ollamaClient.SetGenerationModel("qwen2.5:7b")

	log.Printf("Starting compression with %d workers...", *workers)

	// Compress traces in parallel
	start := time.Now()
	var processed, l64Created, l32Created, l16Created, l8Created, l4Created atomic.Int64
	var skippedNoSources atomic.Int64

	var wg sync.WaitGroup
	traceChan := make(chan *graph.Trace, len(traces))

	// Start workers
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for tr := range traceChan {

				// Get source episodes for this trace
				sourceIDs, err := db.GetTraceSources(tr.ID)
				if err != nil {
					log.Printf("[worker %d] Failed to get sources for trace %s: %v", workerID, tr.ID[:8], err)
					skippedNoSources.Add(1)
					processed.Add(1)
					continue
				}

				if len(sourceIDs) == 0 {
					log.Printf("[worker %d] Trace %s has no source episodes, skipping", workerID, tr.ID[:8])
					skippedNoSources.Add(1)
					processed.Add(1)
					continue
				}

				// Fetch source episodes
				episodes := make([]*graph.Episode, 0, len(sourceIDs))
				for _, epID := range sourceIDs {
					ep, err := db.GetEpisode(epID)
					if err != nil {
						log.Printf("[worker %d] Failed to get episode %s: %v", workerID, epID[:8], err)
						continue
					}
					episodes = append(episodes, ep)
				}

				if len(episodes) == 0 {
					log.Printf("[worker %d] Trace %s has no valid source episodes, skipping", workerID, tr.ID[:8])
					skippedNoSources.Add(1)
					processed.Add(1)
					continue
				}

				// Compress this trace to all levels
				counts, err := compressTrace(db, tr, episodes, ollamaClient)
				if err != nil {
					log.Printf("Failed to compress trace %s: %v", tr.ID[:8], err)
				} else {
					if counts.L64 {
						l64Created.Add(1)
					}
					if counts.L32 {
						l32Created.Add(1)
					}
					if counts.L16 {
						l16Created.Add(1)
					}
					if counts.L8 {
						l8Created.Add(1)
					}
					if counts.L4 {
						l4Created.Add(1)
					}
				}
				n := processed.Add(1)
				if n%10 == 0 || n == int64(len(traces)) {
					elapsed := time.Since(start)
					rate := float64(n) / elapsed.Seconds()
					remaining := time.Duration(float64(len(traces)-int(n))/rate) * time.Second
					log.Printf("Progress: %d/%d (%.1f/s, ~%s remaining)", n, len(traces), rate, remaining.Round(time.Second))
				}
			}
		}(i)
	}

	// Feed traces to workers
	for _, tr := range traces {
		traceChan <- tr
	}
	close(traceChan)

	// Wait for completion
	wg.Wait()

	elapsed := time.Since(start)
	log.Printf("Compression complete!")
	log.Printf("Time: %s (%.1f traces/sec)", elapsed.Round(time.Second), float64(len(traces))/elapsed.Seconds())
	log.Printf("L64 summaries created: %d", l64Created.Load())
	log.Printf("L32 summaries created: %d", l32Created.Load())
	log.Printf("L16 summaries created: %d", l16Created.Load())
	log.Printf("L8 summaries created: %d", l8Created.Load())
	log.Printf("L4 summaries created: %d", l4Created.Load())
	log.Printf("Skipped (no sources): %d", skippedNoSources.Load())
}

type compressionCounts struct {
	L64, L32, L16, L8, L4 bool
}

func compressTrace(db *graph.DB, tr *graph.Trace, episodes []*graph.Episode, compressor graph.Compressor) (compressionCounts, error) {
	var counts compressionCounts

	// Build context from source episodes
	var contextParts []string
	for _, ep := range episodes {
		contextParts = append(contextParts, fmt.Sprintf("[%s] %s", ep.Author, ep.Content))
	}
	sourceContext := strings.Join(contextParts, "\n")

	// Estimate total word count
	wordCount := estimateWordCount(sourceContext)

	// Cascading compression: Generate L64 first (highest detail), then cascade down
	// Each level uses the previous level as input for better consistency

	// L64: ~64 words max (from source episodes)
	l64Summary, err := compressToTarget(sourceContext, compressor, 64, wordCount)
	if err != nil {
		return counts, fmt.Errorf("L64 compression failed: %w", err)
	}
	tokens := estimateTokens(l64Summary)
	if err := db.AddTraceSummary(tr.ID, graph.CompressionLevel64, l64Summary, tokens); err != nil {
		return counts, fmt.Errorf("failed to store L64 summary: %w", err)
	}
	counts.L64 = true

	// L32: ~32 words max (from L64)
	l64Words := estimateWordCount(l64Summary)
	l32Summary, err := compressToTarget(l64Summary, compressor, 32, l64Words)
	if err != nil {
		return counts, fmt.Errorf("L32 compression failed: %w", err)
	}
	tokens = estimateTokens(l32Summary)
	if err := db.AddTraceSummary(tr.ID, graph.CompressionLevel32, l32Summary, tokens); err != nil {
		return counts, fmt.Errorf("failed to store L32 summary: %w", err)
	}
	counts.L32 = true

	// L16: ~16 words max (from L32)
	l32Words := estimateWordCount(l32Summary)
	l16Summary, err := compressToTarget(l32Summary, compressor, 16, l32Words)
	if err != nil {
		return counts, fmt.Errorf("L16 compression failed: %w", err)
	}
	tokens = estimateTokens(l16Summary)
	if err := db.AddTraceSummary(tr.ID, graph.CompressionLevel16, l16Summary, tokens); err != nil {
		return counts, fmt.Errorf("failed to store L16 summary: %w", err)
	}
	counts.L16 = true

	// L8: ~8 words max (from L16)
	l16Words := estimateWordCount(l16Summary)
	l8Summary, err := compressToTarget(l16Summary, compressor, 8, l16Words)
	if err != nil {
		return counts, fmt.Errorf("L8 compression failed: %w", err)
	}
	tokens = estimateTokens(l8Summary)
	if err := db.AddTraceSummary(tr.ID, graph.CompressionLevel8, l8Summary, tokens); err != nil {
		return counts, fmt.Errorf("failed to store L8 summary: %w", err)
	}
	counts.L8 = true

	// L4: ~4 words max (from L8)
	l8Words := estimateWordCount(l8Summary)
	l4Summary, err := compressToTarget(l8Summary, compressor, 4, l8Words)
	if err != nil {
		return counts, fmt.Errorf("L4 compression failed: %w", err)
	}
	tokens = estimateTokens(l4Summary)
	if err := db.AddTraceSummary(tr.ID, graph.CompressionLevel4, l4Summary, tokens); err != nil {
		return counts, fmt.Errorf("failed to store L4 summary: %w", err)
	}
	counts.L4 = true

	return counts, nil
}

// compressToTarget compresses content to target word count or returns verbatim if already below target
func compressToTarget(content string, compressor graph.Compressor, targetWords int, currentWords int) (string, error) {
	// If content is already below target, use verbatim text
	if currentWords <= targetWords {
		return content, nil
	}

	// Otherwise compress to target
	prompt := buildCompressionPrompt(content, targetWords)
	return compressor.Generate(prompt)
}

// buildCompressionPrompt constructs a prompt for trace compression to target word count
func buildCompressionPrompt(content string, targetWords int) string {
	prompt := fmt.Sprintf(`Compress this conversation into a memory trace summary of %d words or less.

Rules:
- Maximum %d words
- Keep the core meaning
- Remove filler, small talk, redundancy
- Preserve key facts and decisions
- Write in past tense (e.g., "User reported..." not "User reports...")

Source conversation:
%s

Compressed summary:`, targetWords, targetWords, content)
	return prompt
}

func estimateTokens(text string) int {
	chars := len(text)
	tokens := chars / 4
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

func estimateWordCount(text string) int {
	// Simple word count: count whitespace-separated tokens
	count := 0
	inWord := false
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			inWord = false
		} else if !inWord {
			count++
			inWord = true
		}
	}
	return count
}
