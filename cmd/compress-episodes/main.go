package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
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

	// Count episodes
	stats, err := db.Stats()
	if err != nil {
		log.Fatalf("Failed to get stats: %v", err)
	}

	totalEpisodes := stats["episodes"]
	totalSummaries := stats["episode_summaries"]

	log.Printf("Episodes: %d", totalEpisodes)
	log.Printf("Existing summaries: %d", totalSummaries)

	// Get episodes without summaries
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
	ollamaClient.SetGenerationModel("qwen2.5:7b")

	log.Printf("Starting compression with %d workers...", *workers)

	// Compress episodes in parallel
	start := time.Now()
	var processed, l1Created, l2Created, l3Created, l4Created, l5Created atomic.Int64

	var wg sync.WaitGroup
	episodeChan := make(chan *graph.Episode, len(episodes))

	// Start workers
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for ep := range episodeChan {
				// Compress this episode
				counts, err := compressEpisode(db, ep, ollamaClient)
				if err != nil {
					log.Printf("[worker %d] Failed to compress episode %s: %v", workerID, ep.ShortID, err)
				} else {
					if counts.L1 {
						l1Created.Add(1)
					}
					if counts.L2 {
						l2Created.Add(1)
					}
					if counts.L3 {
						l3Created.Add(1)
					}
					if counts.L4 {
						l4Created.Add(1)
					}
					if counts.L5 {
						l5Created.Add(1)
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
	log.Printf("L1 summaries created: %d", l1Created.Load())
	log.Printf("L2 summaries created: %d", l2Created.Load())
	log.Printf("L3 summaries created: %d", l3Created.Load())
	log.Printf("L4 summaries created: %d", l4Created.Load())
	log.Printf("L5 summaries created: %d", l5Created.Load())
}

type compressionCounts struct {
	L1, L2, L3, L4, L5 bool
}

func getUncompressedEpisodes(db *graph.DB) ([]*graph.Episode, error) {
	// Query all episodes (no limit)
	episodes, err := db.GetAllEpisodes(10000)
	if err != nil {
		return nil, err
	}

	// Filter to only those that need compression based on token count
	var uncompressed []*graph.Episode
	for _, ep := range episodes {
		// Skip very short episodes (< 50 tokens)
		if ep.TokenCount < 50 {
			continue
		}

		needsCompression := false

		// Check if needs L1 (> 100 tokens)
		if ep.TokenCount > 100 {
			l1Summary, err := db.GetEpisodeSummary(ep.ID, graph.CompressionLevelMedium)
			if err != nil {
				return nil, err
			}
			if l1Summary == nil {
				needsCompression = true
			}
		}

		// Check if needs L2 (> 300 tokens)
		if ep.TokenCount > 300 {
			l2Summary, err := db.GetEpisodeSummary(ep.ID, graph.CompressionLevelHigh)
			if err != nil {
				return nil, err
			}
			if l2Summary == nil {
				needsCompression = true
			}
		}

		// Check if needs L3 (> 500 tokens)
		if ep.TokenCount > 500 {
			l3Summary, err := db.GetEpisodeSummary(ep.ID, graph.CompressionLevelCore)
			if err != nil {
				return nil, err
			}
			if l3Summary == nil {
				needsCompression = true
			}
		}

		// Check if needs L4 (> 700 tokens)
		if ep.TokenCount > 700 {
			l4Summary, err := db.GetEpisodeSummary(ep.ID, graph.CompressionLevelMinimal)
			if err != nil {
				return nil, err
			}
			if l4Summary == nil {
				needsCompression = true
			}
		}

		// Check if needs L5 (> 1000 tokens)
		if ep.TokenCount > 1000 {
			l5Summary, err := db.GetEpisodeSummary(ep.ID, graph.CompressionLevelUltra)
			if err != nil {
				return nil, err
			}
			if l5Summary == nil {
				needsCompression = true
			}
		}

		if needsCompression {
			uncompressed = append(uncompressed, ep)
		}
	}

	return uncompressed, nil
}

func compressEpisode(db *graph.DB, ep *graph.Episode, compressor graph.Compressor) (compressionCounts, error) {
	var counts compressionCounts

	// Skip very short episodes
	if ep.TokenCount < 50 {
		return counts, nil
	}

	// L1: Key points (if > 100 tokens)
	if ep.TokenCount > 100 {
		summary, err := compressToKeyPoints(ep, compressor)
		if err != nil {
			return counts, fmt.Errorf("L1 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddEpisodeSummary(ep.ID, graph.CompressionLevelMedium, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L1 summary: %w", err)
		}
		counts.L1 = true
	}

	// L2: Essential (if > 300 tokens)
	if ep.TokenCount > 300 {
		summary, err := compressToEssence(ep, compressor)
		if err != nil {
			return counts, fmt.Errorf("L2 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddEpisodeSummary(ep.ID, graph.CompressionLevelHigh, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L2 summary: %w", err)
		}
		counts.L2 = true
	}

	// L3: Core facts (if > 500 tokens)
	if ep.TokenCount > 500 {
		summary, err := compressToCore(ep, compressor)
		if err != nil {
			return counts, fmt.Errorf("L3 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddEpisodeSummary(ep.ID, graph.CompressionLevelCore, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L3 summary: %w", err)
		}
		counts.L3 = true
	}

	// L4: Minimal (if > 700 tokens)
	if ep.TokenCount > 700 {
		summary, err := compressToMinimal(ep, compressor)
		if err != nil {
			return counts, fmt.Errorf("L4 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddEpisodeSummary(ep.ID, graph.CompressionLevelMinimal, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L4 summary: %w", err)
		}
		counts.L4 = true
	}

	// L5: Ultra-compressed (if > 1000 tokens)
	if ep.TokenCount > 1000 {
		summary, err := compressToUltra(ep, compressor)
		if err != nil {
			return counts, fmt.Errorf("L5 compression failed: %w", err)
		}
		tokens := estimateTokens(summary)
		if err := db.AddEpisodeSummary(ep.ID, graph.CompressionLevelUltra, summary, tokens); err != nil {
			return counts, fmt.Errorf("failed to store L5 summary: %w", err)
		}
		counts.L5 = true
	}

	return counts, nil
}

func compressToKeyPoints(ep *graph.Episode, compressor graph.Compressor) (string, error) {
	prompt := fmt.Sprintf(`You are compressing a conversation message to key points.

Extract the key points from this message:
- Keep important facts, decisions, and insights
- Remove small talk, acknowledgments, and filler
- Preserve technical details and context
- Output 1-2 sentences

Message context:
- Author: %s
%s
Message:
%s

Summary:`, ep.Author, dialogueActContext(ep), ep.Content)
	return compressor.Generate(prompt)
}

func compressToEssence(ep *graph.Episode, compressor graph.Compressor) (string, error) {
	prompt := fmt.Sprintf(`You are compressing a conversation message to essence.

Summarize the essential point of this message in 1 sentence (20-30 words):
- What is the core information?
- Strip everything except the minimal necessary context

Message context:
- Author: %s
%s
Message:
%s

Summary:`, ep.Author, dialogueActContext(ep), ep.Content)
	return compressor.Generate(prompt)
}

func compressToCore(ep *graph.Episode, compressor graph.Compressor) (string, error) {
	prompt := fmt.Sprintf(`You are compressing a conversation message to core facts.

Compress to core facts in exactly 16 words or less:
- Keep only the absolutely essential information
- Remove all context and qualifiers
- Focus on the main subject and action

Message context:
- Author: %s
%s
Message:
%s

Summary:`, ep.Author, dialogueActContext(ep), ep.Content)
	return compressor.Generate(prompt)
}

func compressToMinimal(ep *graph.Episode, compressor graph.Compressor) (string, error) {
	prompt := fmt.Sprintf(`You are compressing a conversation message to minimal.

Compress to exactly 8 words or less:
- Maximum compression while remaining intelligible
- Subject + verb + object only
- No extra words

Message context:
- Author: %s
%s
Message:
%s

Summary:`, ep.Author, dialogueActContext(ep), ep.Content)
	return compressor.Generate(prompt)
}

func compressToUltra(ep *graph.Episode, compressor graph.Compressor) (string, error) {
	prompt := fmt.Sprintf(`You are compressing a conversation message to ultra-compressed.

Compress to exactly 4 words:
- Absolute minimum representation
- Key concepts only
- Telegram style

Message context:
- Author: %s
%s
Message:
%s

Summary:`, ep.Author, dialogueActContext(ep), ep.Content)
	return compressor.Generate(prompt)
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
