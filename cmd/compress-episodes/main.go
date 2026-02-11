package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
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

	// Wipe existing summaries if requested, otherwise resume
	if *wipe {
		log.Println("Wiping all existing summaries...")
		if err := db.DeleteAllEpisodeSummaries(); err != nil {
			log.Fatalf("Failed to delete existing summaries: %v", err)
		}
	} else {
		log.Println("Resume mode: checking for missing compression levels...")
	}

	// Count episodes
	stats, err := db.Stats()
	if err != nil {
		log.Fatalf("Failed to get stats: %v", err)
	}

	totalEpisodes := stats["episodes"]
	totalSummaries := stats["episode_summaries"]

	log.Printf("Episodes: %d", totalEpisodes)
	log.Printf("Existing summaries: %d", totalSummaries)

	// Get all episodes that need any compression level
	episodes, err := getUncompressedEpisodes(db)
	if err != nil {
		log.Fatalf("Failed to get uncompressed episodes: %v", err)
	}

	log.Printf("Episodes needing compression: %d", len(episodes))

	if *dryRun {
		log.Println("Dry run - exiting")
		return
	}

	// Initialize Ollama client for compression
	ollamaClient := embedding.NewClient("", "")
	ollamaClient.SetGenerationModel("llama3.2:latest")

	log.Printf("Starting compression with %d workers...", *workers)

	// Compress episodes in parallel
	start := time.Now()
	var processed, l64Created, l32Created, l16Created, l8Created, l4Created atomic.Int64

	var wg sync.WaitGroup
	episodeChan := make(chan *graph.Episode, len(episodes))

	// Start workers
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			log.Printf("[worker %d] Started", workerID)
			for ep := range episodeChan {
				log.Printf("[worker %d] Processing episode %s", workerID, ep.ShortID)
				// Compress this episode to all levels (or just missing levels in resume mode)
				counts, err := compressEpisode(db, ep, ollamaClient, !*wipe)
				if err != nil {
					log.Printf("[worker %d] Failed to compress episode %s: %v", workerID, ep.ShortID, err)
				} else {
					log.Printf("[worker %d] Successfully compressed episode %s", workerID, ep.ShortID)
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
				if n%10 == 0 || n == int64(len(episodes)) {
					elapsed := time.Since(start)
					rate := float64(n) / elapsed.Seconds()
					remaining := time.Duration(float64(len(episodes)-int(n))/rate) * time.Second
					log.Printf("Progress: %d/%d (%.1f/s, ~%s remaining)", n, len(episodes), rate, remaining.Round(time.Second))
				}
			}
			log.Printf("[worker %d] Finished", workerID)
		}(i)
	}

	// Feed episodes to workers
	for _, ep := range episodes {
		episodeChan <- ep
	}
	close(episodeChan)

	// Wait for completion
	wg.Wait()

	elapsed := time.Since(start)
	log.Printf("Compression complete!")
	log.Printf("Time: %s (%.1f episodes/sec)", elapsed.Round(time.Second), float64(len(episodes))/elapsed.Seconds())
	log.Printf("L64 summaries created: %d", l64Created.Load())
	log.Printf("L32 summaries created: %d", l32Created.Load())
	log.Printf("L16 summaries created: %d", l16Created.Load())
	log.Printf("L8 summaries created: %d", l8Created.Load())
	log.Printf("L4 summaries created: %d", l4Created.Load())
}

type compressionCounts struct {
	L64, L32, L16, L8, L4 bool
}

func getUncompressedEpisodes(db *graph.DB) ([]*graph.Episode, error) {
	// Query all episodes (no limit)
	episodes, err := db.GetAllEpisodes(10000)
	if err != nil {
		return nil, err
	}

	// Filter to only those missing any compression level
	var uncompressed []*graph.Episode
	for _, ep := range episodes {
		needsCompression := false

		// Check all compression levels (L64, L32, L16, L8, L4)
		for _, level := range []int{graph.CompressionLevel64, graph.CompressionLevel32, graph.CompressionLevel16, graph.CompressionLevel8, graph.CompressionLevel4} {
			summary, err := db.GetEpisodeSummary(ep.ID, level)
			if err != nil {
				return nil, err
			}
			if summary == nil {
				needsCompression = true
				break
			}
		}

		if needsCompression {
			uncompressed = append(uncompressed, ep)
		}
	}

	return uncompressed, nil
}

