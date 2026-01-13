package extract

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vthunder/bud2/internal/graph"
)

// Generator is the interface for LLM text generation
type Generator interface {
	Generate(prompt string) (string, error)
}

// DeepExtractor performs LLM-based entity extraction
type DeepExtractor struct {
	generator Generator
}

// NewDeepExtractor creates a new deep entity extractor
func NewDeepExtractor(generator Generator) *DeepExtractor {
	return &DeepExtractor{generator: generator}
}

const extractionPrompt = `Extract named entities from this text. Return a JSON array of objects with "name", "type", and "confidence" fields.

Types: person, project, concept, location, time, other

Text: "%s"

Rules:
- Only extract specific, named entities (not generic nouns)
- Person: names of people (not pronouns like "he", "she")
- Project: named projects, products, companies
- Concept: specific ideas, technologies, methodologies
- Location: specific places
- Time: specific dates, times, events
- Confidence: 0.0-1.0 based on how certain you are

Return ONLY a valid JSON array, no explanation.
Example: [{"name": "John", "type": "person", "confidence": 0.9}]

JSON:`

// Extract performs LLM-based entity extraction
func (e *DeepExtractor) Extract(text string) ([]ExtractedEntity, error) {
	if e.generator == nil {
		return nil, fmt.Errorf("no generator configured")
	}

	// Truncate very long text
	if len(text) > 2000 {
		text = text[:2000] + "..."
	}

	prompt := fmt.Sprintf(extractionPrompt, text)
	response, err := e.generator.Generate(prompt)
	if err != nil {
		return nil, fmt.Errorf("generation failed: %w", err)
	}

	// Parse JSON response
	response = strings.TrimSpace(response)

	// Handle common response formats
	if strings.HasPrefix(response, "```json") {
		response = strings.TrimPrefix(response, "```json")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	} else if strings.HasPrefix(response, "```") {
		response = strings.TrimPrefix(response, "```")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	}

	var rawEntities []struct {
		Name       string  `json:"name"`
		Type       string  `json:"type"`
		Confidence float64 `json:"confidence"`
	}

	if err := json.Unmarshal([]byte(response), &rawEntities); err != nil {
		// Try to find JSON array in response
		start := strings.Index(response, "[")
		end := strings.LastIndex(response, "]")
		if start >= 0 && end > start {
			response = response[start : end+1]
			if err := json.Unmarshal([]byte(response), &rawEntities); err != nil {
				return nil, fmt.Errorf("failed to parse response: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
	}

	// Convert to ExtractedEntity
	var entities []ExtractedEntity
	for _, raw := range rawEntities {
		if raw.Name == "" {
			continue
		}

		entityType := parseEntityType(raw.Type)
		confidence := raw.Confidence
		if confidence <= 0 {
			confidence = 0.7
		}

		entities = append(entities, ExtractedEntity{
			Name:       raw.Name,
			Type:       entityType,
			Confidence: confidence,
		})
	}

	return entities, nil
}

// parseEntityType converts a string to EntityType
func parseEntityType(s string) graph.EntityType {
	switch strings.ToLower(s) {
	case "person":
		return graph.EntityPerson
	case "project":
		return graph.EntityProject
	case "concept":
		return graph.EntityConcept
	case "location":
		return graph.EntityLocation
	case "time":
		return graph.EntityTime
	default:
		return graph.EntityOther
	}
}

// ExtractAndMerge extracts entities and merges with fast extraction results
func (e *DeepExtractor) ExtractAndMerge(text string, fastEntities []ExtractedEntity) ([]ExtractedEntity, error) {
	deepEntities, err := e.Extract(text)
	if err != nil {
		// Fall back to fast entities only
		return fastEntities, nil
	}

	// Merge: prefer deep extraction results, add unique fast results
	seen := make(map[string]bool)
	var merged []ExtractedEntity

	// Add deep entities first (higher confidence)
	for _, e := range deepEntities {
		key := strings.ToLower(e.Name)
		seen[key] = true
		merged = append(merged, e)
	}

	// Add unique fast entities
	for _, e := range fastEntities {
		key := strings.ToLower(e.Name)
		if !seen[key] {
			seen[key] = true
			merged = append(merged, e)
		}
	}

	return merged, nil
}
