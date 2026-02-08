package graph

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// EpisodeSummary represents a compressed version of an episode
type EpisodeSummary struct {
	ID               int    `json:"id"`
	EpisodeID        string `json:"episode_id"`
	CompressionLevel int    `json:"compression_level"`
	Summary          string `json:"summary"`
	Tokens           int    `json:"tokens"`
}

// CompressionLevel represents different tiers of compression (pyramid structure)
const (
	CompressionLevelMedium      = 1 // Key points (~50-100 words)
	CompressionLevelHigh        = 2 // Essential summary (~20-30 words)
	CompressionLevelCore        = 3 // Core facts (~16 words)
	CompressionLevelMinimal     = 4 // Minimal summary (~8 words)
	CompressionLevelUltra       = 5 // Ultra-compressed (~4 words)
	CompressionLevelMax         = 5 // Maximum compression level
)

// Note: Level 0 (full text) removed - we fall back to episodes.content directly

// AddEpisodeSummary stores a summary for an episode at a given compression level
func (g *DB) AddEpisodeSummary(episodeID string, level int, summary string, tokens int) error {
	_, err := g.db.Exec(`
		INSERT OR REPLACE INTO episode_summaries (episode_id, compression_level, summary, tokens)
		VALUES (?, ?, ?, ?)
	`, episodeID, level, summary, tokens)
	return err
}

// GetEpisodeSummary retrieves a summary for an episode at a specific compression level
// Falls back to higher compression levels if the requested level doesn't exist
// Returns nil if no summary exists (caller should fall back to episodes.content)
func (g *DB) GetEpisodeSummary(episodeID string, level int) (*EpisodeSummary, error) {
	// Try requested level first, then higher levels
	for lvl := level; lvl <= CompressionLevelMax; lvl++ {
		var summary EpisodeSummary
		err := g.db.QueryRow(`
			SELECT id, episode_id, compression_level, summary, tokens
			FROM episode_summaries
			WHERE episode_id = ? AND compression_level = ?
		`, episodeID, lvl).Scan(
			&summary.ID,
			&summary.EpisodeID,
			&summary.CompressionLevel,
			&summary.Summary,
			&summary.Tokens,
		)
		if err == nil {
			return &summary, nil
		}
	}
	// No summary found - caller should use episodes.content
	return nil, nil
}

// CompressEpisode generates summaries at different compression levels
// Uses Ollama/Qwen2.5:7b for compression
type Compressor interface {
	Generate(prompt string) (string, error)
}

// GenerateEpisodeSummaries creates summaries at compression levels 1-5 for an episode
// Generates asynchronously - full text is stored in episodes table with token_count
func (g *DB) GenerateEpisodeSummaries(episode Episode, compressor Compressor) error {
	// Generate compressed versions asynchronously if content is long enough
	if compressor != nil && episode.TokenCount > 50 {
		go g.generateCompressedSummaries(episode, compressor, episode.TokenCount)
	}

	return nil
}

// generateCompressedSummaries creates level 1-5 summaries (async)
func (g *DB) generateCompressedSummaries(episode Episode, compressor Compressor, fullTokens int) {
	// Level 1: Key points (~50-100 words, if content > 100 tokens)
	if fullTokens > 100 {
		summary, err := compressToKeyPoints(episode, compressor)
		if err == nil {
			tokens := estimateTokens(summary)
			g.AddEpisodeSummary(episode.ID, CompressionLevelMedium, summary, tokens)
		}
	}

	// Level 2: Essential summary (~20-30 words, if content > 300 tokens)
	if fullTokens > 300 {
		summary, err := compressToEssence(episode, compressor)
		if err == nil {
			tokens := estimateTokens(summary)
			g.AddEpisodeSummary(episode.ID, CompressionLevelHigh, summary, tokens)
		}
	}

	// Level 3: Core facts (~16 words, if content > 500 tokens)
	if fullTokens > 500 {
		summary, err := compressToCore(episode, compressor)
		if err == nil {
			tokens := estimateTokens(summary)
			g.AddEpisodeSummary(episode.ID, CompressionLevelCore, summary, tokens)
		}
	}

	// Level 4: Minimal summary (~8 words, if content > 700 tokens)
	if fullTokens > 700 {
		summary, err := compressToMinimal(episode, compressor)
		if err == nil {
			tokens := estimateTokens(summary)
			g.AddEpisodeSummary(episode.ID, CompressionLevelMinimal, summary, tokens)
		}
	}

	// Level 5: Ultra-compressed (~4 words, if content > 1000 tokens)
	if fullTokens > 1000 {
		summary, err := compressToUltra(episode, compressor)
		if err == nil {
			tokens := estimateTokens(summary)
			g.AddEpisodeSummary(episode.ID, CompressionLevelUltra, summary, tokens)
		}
	}
}