func compressEpisode(db *graph.DB, ep *graph.Episode, compressor graph.Compressor, resumeMode bool) (compressionCounts, error) {
	var counts compressionCounts

	// Generate all compression levels (verbatim if already below target)
	wordCount := estimateWordCount(ep.Content)
	log.Printf("  Episode %s has %d words", ep.ShortID, wordCount)

	// In resume mode, check which levels are missing and only compress those
	missingLevels := make(map[int]bool)
	if resumeMode {
		for _, level := range []int{graph.CompressionLevel64, graph.CompressionLevel32, graph.CompressionLevel16, graph.CompressionLevel8, graph.CompressionLevel4} {
			summary, err := db.GetEpisodeSummary(ep.ID, level)
			if err != nil {
				return counts, fmt.Errorf("failed to check L%d: %w", level, err)
			}
			if summary == nil {
				missingLevels[level] = true
			}
		}
		if len(missingLevels) == 0 {
			log.Printf("  Episode %s has all compression levels, skipping", ep.ShortID)
			return counts, nil
		}
		log.Printf("  Episode %s missing levels: %v", ep.ShortID, missingLevels)
	} else {
		// Not in resume mode, compress all levels
		missingLevels[graph.CompressionLevel64] = true
		missingLevels[graph.CompressionLevel32] = true
		missingLevels[graph.CompressionLevel16] = true
		missingLevels[graph.CompressionLevel8] = true
		missingLevels[graph.CompressionLevel4] = true
	}

	// L64: ~64 words max
	if missingLevels[graph.CompressionLevel64] {
		log.Printf("  Compressing %s to L64...", ep.ShortID)
		summary, err := compressToTarget(ep, compressor, 64, wordCount)
		if err != nil {
			return counts, fmt.Errorf("L64 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddEpisodeSummary(ep.ID, graph.CompressionLevel64, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L64 summary: %w", err)
		}
		counts.L64 = true
		log.Printf("  ✓ L64 done for %s", ep.ShortID)
	}

	// L32: ~32 words max
	if missingLevels[graph.CompressionLevel32] {
		summary, err := compressToTarget(ep, compressor, 32, wordCount)
		if err != nil {
			return counts, fmt.Errorf("L32 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddEpisodeSummary(ep.ID, graph.CompressionLevel32, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L32 summary: %w", err)
		}
		counts.L32 = true
	}

	// L16: ~16 words max
	if missingLevels[graph.CompressionLevel16] {
		summary, err := compressToTarget(ep, compressor, 16, wordCount)
		if err != nil {
			return counts, fmt.Errorf("L16 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddEpisodeSummary(ep.ID, graph.CompressionLevel16, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L16 summary: %w", err)
		}
		counts.L16 = true
	}

	// L8: ~8 words max
	if missingLevels[graph.CompressionLevel8] {
		summary, err := compressToTarget(ep, compressor, 8, wordCount)
		if err != nil {
			return counts, fmt.Errorf("L8 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddEpisodeSummary(ep.ID, graph.CompressionLevel8, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L8 summary: %w", err)
		}
		counts.L8 = true
	}

	// L4: ~4 words max
	if missingLevels[graph.CompressionLevel4] {
		summary, err := compressToTarget(ep, compressor, 4, wordCount)
		if err != nil {
			return counts, fmt.Errorf("L4 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddEpisodeSummary(ep.ID, graph.CompressionLevel4, summary, tokens); err != nil {
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

// compressToTarget compresses episode to target word count or returns verbatim if already below target
func compressToTarget(ep *graph.Episode, compressor graph.Compressor, targetWords int, currentWords int) (string, error) {
	// If episode is already below target, use verbatim text
	if currentWords <= targetWords {
		return ep.Content, nil
	}

	// Otherwise compress to target
	prompt := buildCompressionPrompt(ep, targetWords)
	summary, err := compressor.Generate(prompt)
	if err != nil {
		return "", err
	}

	// Check for CJK leakage: if output has CJK but input doesn't, re-summarize with fallback
	if hasCJK(summary) && !hasCJK(ep.Content) {
		log.Printf("  ⚠ CJK detected in summary for episode %s, retrying with Mistral...", ep.ShortID)
		// Try fallback with Mistral (English-focused model)
		if mistralCompressor, ok := compressor.(interface{ SetGenerationModel(string) }); ok {
			mistralCompressor.SetGenerationModel("mistral")
			fallbackSummary, fallbackErr := compressor.Generate(prompt)
			if fallbackErr == nil && !hasCJK(fallbackSummary) {
				log.Printf("  ✓ Mistral fallback successful for episode %s", ep.ShortID)
				// Reset model back to default
				mistralCompressor.SetGenerationModel("llama3.2:latest")
				return fallbackSummary, nil
			}
			// Reset model back to default
			mistralCompressor.SetGenerationModel("llama3.2:latest")
			if fallbackErr != nil {
				log.Printf("  ✗ Mistral fallback failed for episode %s: %v", ep.ShortID, fallbackErr)
			} else {
				log.Printf("  ✗ Mistral fallback still has CJK for episode %s", ep.ShortID)
			}
		}
	}

	return summary, nil
}

// buildCompressionPrompt constructs a prompt for episode compression to target word count
func buildCompressionPrompt(ep *graph.Episode, targetWords int) string {
	prompt := fmt.Sprintf(`Compress this message to %d words or less.

Rules:
- Maximum %d words
- Keep the core meaning
- Remove filler, small talk, redundancy
- Preserve key facts and decisions
- CRITICAL: You MUST write ONLY in English - NO Chinese characters allowed
- If you write ANY Chinese characters (像这样的字符), the output will be REJECTED
- Use ONLY English words from A-Z - absolutely NO non-English characters

%sOriginal message:
%s

Compressed version (ONLY English):`, targetWords, targetWords, dialogueActContext(ep), ep.Content)
	return prompt
}

func dialogueActContext(ep *graph.Episode) string {
	if ep.DialogueAct != "" {
		return fmt.Sprintf("- Type: %s\n", ep.DialogueAct)
	}
	return ""
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
