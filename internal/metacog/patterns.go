package metacog

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// PatternDetector detects repeated executive patterns that could become reflexes
type PatternDetector struct {
	mu       sync.RWMutex
	patterns map[string]*Pattern

	// Configuration
	config PatternConfig
}

// PatternConfig holds detection configuration
type PatternConfig struct {
	MinRepetitions   int     // Minimum times a pattern must repeat (default 3)
	SuccessRateMin   float64 // Minimum success rate (default 1.0 = 100%)
	SimilarityMin    float64 // Minimum response similarity (default 0.9)
	MaxPatternAge    time.Duration // Age after which patterns are discarded (default 7 days)
}

// DefaultPatternConfig returns sensible defaults
func DefaultPatternConfig() PatternConfig {
	return PatternConfig{
		MinRepetitions: 3,
		SuccessRateMin: 1.0,
		SimilarityMin:  0.9,
		MaxPatternAge:  7 * 24 * time.Hour,
	}
}

// Pattern represents a detected input/response pattern
type Pattern struct {
	ID           string    `json:"id"`
	InputHash    string    `json:"input_hash"`    // Hash of normalized input
	InputExample string    `json:"input_example"` // Example input
	Response     string    `json:"response"`      // Consistent response
	Category     string    `json:"category"`      // greeting, gtd, calendar, etc.
	Occurrences  int       `json:"occurrences"`
	Successes    int       `json:"successes"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	IsProposed   bool      `json:"is_proposed"`    // Has been proposed as reflex
	IsRejected   bool      `json:"is_rejected"`    // User rejected this pattern
}

// SuccessRate returns the success rate of this pattern
func (p *Pattern) SuccessRate() float64 {
	if p.Occurrences == 0 {
		return 0
	}
	return float64(p.Successes) / float64(p.Occurrences)
}

// NewPatternDetector creates a new pattern detector
func NewPatternDetector(config PatternConfig) *PatternDetector {
	return &PatternDetector{
		patterns: make(map[string]*Pattern),
		config:   config,
	}
}

// Record records an input/response pair
// corrected indicates if the response was later corrected by the user
func (pd *PatternDetector) Record(input, response, category string, corrected bool) {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	// Normalize and hash input
	normalized := normalizeInput(input)
	hash := hashString(normalized)

	pattern, exists := pd.patterns[hash]
	if !exists {
		pattern = &Pattern{
			ID:           "pattern-" + hash[:12],
			InputHash:    hash,
			InputExample: input,
			Response:     response,
			Category:     category,
			FirstSeen:    time.Now(),
		}
		pd.patterns[hash] = pattern
	}

	pattern.Occurrences++
	pattern.LastSeen = time.Now()

	if !corrected {
		pattern.Successes++
	}

	// Update response if this is a successful response
	if !corrected && response != "" {
		pattern.Response = response
	}
}

// GetCandidates returns patterns that are candidates for reflex compilation
func (pd *PatternDetector) GetCandidates() []*Pattern {
	pd.mu.RLock()
	defer pd.mu.RUnlock()

	var candidates []*Pattern
	for _, p := range pd.patterns {
		// Skip already proposed or rejected
		if p.IsProposed || p.IsRejected {
			continue
		}

		// Check minimum repetitions
		if p.Occurrences < pd.config.MinRepetitions {
			continue
		}

		// Check success rate
		if p.SuccessRate() < pd.config.SuccessRateMin {
			continue
		}

		// Check pattern is not too old
		if time.Since(p.LastSeen) > pd.config.MaxPatternAge {
			continue
		}

		candidates = append(candidates, p)
	}

	return candidates
}

// MarkProposed marks a pattern as having been proposed
func (pd *PatternDetector) MarkProposed(patternID string) {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	for _, p := range pd.patterns {
		if p.ID == patternID {
			p.IsProposed = true
			return
		}
	}
}

// MarkRejected marks a pattern as rejected by the user
func (pd *PatternDetector) MarkRejected(patternID string) {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	for _, p := range pd.patterns {
		if p.ID == patternID {
			p.IsRejected = true
			return
		}
	}
}

// Prune removes old patterns
func (pd *PatternDetector) Prune() int {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	cutoff := time.Now().Add(-pd.config.MaxPatternAge)
	removed := 0

	for hash, p := range pd.patterns {
		if p.LastSeen.Before(cutoff) {
			delete(pd.patterns, hash)
			removed++
		}
	}

	return removed
}

// Stats returns statistics about patterns
func (pd *PatternDetector) Stats() map[string]int {
	pd.mu.RLock()
	defer pd.mu.RUnlock()

	stats := map[string]int{
		"total":     len(pd.patterns),
		"proposed":  0,
		"rejected":  0,
		"candidate": 0,
	}

	for _, p := range pd.patterns {
		if p.IsProposed {
			stats["proposed"]++
		}
		if p.IsRejected {
			stats["rejected"]++
		}
		if !p.IsProposed && !p.IsRejected &&
			p.Occurrences >= pd.config.MinRepetitions &&
			p.SuccessRate() >= pd.config.SuccessRateMin {
			stats["candidate"]++
		}
	}

	return stats
}

// normalizeInput normalizes input for comparison
func normalizeInput(input string) string {
	// Lowercase
	normalized := strings.ToLower(input)
	// Remove extra whitespace
	normalized = strings.Join(strings.Fields(normalized), " ")
	// Remove punctuation at end
	normalized = strings.TrimRight(normalized, "?!.")
	return normalized
}

// hashString creates a hash of a string
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
