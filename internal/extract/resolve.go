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

	// 2. Try name-based fuzzy match for PERSON entities (e.g., "Sarah" matches "Sarah Chen")
	if extracted.Type == graph.EntityPerson {
		entity, matchType := r.findNameMatch(extracted.Name)
		if entity != nil {
			r.db.AddEntityAlias(entity.ID, extracted.Name)
			result.Entity = entity
			result.MatchedBy = matchType
			if config.IncrementSalience {
				r.db.IncrementEntitySalience(entity.ID, 0.1)
			}
			return result, nil
		}
	}

	// 3. Try fuzzy match by embedding (if embedder available)
	if r.embedder != nil && config.EmbeddingThreshold > 0 {
		embedding, err := r.embedder.Embed(extracted.Name)
		if err == nil && len(embedding) > 0 {
			entity, err := r.db.FindSimilarEntity(embedding, config.EmbeddingThreshold)
			if err == nil && entity != nil {
				// Note: Don't add alias here - embedding similarity doesn't imply identity
				// Aliases should only come from name-based matches (e.g., "Sarah" → "Sarah Chen")
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

// findNameMatch looks for existing person entities that might be the same person
// Uses substring matching: "Sarah" matches "Sarah Chen", "Sarah Chen" matches "Sarah"
func (r *Resolver) findNameMatch(name string) (*graph.Entity, string) {
	nameLower := strings.ToLower(name)
	nameParts := strings.Fields(nameLower)

	// Get all person entities
	entities, err := r.db.GetEntitiesByType(graph.EntityPerson, 100)
	if err != nil {
		return nil, ""
	}

	for _, entity := range entities {
		entityNameLower := strings.ToLower(entity.Name)
		entityParts := strings.Fields(entityNameLower)

		// Check if the new name is a substring of existing (e.g., "Sarah" in "Sarah Chen")
		if strings.Contains(entityNameLower, nameLower) {
			return entity, "name_substring"
		}

		// Check if existing name is a substring of new (e.g., "Sarah Chen" contains existing "Sarah")
		if strings.Contains(nameLower, entityNameLower) {
			// Update the entity's canonical name to the longer version
			entity.Name = name
			r.db.AddEntity(entity)
			return entity, "name_expanded"
		}

		// Check if first name matches (handles "Sarah" vs "Sarah Chen")
		if len(nameParts) > 0 && len(entityParts) > 0 && nameParts[0] == entityParts[0] {
			// Only match if one is clearly an expansion of the other
			if len(nameParts) == 1 && len(entityParts) > 1 {
				// "Sarah" matches "Sarah Chen" - use the fuller name
				return entity, "first_name"
			}
			if len(nameParts) > 1 && len(entityParts) == 1 {
				// "Sarah Chen" matches "Sarah" - update to fuller name
				entity.Name = name
				r.db.AddEntity(entity)
				return entity, "first_name_expanded"
			}
		}
	}

	return nil, ""
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
