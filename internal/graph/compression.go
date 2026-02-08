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

// CompressionLevel represents different tiers of compression
const (
	CompressionLevelMedium = 1 // Key points (~30% reduction)
	CompressionLevelHigh   = 2 // Essential summary (~70% reduction)
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
	for lvl := level; lvl <= CompressionLevelHigh; lvl++ {
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

// GenerateEpisodeSummaries creates summaries at compression levels 1-2 for an episode
// Generates asynchronously - full text is stored in episodes table with token_count
func (g *DB) GenerateEpisodeSummaries(episode Episode, compressor Compressor) error {
	// Generate compressed versions asynchronously if content is long enough
	if compressor != nil && episode.TokenCount > 50 {
		go g.generateCompressedSummaries(episode, compressor, episode.TokenCount)
	}

	return nil
}

// generateCompressedSummaries creates level 1 and 2 summaries (async)
func (g *DB) generateCompressedSummaries(episode Episode, compressor Compressor, fullTokens int) {
	// Level 1: Key points (if content > 100 tokens)
	if fullTokens > 100 {
		summary, err := compressToKeyPoints(episode, compressor)
		if err == nil {
			tokens := estimateTokens(summary)
			g.AddEpisodeSummary(episode.ID, CompressionLevelMedium, summary, tokens)
		}
	}

	// Level 2: Essential summary (if content > 300 tokens)
	if fullTokens > 300 {
		summary, err := compressToEssence(episode, compressor)
		if err == nil {
			tokens := estimateTokens(summary)
			g.AddEpisodeSummary(episode.ID, CompressionLevelHigh, summary, tokens)
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
Summarize the essential point of this message in 1 sentence:
- What is the core information?
- Strip everything except the minimal necessary context
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
