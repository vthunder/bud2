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

// CompressionLevel represents compression targets by max words
// Level number IS the target word count (L4=4 words, L8=8 words, etc.)
// Level 0 = verbatim/original content (no compression)
const (
	CompressionLevelVerbatim = 0  // Verbatim/original content (no compression)
	CompressionLevel4        = 4  // 4 words max
	CompressionLevel8        = 8  // 8 words max
	CompressionLevel16       = 16 // 16 words max
	CompressionLevel32       = 32 // 32 words max
	CompressionLevel64       = 64 // 64 words max
	CompressionLevelMax      = 64 // Maximum compression level
)

// AddEpisodeSummary stores a summary for an episode at a given compression level
func (g *DB) AddEpisodeSummary(episodeID string, level int, summary string, tokens int) error {
	_, err := g.db.Exec(`
		INSERT OR REPLACE INTO episode_summaries (episode_id, compression_level, summary, tokens)
		VALUES (?, ?, ?, ?)
	`, episodeID, level, summary, tokens)
	return err
}

// DeleteAllEpisodeSummaries removes all episode summaries from the database
func (g *DB) DeleteAllEpisodeSummaries() error {
	_, err := g.db.Exec(`DELETE FROM episode_summaries`)
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

// GenerateEpisodeSummaries creates summaries at all compression levels (L4-L64) for an episode
// Always generates all levels - uses verbatim text if episode already below target word count
func (g *DB) GenerateEpisodeSummaries(episode Episode, compressor Compressor) error {
	// Generate all compression levels asynchronously
	if compressor != nil {
		go g.generateCompressedSummaries(episode, compressor)
	}

	return nil
}

// generateCompressedSummaries creates all compression levels (L4-L64) for every episode
// Uses verbatim text if episode is already below target word count
func (g *DB) generateCompressedSummaries(episode Episode, compressor Compressor) {
	// Strip author prefix and calculate word count on clean content
	cleanContent := stripAuthorPrefix(episode.Content)
	wordCount := estimateWordCount(cleanContent)

	// Level 4: 4 words max
	summary, err := compressToTarget(episode, compressor, 4, wordCount)
	if err == nil {
		tokens := estimateTokens(summary)
		g.AddEpisodeSummary(episode.ID, CompressionLevel4, summary, tokens)
	}

	// Level 8: 8 words max
	summary, err = compressToTarget(episode, compressor, 8, wordCount)
	if err == nil {
		tokens := estimateTokens(summary)
		g.AddEpisodeSummary(episode.ID, CompressionLevel8, summary, tokens)
	}

	// Level 16: 16 words max
	summary, err = compressToTarget(episode, compressor, 16, wordCount)
	if err == nil {
		tokens := estimateTokens(summary)
		g.AddEpisodeSummary(episode.ID, CompressionLevel16, summary, tokens)
	}

	// Level 32: 32 words max
	summary, err = compressToTarget(episode, compressor, 32, wordCount)
	if err == nil {
		tokens := estimateTokens(summary)
		g.AddEpisodeSummary(episode.ID, CompressionLevel32, summary, tokens)
	}

	// Level 64: 64 words max
	summary, err = compressToTarget(episode, compressor, 64, wordCount)
	if err == nil {
		tokens := estimateTokens(summary)
		g.AddEpisodeSummary(episode.ID, CompressionLevel64, summary, tokens)
	}
}

// compressToTarget compresses episode to target word count or returns verbatim if already below target
func compressToTarget(episode Episode, compressor Compressor, targetWords int, currentWords int) (string, error) {
	// Strip author prefix from content (it's redundant metadata)
	cleanContent := stripAuthorPrefix(episode.Content)

	// Recalculate word count on cleaned content
	cleanWordCount := estimateWordCount(cleanContent)

	// If episode is already below target, use verbatim text
	if cleanWordCount <= targetWords {
		return cleanContent, nil
	}

	// Otherwise compress to target
	prompt := buildCompressionPrompt(episode.Author, cleanContent, targetWords)
	return compressor.Generate(prompt)
}

// stripAuthorPrefix removes "AuthorName:" prefix from beginning of content
// Example: "Bud: hello world" -> "hello world"
func stripAuthorPrefix(content string) string {
	// Look for pattern: "word(s): " at the start
	idx := strings.Index(content, ": ")
	if idx == -1 || idx > 50 { // Limit search to first 50 chars to avoid false matches
		return content
	}

	// Check if prefix looks like an author name (no whitespace before colon)
	prefix := content[:idx]
	if strings.ContainsAny(prefix, " \t\n") {
		return content
	}

	// Strip the prefix and return trimmed content
	return strings.TrimSpace(content[idx+2:])
}

// estimateWordCount provides rough word count estimate
func estimateWordCount(text string) int {
	// Simple word count: split on whitespace
	words := strings.Fields(text)
	return len(words)
}

// buildCompressionPrompt constructs a prompt for episode compression to target word count
func buildCompressionPrompt(author string, content string, targetWords int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Compress this message to EXACTLY %d words or fewer.\n\n", targetWords))
	sb.WriteString("STRICT RULES:\n")
	sb.WriteString(fmt.Sprintf("- Output MUST be %d words or less - NO EXCEPTIONS\n", targetWords))
	sb.WriteString("- Count every word carefully before responding\n")
	sb.WriteString("- Do NOT include author name in output (it's already metadata)\n")
	sb.WriteString("- Keep only the essential core meaning\n")
	sb.WriteString("- Remove ALL filler, small talk, and redundancy\n")
	sb.WriteString("- Preserve key facts and decisions only\n")

	sb.WriteString("\nMessage context:\n")
	sb.WriteString(fmt.Sprintf("- Author: %s (DO NOT include in output)\n", author))

	sb.WriteString("\nOriginal message:\n")
	sb.WriteString(content)
	sb.WriteString(fmt.Sprintf("\n\nCompressed version (%d words max):", targetWords))

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

// TraceSummary represents a compressed version of a trace
type TraceSummary struct {
	ID               int    `json:"id"`
	TraceID          string `json:"trace_id"`
	CompressionLevel int    `json:"compression_level"`
	Summary          string `json:"summary"`
	Tokens           int    `json:"tokens"`
}

// AddTraceSummary stores a summary for a trace at a given compression level
func (g *DB) AddTraceSummary(traceID string, level int, summary string, tokens int) error {
	_, err := g.db.Exec(`
		INSERT OR REPLACE INTO trace_summaries (trace_id, compression_level, summary, tokens)
		VALUES (?, ?, ?, ?)
	`, traceID, level, summary, tokens)
	return err
}

// DeleteAllTraceSummaries removes all trace summaries from the database
func (g *DB) DeleteAllTraceSummaries() error {
	_, err := g.db.Exec(`DELETE FROM trace_summaries`)
	return err
}

// GetTraceSummary retrieves a summary for a trace at a specific compression level
// Falls back to higher compression levels if the requested level doesn't exist
// Returns nil if no summary exists
func (g *DB) GetTraceSummary(traceID string, level int) (*TraceSummary, error) {
	// Try requested level first, then higher levels
	for lvl := level; lvl <= CompressionLevelMax; lvl++ {
		var summary TraceSummary
		err := g.db.QueryRow(`
			SELECT id, trace_id, compression_level, summary, tokens
			FROM trace_summaries
			WHERE trace_id = ? AND compression_level = ?
		`, traceID, lvl).Scan(
			&summary.ID,
			&summary.TraceID,
			&summary.CompressionLevel,
			&summary.Summary,
			&summary.Tokens,
		)
		if err == nil {
			return &summary, nil
		}
	}
	// No summary found - caller should use traces.summary
	return nil, nil
}

// GenerateTracePyramid creates cascading summaries (L64→L32→L16→L8→L4) for a trace
// from its source episodes. Uses cascading approach for consistency.
func (g *DB) GenerateTracePyramid(traceID string, sourceEpisodes []*Episode, compressor Compressor) error {
	if compressor == nil || len(sourceEpisodes) == 0 {
		return fmt.Errorf("compressor and source episodes required")
	}

	// Build context from source episodes
	var contextParts []string
	for _, ep := range sourceEpisodes {
		contextParts = append(contextParts, fmt.Sprintf("[%s] %s", ep.Author, ep.Content))
	}
	sourceContext := strings.Join(contextParts, "\n")
	wordCount := estimateWordCount(sourceContext)

	// Generate L64 first (highest detail) from source episodes
	l64Summary, err := compressTraceToTarget(sourceContext, compressor, 64, wordCount)
	if err != nil {
		return fmt.Errorf("L64 compression failed: %w", err)
	}
	if err := g.AddTraceSummary(traceID, CompressionLevel64, l64Summary, estimateTokens(l64Summary)); err != nil {
		return fmt.Errorf("failed to store L64 summary: %w", err)
	}

	// L32: cascade from L64
	l64Words := estimateWordCount(l64Summary)
	l32Summary, err := compressTraceToTarget(l64Summary, compressor, 32, l64Words)
	if err != nil {
		return fmt.Errorf("L32 compression failed: %w", err)
	}
	if err := g.AddTraceSummary(traceID, CompressionLevel32, l32Summary, estimateTokens(l32Summary)); err != nil {
		return fmt.Errorf("failed to store L32 summary: %w", err)
	}

	// L16: cascade from L32
	l32Words := estimateWordCount(l32Summary)
	l16Summary, err := compressTraceToTarget(l32Summary, compressor, 16, l32Words)
	if err != nil {
		return fmt.Errorf("L16 compression failed: %w", err)
	}
	if err := g.AddTraceSummary(traceID, CompressionLevel16, l16Summary, estimateTokens(l16Summary)); err != nil {
		return fmt.Errorf("failed to store L16 summary: %w", err)
	}

	// L8: cascade from L16
	l16Words := estimateWordCount(l16Summary)
	l8Summary, err := compressTraceToTarget(l16Summary, compressor, 8, l16Words)
	if err != nil {
		return fmt.Errorf("L8 compression failed: %w", err)
	}
	if err := g.AddTraceSummary(traceID, CompressionLevel8, l8Summary, estimateTokens(l8Summary)); err != nil {
		return fmt.Errorf("failed to store L8 summary: %w", err)
	}

	// L4: cascade from L8
	l8Words := estimateWordCount(l8Summary)
	l4Summary, err := compressTraceToTarget(l8Summary, compressor, 4, l8Words)
	if err != nil {
		return fmt.Errorf("L4 compression failed: %w", err)
	}
	if err := g.AddTraceSummary(traceID, CompressionLevel4, l4Summary, estimateTokens(l4Summary)); err != nil {
		return fmt.Errorf("failed to store L4 summary: %w", err)
	}

	return nil
}

// compressTraceToTarget compresses trace content to target word count or returns verbatim if already below target
func compressTraceToTarget(content string, compressor Compressor, targetWords int, currentWords int) (string, error) {
	// If content is already below target, use verbatim text
	if currentWords <= targetWords {
		return content, nil
	}

	// Otherwise compress to target
	prompt := buildTraceCompressionPrompt(content, targetWords)
	return compressor.Generate(prompt)
}

// buildTraceCompressionPrompt constructs a prompt for trace compression to target word count
func buildTraceCompressionPrompt(content string, targetWords int) string {
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
