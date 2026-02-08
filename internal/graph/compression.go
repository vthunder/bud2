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

// CompressionLevel represents compression targets by max words (inverted pyramid)
// Level indicates target maximum word count - higher level = MORE compression
const (
	CompressionLevel64  = 1 // ~64 words max
	CompressionLevel32  = 2 // ~32 words max
	CompressionLevel16  = 3 // ~16 words max
	CompressionLevel8   = 4 // ~8 words max
	CompressionLevel4   = 5 // ~4 words max
	CompressionLevelMax = 5 // Maximum compression level
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

// GenerateEpisodeSummaries creates summaries at all compression levels (1-5) for an episode
// Always generates all levels - uses verbatim text if episode already below target word count
func (g *DB) GenerateEpisodeSummaries(episode Episode, compressor Compressor) error {
	// Generate all compression levels asynchronously
	if compressor != nil {
		go g.generateCompressedSummaries(episode, compressor)
	}

	return nil
}

// generateCompressedSummaries creates all compression levels (1-5) for every episode
// Uses verbatim text if episode is already below target word count
func (g *DB) generateCompressedSummaries(episode Episode, compressor Compressor) {
	wordCount := estimateWordCount(episode.Content)

	// Level 1: ~64 words max
	summary, err := compressToTarget(episode, compressor, 64, wordCount)
	if err == nil {
		tokens := estimateTokens(summary)
		g.AddEpisodeSummary(episode.ID, CompressionLevel64, summary, tokens)
	}

	// Level 2: ~32 words max
	summary, err = compressToTarget(episode, compressor, 32, wordCount)
	if err == nil {
		tokens := estimateTokens(summary)
		g.AddEpisodeSummary(episode.ID, CompressionLevel32, summary, tokens)
	}

	// Level 3: ~16 words max
	summary, err = compressToTarget(episode, compressor, 16, wordCount)
	if err == nil {
		tokens := estimateTokens(summary)
		g.AddEpisodeSummary(episode.ID, CompressionLevel16, summary, tokens)
	}

	// Level 4: ~8 words max
	summary, err = compressToTarget(episode, compressor, 8, wordCount)
	if err == nil {
		tokens := estimateTokens(summary)
		g.AddEpisodeSummary(episode.ID, CompressionLevel8, summary, tokens)
	}

	// Level 5: ~4 words max
	summary, err = compressToTarget(episode, compressor, 4, wordCount)
	if err == nil {
		tokens := estimateTokens(summary)
		g.AddEpisodeSummary(episode.ID, CompressionLevel4, summary, tokens)
	}
}

// compressToTarget compresses episode to target word count or returns verbatim if already below target
func compressToTarget(episode Episode, compressor Compressor, targetWords int, currentWords int) (string, error) {
	// If episode is already below target, use verbatim text
	if currentWords <= targetWords {
		return episode.Content, nil
	}

	// Otherwise compress to target
	prompt := buildCompressionPrompt(episode, targetWords)
	return compressor.Generate(prompt)
}

// estimateWordCount provides rough word count estimate
func estimateWordCount(text string) int {
	// Simple word count: split on whitespace
	words := strings.Fields(text)
	return len(words)
}

// buildCompressionPrompt constructs a prompt for episode compression to target word count
func buildCompressionPrompt(episode Episode, targetWords int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Compress this message to %d words or less.\n\n", targetWords))
	sb.WriteString("Rules:\n")
	sb.WriteString(fmt.Sprintf("- Maximum %d words\n", targetWords))
	sb.WriteString("- Keep the core meaning\n")
	sb.WriteString("- Remove filler, small talk, redundancy\n")
	sb.WriteString("- Preserve key facts and decisions\n")

	sb.WriteString("\nMessage context:\n")
	sb.WriteString(fmt.Sprintf("- Author: %s\n", episode.Author))
	if episode.DialogueAct != "" {
		sb.WriteString(fmt.Sprintf("- Type: %s\n", episode.DialogueAct))
	}

	sb.WriteString("\nOriginal message:\n")
	sb.WriteString(episode.Content)
	sb.WriteString("\n\nCompressed version:")

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