// compressToKeyPoints creates a medium compression summary (level 1)
func compressToKeyPoints(episode Episode, compressor Compressor) (string, error) {
	prompt := buildCompressionPrompt(episode, "key points", `
Extract the key points from this message:
- Keep important facts, decisions, and insights
- Remove small talk, acknowledgments, and filler
- Preserve technical details and context
- Output 1-2 sentences
`)
	return compressor.Generate(prompt)
}

// compressToEssence creates a high compression summary (level 2)
func compressToEssence(episode Episode, compressor Compressor) (string, error) {
	prompt := buildCompressionPrompt(episode, "essence", `
Summarize the essential point of this message in 1 sentence (20-30 words):
- What is the core information?
- Strip everything except the minimal necessary context
`)
	return compressor.Generate(prompt)
}

// compressToCore creates a core facts summary (level 3)
func compressToCore(episode Episode, compressor Compressor) (string, error) {
	prompt := buildCompressionPrompt(episode, "core facts", `
Compress to core facts in exactly 16 words or less:
- Keep only the absolutely essential information
- Remove all context and qualifiers
- Focus on the main subject and action
`)
	return compressor.Generate(prompt)
}

// compressToMinimal creates a minimal summary (level 4)
func compressToMinimal(episode Episode, compressor Compressor) (string, error) {
	prompt := buildCompressionPrompt(episode, "minimal", `
Compress to exactly 8 words or less:
- Maximum compression while remaining intelligible
- Subject + verb + object only
- No extra words
`)
	return compressor.Generate(prompt)
}

// compressToUltra creates an ultra-compressed summary (level 5)
func compressToUltra(episode Episode, compressor Compressor) (string, error) {
	prompt := buildCompressionPrompt(episode, "ultra-compressed", `
Compress to exactly 4 words:
- Absolute minimum representation
- Key concepts only
- Telegram style
`)
	return compressor.Generate(prompt)
}

// buildCompressionPrompt constructs a prompt for episode compression
func buildCompressionPrompt(episode Episode, level, instruction string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are compressing a conversation message to %s.\n\n", level))
	sb.WriteString(instruction)
	sb.WriteString("\n\nMessage context:\n")
	sb.WriteString(fmt.Sprintf("- Author: %s\n", episode.Author))
	if episode.DialogueAct != "" {
		sb.WriteString(fmt.Sprintf("- Type: %s\n", episode.DialogueAct))
	}
	sb.WriteString("\nMessage:\n")
	sb.WriteString(episode.Content)
	sb.WriteString("\n\nSummary:")

	return sb.String()
}

// estimateTokens provides a rough token count estimate (4 chars ≈ 1 token)
func estimateTokens(text string) int {
	chars := utf8.RuneCountInString(text)
	return max(1, chars/4)
}

// GetEpisodeSummariesBatch retrieves summaries for multiple episodes at specified levels
// Returns a map of episode_id -> EpisodeSummary
func (g *DB) GetEpisodeSummariesBatch(episodeIDs []string, level int) (map[string]*EpisodeSummary, error) {
	if len(episodeIDs) == 0 {
		return make(map[string]*EpisodeSummary), nil
	}

	// Build query with placeholders
	placeholders := make([]string, len(episodeIDs))
	args := make([]interface{}, len(episodeIDs)+1)
	args[0] = level
	for i, id := range episodeIDs {
		placeholders[i] = "?"
		args[i+1] = id
	}

	query := fmt.Sprintf(`
		SELECT id, episode_id, compression_level, summary, tokens
		FROM episode_summaries
		WHERE compression_level = ? AND episode_id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := g.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]*EpisodeSummary)
	for rows.Next() {
		var summary EpisodeSummary
		if err := rows.Scan(&summary.ID, &summary.EpisodeID, &summary.CompressionLevel, &summary.Summary, &summary.Tokens); err != nil {
			continue
		}
		result[summary.EpisodeID] = &summary
	}

	return result, nil
}

// StoreEmbeddingJSON is a helper to serialize embeddings to JSON for storage
func StoreEmbeddingJSON(embedding []float64) ([]byte, error) {
	return json.Marshal(embedding)
}

// LoadEmbeddingJSON is a helper to deserialize embeddings from JSON
func LoadEmbeddingJSON(data []byte) ([]float64, error) {
	var embedding []float64
	err := json.Unmarshal(data, &embedding)
	return embedding, err
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
