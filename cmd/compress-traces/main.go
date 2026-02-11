package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
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
	wipe := flag.Bool("wipe", false, "Wipe all existing summaries and rebuild from scratch")
	flag.Parse()

	// Open database
	dbPath := filepath.Join(*stateDir, "system", "memory.db")
	db, err := graph.Open(*stateDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	log.Printf("Database: %s", dbPath)

	// Wipe existing trace summaries if requested, otherwise resume
	if *wipe {
		log.Println("Wiping all existing trace summaries...")
		if err := db.DeleteAllTraceSummaries(); err != nil {
			log.Fatalf("Failed to delete existing trace summaries: %v", err)
		}
	} else {
		log.Println("Resume mode: checking for missing compression levels...")
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
	ollamaClient.SetGenerationModel("llama3.2:latest")

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
					log.Printf("[worker %d] Failed to get sources for trace %s: %v", workerID, tr.ID[:15], err)
					skippedNoSources.Add(1)
					processed.Add(1)
					continue
				}

				if len(sourceIDs) == 0 {
					log.Printf("[worker %d] Trace %s has no source episodes, skipping", workerID, tr.ID[:15])
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
					log.Printf("[worker %d] Trace %s has no valid source episodes, skipping", workerID, tr.ID[:15])
					skippedNoSources.Add(1)
					processed.Add(1)
					continue
				}

				// Compress this trace to all levels (or just missing levels in resume mode)
				counts, err := compressTrace(db, tr, episodes, ollamaClient, !*wipe)
				if err != nil {
					log.Printf("Failed to compress trace %s: %v", tr.ID[:15], err)
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

func compressTrace(db *graph.DB, tr *graph.Trace, episodes []*graph.Episode, compressor graph.Compressor, resumeMode bool) (compressionCounts, error) {
	var counts compressionCounts

	// In resume mode, check which levels are missing
	missingLevels := make(map[int]bool)
	if resumeMode {
		for _, level := range []int{graph.CompressionLevel64, graph.CompressionLevel32, graph.CompressionLevel16, graph.CompressionLevel8, graph.CompressionLevel4} {
			summary, err := db.GetTraceSummary(tr.ID, level)
			if err != nil {
				return counts, fmt.Errorf("failed to check L%d: %w", level, err)
			}
			if summary == nil {
				missingLevels[level] = true
			}
		}
		if len(missingLevels) == 0 {
			log.Printf("  Trace %s has all compression levels, skipping", tr.ID[:15])
			return counts, nil
		}
		log.Printf("  Trace %s missing levels: %v", tr.ID[:15], missingLevels)
	} else {
		// Not in resume mode, compress all levels
		missingLevels[graph.CompressionLevel64] = true
		missingLevels[graph.CompressionLevel32] = true
		missingLevels[graph.CompressionLevel16] = true
		missingLevels[graph.CompressionLevel8] = true
		missingLevels[graph.CompressionLevel4] = true
	}

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
	// In resume mode, we need to load existing summaries if they exist to use as source for lower levels

	var l64Summary, l32Summary, l16Summary, l8Summary string

	// L64: ~64 words max (from source episodes)
	if missingLevels[graph.CompressionLevel64] {
		summary, err := compressToTarget(sourceContext, compressor, 64, wordCount)
		if err != nil {
			return counts, fmt.Errorf("L64 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddTraceSummary(tr.ID, graph.CompressionLevel64, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L64 summary: %w", err)
		}
		counts.L64 = true
		l64Summary = summary
	} else {
		// Load existing L64 for cascading compression
		existing, err := db.GetTraceSummary(tr.ID, graph.CompressionLevel64)
		if err != nil {
			return counts, fmt.Errorf("failed to load existing L64: %w", err)
		}
		if existing != nil {
			l64Summary = existing.Summary
		}
	}

	// L32: ~32 words max (from L64)
	if missingLevels[graph.CompressionLevel32] {
		if l64Summary == "" {
			return counts, fmt.Errorf("L32 compression requires L64, but L64 is missing")
		}
		l64Words := estimateWordCount(l64Summary)
		summary, err := compressToTarget(l64Summary, compressor, 32, l64Words)
		if err != nil {
			return counts, fmt.Errorf("L32 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddTraceSummary(tr.ID, graph.CompressionLevel32, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L32 summary: %w", err)
		}
		counts.L32 = true
		l32Summary = summary
	} else {
		// Load existing L32 for cascading compression
		existing, err := db.GetTraceSummary(tr.ID, graph.CompressionLevel32)
		if err != nil {
			return counts, fmt.Errorf("failed to load existing L32: %w", err)
		}
		if existing != nil {
			l32Summary = existing.Summary
		}
	}

	// L16: ~16 words max (from L32)
	if missingLevels[graph.CompressionLevel16] {
		if l32Summary == "" {
			return counts, fmt.Errorf("L16 compression requires L32, but L32 is missing")
		}
		l32Words := estimateWordCount(l32Summary)
		summary, err := compressToTarget(l32Summary, compressor, 16, l32Words)
		if err != nil {
			return counts, fmt.Errorf("L16 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddTraceSummary(tr.ID, graph.CompressionLevel16, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L16 summary: %w", err)
		}
		counts.L16 = true
		l16Summary = summary
	} else {
		// Load existing L16 for cascading compression
		existing, err := db.GetTraceSummary(tr.ID, graph.CompressionLevel16)
		if err != nil {
			return counts, fmt.Errorf("failed to load existing L16: %w", err)
		}
		if existing != nil {
			l16Summary = existing.Summary
		}
	}

	// L8: ~8 words max (from L16)
	if missingLevels[graph.CompressionLevel8] {
		if l16Summary == "" {
			return counts, fmt.Errorf("L8 compression requires L16, but L16 is missing")
		}
		l16Words := estimateWordCount(l16Summary)
		summary, err := compressToTarget(l16Summary, compressor, 8, l16Words)
		if err != nil {
			return counts, fmt.Errorf("L8 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddTraceSummary(tr.ID, graph.CompressionLevel8, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L8 summary: %w", err)
		}
		counts.L8 = true
		l8Summary = summary
	} else {
		// Load existing L8 for cascading compression
		existing, err := db.GetTraceSummary(tr.ID, graph.CompressionLevel8)
		if err != nil {
			return counts, fmt.Errorf("failed to load existing L8: %w", err)
		}
		if existing != nil {
			l8Summary = existing.Summary
		}
	}

	// L4: ~4 words max (from L8)
	if missingLevels[graph.CompressionLevel4] {
		if l8Summary == "" {
			return counts, fmt.Errorf("L4 compression requires L8, but L8 is missing")
		}
		l8Words := estimateWordCount(l8Summary)
		summary, err := compressToTarget(l8Summary, compressor, 4, l8Words)
		if err != nil {
			return counts, fmt.Errorf("L4 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddTraceSummary(tr.ID, graph.CompressionLevel4, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L4 summary: %w", err)
		}
		counts.L4 = true
	}

	return counts, nil
}

// hasCJK returns true if the text contains any CJK (Chinese/Japanese/Korean) characters
func hasCJK(text string) bool {
	re := regexp.MustCompile(`[\x{4E00}-\x{9FFF}]`)
	return re.MatchString(text)
}

// compressToTarget compresses content to target word count or returns verbatim if already below target
func compressToTarget(content string, compressor graph.Compressor, targetWords int, currentWords int) (string, error) {
	// If content is already below target, use verbatim text
	if currentWords <= targetWords {
		return content, nil
	}

	// Otherwise compress to target
	prompt := buildCompressionPrompt(content, targetWords)
	summary, err := compressor.Generate(prompt)
	if err != nil {
		return "", err
	}

	// Check for CJK leakage: if output has CJK but input doesn't, re-summarize with fallback
	if hasCJK(summary) && !hasCJK(content) {
		log.Printf("  ⚠ CJK detected in trace summary (L%d), retrying with Mistral...", targetWords)
		// Try fallback with Mistral (English-focused model)
		if mistralCompressor, ok := compressor.(interface{ SetGenerationModel(string) }); ok {
			mistralCompressor.SetGenerationModel("mistral")
			fallbackSummary, fallbackErr := compressor.Generate(prompt)
			if fallbackErr == nil && !hasCJK(fallbackSummary) {
				log.Printf("  ✓ Mistral fallback successful (L%d)", targetWords)
				// Reset model back to default
				mistralCompressor.SetGenerationModel("llama3.2:latest")
				return fallbackSummary, nil
			}
			// Reset model back to default
			mistralCompressor.SetGenerationModel("llama3.2:latest")
			if fallbackErr != nil {
				log.Printf("  ✗ Mistral fallback failed (L%d): %v", targetWords, fallbackErr)
			} else {
				log.Printf("  ✗ Mistral fallback still has CJK (L%d)", targetWords)
			}
		}
	}

	return summary, nil
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
- CRITICAL: You MUST write ONLY in English - NO Chinese characters allowed
- If you write ANY Chinese characters (像这样的字符), the output will be REJECTED
- Use ONLY English words from A-Z - absolutely NO non-English characters

Source conversation:
%s

Compressed summary (ONLY English):`, targetWords, targetWords, content)
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
