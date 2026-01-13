package extract

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/vthunder/bud2/internal/graph"
)

// Embedder is the interface for embedding generation
type Embedder interface {
	Embed(text string) ([]float64, error)
}

// Resolver handles entity resolution against the graph
type Resolver struct {
	db       *graph.DB
	embedder Embedder
}

// NewResolver creates a new entity resolver
func NewResolver(db *graph.DB, embedder Embedder) *Resolver {
	return &Resolver{
		db:       db,
		embedder: embedder,
	}
}

// ResolveConfig contains configuration for entity resolution
type ResolveConfig struct {
	EmbeddingThreshold float64 // Similarity threshold for fuzzy matching (default 0.85)
	CreateIfNotFound   bool    // Create new entity if not found
	IncrementSalience  bool    // Increment salience when entity is found
}

// DefaultResolveConfig returns default resolution configuration
func DefaultResolveConfig() ResolveConfig {
	return ResolveConfig{
		EmbeddingThreshold: 0.85,
		CreateIfNotFound:   true,
		IncrementSalience:  true,
	}
}

// ResolveResult contains the result of entity resolution
type ResolveResult struct {
	Entity   *graph.Entity
	IsNew    bool
	MatchedBy string // "exact", "alias", "embedding", or "created"
}

// Resolve attempts to match an extracted entity against the graph
func (r *Resolver) Resolve(extracted ExtractedEntity, config ResolveConfig) (*ResolveResult, error) {
	result := &ResolveResult{}

	// 1. Try exact match on canonical name
	entity, err := r.db.FindEntityByName(extracted.Name)
	if err == nil && entity != nil {
		result.Entity = entity
		result.MatchedBy = "exact"
		if config.IncrementSalience {
			r.db.IncrementEntitySalience(entity.ID, 0.1)
		}
		return result, nil
	}

	// 2. Try fuzzy match by embedding (if embedder available)
	if r.embedder != nil && config.EmbeddingThreshold > 0 {
		embedding, err := r.embedder.Embed(extracted.Name)
		if err == nil && len(embedding) > 0 {
			entity, err := r.db.FindSimilarEntity(embedding, config.EmbeddingThreshold)
			if err == nil && entity != nil {
				// Add as alias for future matches
				r.db.AddEntityAlias(entity.ID, extracted.Name)
				result.Entity = entity
				result.MatchedBy = "embedding"
				if config.IncrementSalience {
					r.db.IncrementEntitySalience(entity.ID, 0.1)
				}
				return result, nil
			}
		}
	}

	// 3. Create new entity if configured
	if config.CreateIfNotFound {
		newEntity := &graph.Entity{
			ID:       generateEntityID(extracted.Name),
			Name:     extracted.Name,
			Type:     extracted.Type,
			Salience: extracted.Confidence,
		}

		// Generate embedding if possible
		if r.embedder != nil {
			embedding, err := r.embedder.Embed(extracted.Name)
			if err == nil {
				newEntity.Embedding = embedding
			}
		}

		if err := r.db.AddEntity(newEntity); err != nil {
			return nil, err
		}

		result.Entity = newEntity
		result.IsNew = true
		result.MatchedBy = "created"
		return result, nil
	}

	// Not found and not creating
	return nil, nil
}

// ResolveAll resolves a list of extracted entities
func (r *Resolver) ResolveAll(entities []ExtractedEntity, config ResolveConfig) ([]*ResolveResult, error) {
	var results []*ResolveResult

	for _, extracted := range entities {
		result, err := r.Resolve(extracted, config)
		if err != nil {
			continue
		}
		if result != nil {
			results = append(results, result)
		}
	}

	return results, nil
}

// generateEntityID creates a unique ID for an entity
func generateEntityID(name string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(name)))
	return "entity-" + hex.EncodeToString(hash[:8])
}

// ProcessTextForEntities is a convenience function that extracts and resolves entities
func (r *Resolver) ProcessTextForEntities(text string, fast *FastExtractor, deep *DeepExtractor) ([]*graph.Entity, error) {
	// Fast extraction
	fastEntities := fast.Extract(text)

	// Deep extraction (if available)
	var allEntities []ExtractedEntity
	if deep != nil {
		merged, err := deep.ExtractAndMerge(text, fastEntities)
		if err == nil {
			allEntities = merged
		} else {
			allEntities = fastEntities
		}
	} else {
		allEntities = fastEntities
	}

	// Resolve all entities
	config := DefaultResolveConfig()
	results, err := r.ResolveAll(allEntities, config)
	if err != nil {
		return nil, err
	}

	// Extract graph entities
	var entities []*graph.Entity
	for _, result := range results {
		if result.Entity != nil {
			entities = append(entities, result.Entity)
		}
	}

	return entities, nil
}
