package filter

import (
	"regexp"
	"strings"
	"unicode"
)

const (
	// DefaultEntropyThreshold is the minimum quality score for full processing
	// Messages below this are buffer-only (no episode creation)
	DefaultEntropyThreshold = 0.35

	// Alpha controls the weight between entity novelty and semantic divergence
	Alpha = 0.5
)

// EntropyFilter determines message quality for memory decisions
type EntropyFilter struct {
	threshold float64
	embedder  Embedder

	// Track recent history for divergence calculation
	recentEmbeddings [][]float64
	maxHistory       int
}

// Embedder interface for embedding generation
type Embedder interface {
	Embed(text string) ([]float64, error)
}

// NewEntropyFilter creates a new entropy-based quality filter
func NewEntropyFilter(embedder Embedder) *EntropyFilter {
	return &EntropyFilter{
		threshold:        DefaultEntropyThreshold,
		embedder:         embedder,
		recentEmbeddings: make([][]float64, 0),
		maxHistory:       10,
	}
}

// SetThreshold changes the quality threshold
func (f *EntropyFilter) SetThreshold(threshold float64) {
	f.threshold = threshold
}

// Result contains the quality assessment of a message
type Result struct {
	Score           float64 // 0.0-1.0, higher = more novel/valuable
	PassesThreshold bool    // true if score >= threshold
	EntityNovelty   float64 // proportion of potential new entities
	SemanticDiv     float64 // how different from recent history
	Embedding       []float64
}

// Score computes the quality score for a message
func (f *EntropyFilter) Score(content string) (*Result, error) {
	result := &Result{}

	// Calculate entity novelty (proportion of potential named entities)
	result.EntityNovelty = computeEntityNovelty(content)

	// Get embedding for semantic divergence
	if f.embedder != nil {
		emb, err := f.embedder.Embed(content)
		if err == nil {
			result.Embedding = emb
			result.SemanticDiv = f.computeSemanticDivergence(emb)

			// Update history
			f.addToHistory(emb)
		}
	} else {
		// Fallback: use only entity novelty
		result.SemanticDiv = 0.5
	}

	// Combine scores: Score = α * EntityNovelty + (1-α) * SemanticDivergence
	result.Score = Alpha*result.EntityNovelty + (1-Alpha)*result.SemanticDiv
	result.PassesThreshold = result.Score >= f.threshold

	return result, nil
}

// ShouldCreateEpisode returns true if the message should create a memory episode
func (f *EntropyFilter) ShouldCreateEpisode(content string) (bool, float64, error) {
	result, err := f.Score(content)
	if err != nil {
		// On error, default to creating episode
		return true, 0.5, err
	}
	return result.PassesThreshold, result.Score, nil
}

// computeEntityNovelty estimates the proportion of potential named entities
func computeEntityNovelty(content string) float64 {
	words := strings.Fields(content)
	if len(words) == 0 {
		return 0
	}

	potentialEntities := 0
	for _, word := range words {
		if isPotentialEntity(word) {
			potentialEntities++
		}
	}

	return float64(potentialEntities) / float64(len(words))
}

// isPotentialEntity uses heuristics to detect potential named entities
func isPotentialEntity(word string) bool {
	// Remove punctuation for analysis
	word = strings.Trim(word, ".,!?;:'\"()[]{}@#")
	if len(word) == 0 {
		return false
	}

	// Check if capitalized (potential proper noun)
	firstRune := []rune(word)[0]
	if unicode.IsUpper(firstRune) {
		return true
	}

	// Check for common patterns
	patterns := []string{
		`^\d{1,2}:\d{2}`,      // time
		`^\d{1,2}/\d{1,2}`,    // date
		`^https?://`,          // URL
		`^@\w+`,               // mention
		`^#\w+`,               // hashtag
		`^\$\d+`,              // currency
	}

	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(pattern, word); matched {
			return true
		}
	}

	return false
}

// computeSemanticDivergence measures how different content is from recent history
func (f *EntropyFilter) computeSemanticDivergence(embedding []float64) float64 {
	if len(f.recentEmbeddings) == 0 {
		return 1.0 // First message is maximally novel
	}

	// Compute average similarity to recent history
	avgSimilarity := 0.0
	for _, histEmb := range f.recentEmbeddings {
		avgSimilarity += cosineSimilarity(embedding, histEmb)
	}
	avgSimilarity /= float64(len(f.recentEmbeddings))

	// Divergence is 1 - similarity
	return 1.0 - avgSimilarity
}

// addToHistory adds embedding to recent history (sliding window)
func (f *EntropyFilter) addToHistory(embedding []float64) {
	f.recentEmbeddings = append(f.recentEmbeddings, embedding)
	if len(f.recentEmbeddings) > f.maxHistory {
		f.recentEmbeddings = f.recentEmbeddings[1:]
	}
}

// ClearHistory resets the history (e.g., for a new conversation)
func (f *EntropyFilter) ClearHistory() {
	f.recentEmbeddings = make([][]float64, 0)
}

// cosineSimilarity computes similarity between two embeddings
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (sqrt(normA) * sqrt(normB))
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton's method
	z := x / 2
	for i := 0; i < 10; i++ {
		z = z - (z*z-x)/(2*z)
	}
	return z
}
