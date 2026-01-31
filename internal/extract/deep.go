package extract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/vthunder/bud2/internal/graph"
)

var (
	// Regex patterns for post-processing
	emailRegex = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	moneyRegex = regexp.MustCompile(`\$[\d,]+(?:\.\d{2})?[kKmMbB]?|\d+(?:,\d{3})*(?:\.\d{2})?\s*(?:dollars?|USD|EUR|GBP)`)

	// Noise entities to filter out (system artifacts, pronouns, conversational fragments)
	noiseEntities = map[string]bool{
		// Pronouns
		"i": true, "me": true, "my": true, "you": true, "your": true,
		"he": true, "she": true, "it": true, "they": true, "we": true,
		"this": true, "that": true, "speaker": true, "user": true,
		// System/meta terms
		"memory_reset": true, "memory": true, "your memory": true,
		// Conversational fragments (often misclassified as PRODUCT)
		"ok": true, "okay": true, "yes": true, "no": true, "done": true,
		"create": true, "created": true, "redeployed": true, "restart": true,
		"try again": true, "the project": true, "the prompt": true,
		"context": true, "event": true, "backend": true, "tables": true,
		"repo": true, "diff": true, "push": true, "pull": true,
		// Single numbers/letters
		"1": true, "2": true, "3": true, "a": true, "b": true, "c": true,
		// Common exclamations
		"omg": true, "wow": true, "nice": true, "great": true, "good": true,
	}

	// Product-specific noise (short phrases that aren't real products)
	productNoise = map[string]bool{
		"option 2": true, "option a": true, "option b": true, "option 1": true,
		"ok commit": true, "ok let's try it!": true, "omg how annoying": true,
		"their hosted version": true, "the project": true, "the prompt": true,
		"try again": true, "update the page": true,
	}
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

const extractionPrompt = `Extract entities and relationships from this text.

ENTITY TYPES (use these exact labels):
- PERSON: Individual people by name (not pronouns like "he", "she", "my", "I")
- ORG: Companies, organizations, brands, coffee shops, stores (Google, Blue Bottle, Stanford, UCSF)
- GPE: Cities, countries, states (Portland, USA, California, Seattle)
- LOC: Streets, addresses, non-GPE locations (Market Street, Highway 101, the park)
- FAC: Buildings, hospitals, airports (UCSF Medical Center, SFO, the office)
- DATE: Dates, years, relative dates, deadlines (2015, next Tuesday, March 15th, Q1)
- TIME: Times of day (2pm, 6am, noon)
- PRODUCT: Named commercial products and vehicles only (iPhone, Tesla Model 3, Slack, Notion). NOT: generic tech terms, internal projects, code concepts, protocols, or tools being discussed in passing
- MONEY: Monetary values, budgets, prices ($50k, $1 million, 500 dollars)
- EMAIL: Email addresses (user@example.com, sarah.chen@company.com)
- PET: Pet names, animals (cats named Pixel, dog named Max)

RELATIONSHIP TYPES (subject → predicate → object):
- works_at: PERSON works at ORG
- lives_in: PERSON lives in GPE/LOC
- married_to: PERSON married to PERSON
- sibling_of: PERSON sibling of PERSON (sister, brother)
- parent_of: PERSON parent of PERSON
- child_of: PERSON child of PERSON
- friend_of: PERSON friend of PERSON
- works_on: PERSON works on PRODUCT/project
- located_in: ORG/FAC located in GPE/LOC
- part_of: ORG part of ORG (team is part of company)
- studied_at: PERSON studied at ORG
- met_at: PERSON met PERSON at LOC/ORG
- cofounder_of: PERSON is cofounder with PERSON
- owner_of: PERSON owns PRODUCT (my car, my house)
- has_email: PERSON has email EMAIL
- prefers: PERSON prefers PRODUCT/ORG/LOC (favorite coffee shop, preferred tool)
- allergic_to: PERSON is allergic to PRODUCT
- has_pet: PERSON has/owns PET

IMPORTANT: Be conservative. Only extract entities you are confident about (>0.8).
Do NOT extract:
- Generic technical terms or code concepts as PRODUCT (e.g., "memory system", "subprocess", "sessions")
- Internal project names as PRODUCT (use ORG for teams/projects if they represent an organization)
- Descriptions or phrases as entities (e.g., "autonomous wake-up" is not an entity)

When "my", "I", or "me" refers to the speaker, use "speaker" as the entity name.
For example: "My brother Marcus" → sibling_of(Marcus, speaker)
             "My car is a Tesla" → owner_of(speaker, Tesla Model 3)

TEXT: "%s"

Return ONLY valid JSON with this structure:
{"entities":[{"name":"...","type":"...","confidence":0.9}],"relationships":[{"subject":"...","predicate":"...","object":"...","confidence":0.9}]}

EXAMPLE for "My friend Sarah works at Google. We met at Stanford in 2015.":
{"entities":[{"name":"Sarah","type":"PERSON","confidence":0.95},{"name":"Google","type":"ORG","confidence":0.99},{"name":"Stanford","type":"ORG","confidence":0.95},{"name":"2015","type":"DATE","confidence":0.99}],"relationships":[{"subject":"Sarah","predicate":"friend_of","object":"speaker","confidence":0.9},{"subject":"Sarah","predicate":"works_at","object":"Google","confidence":0.95},{"subject":"Sarah","predicate":"met_at","object":"Stanford","confidence":0.85},{"subject":"speaker","predicate":"studied_at","object":"Stanford","confidence":0.7}]}

JSON:`

// ExtractedRelationship represents a relationship between entities
type ExtractedRelationship struct {
	Subject    string
	Predicate  string
	Object     string
	Confidence float64
}

// ExtractionResult contains both entities and relationships
type ExtractionResult struct {
	Entities      []ExtractedEntity
	Relationships []ExtractedRelationship
}

// Extract performs LLM-based entity extraction (legacy, returns entities only)
func (e *DeepExtractor) Extract(text string) ([]ExtractedEntity, error) {
	result, err := e.ExtractAll(text)
	if err != nil {
		return nil, err
	}
	return result.Entities, nil
}

// ExtractAll performs LLM-based entity and relationship extraction
func (e *DeepExtractor) ExtractAll(text string) (*ExtractionResult, error) {
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

	// Try to parse as new format (object with entities and relationships)
	var rawResult struct {
		Entities []struct {
			Name       string  `json:"name"`
			Type       string  `json:"type"`
			Confidence float64 `json:"confidence"`
		} `json:"entities"`
		Relationships []struct {
			Subject    string  `json:"subject"`
			Predicate  string  `json:"predicate"`
			Object     string  `json:"object"`
			Confidence float64 `json:"confidence"`
		} `json:"relationships"`
	}

	// Determine if response is array (legacy) or object (new format)
	arrayStart := strings.Index(response, "[")
	objectStart := strings.Index(response, "{")

	// If array comes before object (or no object), try legacy format first
	if arrayStart >= 0 && (objectStart < 0 || arrayStart < objectStart) {
		var rawEntities []struct {
			Name       string  `json:"name"`
			Type       string  `json:"type"`
			Confidence float64 `json:"confidence"`
		}
		end := strings.LastIndex(response, "]")
		if end > arrayStart {
			if err := json.Unmarshal([]byte(response[arrayStart:end+1]), &rawEntities); err == nil {
				rawResult.Entities = rawEntities
				goto parseComplete
			}
		}
	}

	// Try new object format
	if objectStart >= 0 {
		end := strings.LastIndex(response, "}")
		if end > objectStart {
			if err := json.Unmarshal([]byte(response[objectStart:end+1]), &rawResult); err != nil {
				return nil, fmt.Errorf("failed to parse response: %w", err)
			}
		} else {
			return nil, fmt.Errorf("no valid JSON found in response")
		}
	} else {
		return nil, fmt.Errorf("no valid JSON found in response")
	}

parseComplete:

	// Convert to result
	result := &ExtractionResult{}

	for _, raw := range rawResult.Entities {
		if raw.Name == "" {
			continue
		}

		entityType := parseEntityType(raw.Type)
		confidence := raw.Confidence
		if confidence <= 0 {
			confidence = 0.7
		}

		result.Entities = append(result.Entities, ExtractedEntity{
			Name:       raw.Name,
			Type:       entityType,
			Confidence: confidence,
		})
	}

	for _, raw := range rawResult.Relationships {
		if raw.Subject == "" || raw.Object == "" || raw.Predicate == "" {
			continue
		}

		confidence := raw.Confidence
		if confidence <= 0 {
			confidence = 0.7
		}

		result.Relationships = append(result.Relationships, ExtractedRelationship{
			Subject:    raw.Subject,
			Predicate:  raw.Predicate,
			Object:     raw.Object,
			Confidence: confidence,
		})
	}

	// Post-process to fix types and filter noise
	result = postProcessEntities(text, result)

	return result, nil
}

// postProcessEntities fixes entity types, filters noise, and adds regex-detected entities
func postProcessEntities(text string, result *ExtractionResult) *ExtractionResult {
	// Track entities by name for deduplication
	seenNames := make(map[string]bool)
	var filtered []ExtractedEntity

	for _, e := range result.Entities {
		nameLower := strings.ToLower(e.Name)

		// Skip noise entities
		if noiseEntities[nameLower] {
			continue
		}

		// Skip product-specific noise
		if e.Type == graph.EntityProduct && productNoise[nameLower] {
			continue
		}

		// Skip very short entities (likely noise)
		if len(e.Name) <= 2 {
			continue
		}

		// Skip PRODUCT entities that look like generic descriptions rather than named products
		if e.Type == graph.EntityProduct && isGenericProductName(e.Name) {
			continue
		}

		// Fix misclassified types based on content
		if emailRegex.MatchString(e.Name) {
			e.Type = graph.EntityEmail
		} else if moneyRegex.MatchString(e.Name) {
			e.Type = graph.EntityMoney
		}

		// Track seen names
		if !seenNames[nameLower] {
			seenNames[nameLower] = true
			filtered = append(filtered, e)
		}
	}

	// Add emails found by regex that LLM missed
	for _, match := range emailRegex.FindAllString(text, -1) {
		nameLower := strings.ToLower(match)
		if !seenNames[nameLower] {
			seenNames[nameLower] = true
			filtered = append(filtered, ExtractedEntity{
				Name:       match,
				Type:       graph.EntityEmail,
				Confidence: 0.95,
			})
		}
	}

	// Add money amounts found by regex that LLM missed
	for _, match := range moneyRegex.FindAllString(text, -1) {
		nameLower := strings.ToLower(match)
		if !seenNames[nameLower] {
			seenNames[nameLower] = true
			filtered = append(filtered, ExtractedEntity{
				Name:       match,
				Type:       graph.EntityMoney,
				Confidence: 0.95,
			})
		}
	}

	result.Entities = filtered
	return result
}

// isGenericProductName returns true if the name looks like a generic description
// rather than a named product. Generic descriptions contain common tech/project terms
// and are typically multi-word phrases.
func isGenericProductName(name string) bool {
	lower := strings.ToLower(name)
	words := strings.Fields(lower)

	// Single well-known product names are fine
	if len(words) == 1 {
		return false
	}

	// Multi-word names containing generic terms are likely descriptions, not products
	genericTerms := []string{
		"system", "project", "session", "sessions", "service", "model",
		"memory", "wake", "wake-up", "prompt", "repo", "subscription",
		"reflex", "improvement", "evaluation", "check", "page", "doc",
		"interactive", "autonomous", "converter", "notes", "guild",
	}
	for _, term := range genericTerms {
		if strings.Contains(lower, term) {
			return true
		}
	}

	return false
}

// PredicateToEdgeType converts a predicate string to an EdgeType
func PredicateToEdgeType(predicate string) graph.EdgeType {
	switch strings.ToLower(predicate) {
	case "works_at":
		return graph.EdgeWorksAt
	case "lives_in":
		return graph.EdgeLivesIn
	case "married_to":
		return graph.EdgeMarriedTo
	case "sibling_of":
		return graph.EdgeSiblingOf
	case "parent_of":
		return graph.EdgeParentOf
	case "child_of":
		return graph.EdgeChildOf
	case "friend_of":
		return graph.EdgeFriendOf
	case "works_on":
		return graph.EdgeWorksOn
	case "located_in":
		return graph.EdgeLocatedIn
	case "part_of":
		return graph.EdgePartOf
	case "studied_at":
		return graph.EdgeStudiedAt
	case "met_at":
		return graph.EdgeMetAt
	case "cofounder_of":
		return graph.EdgeCofounderOf
	case "owner_of":
		return graph.EdgeOwnerOf
	case "has_email":
		return graph.EdgeHasEmail
	case "prefers":
		return graph.EdgePrefers
	case "allergic_to":
		return graph.EdgeAllergicTo
	case "has_pet":
		return graph.EdgeHasPet
	default:
		return graph.EdgeRelatedTo
	}
}

// parseEntityType converts a string to EntityType (OntoNotes-compatible + extensions)
func parseEntityType(s string) graph.EntityType {
	switch strings.ToUpper(s) {
	case "PERSON":
		return graph.EntityPerson
	case "ORG", "ORGANIZATION":
		return graph.EntityOrg
	case "GPE":
		return graph.EntityGPE
	case "LOC", "LOCATION":
		return graph.EntityLoc
	case "FAC", "FACILITY":
		return graph.EntityFac
	case "PRODUCT":
		return graph.EntityProduct
	case "EVENT":
		return graph.EntityEvent
	case "WORK_OF_ART", "PROJECT": // Map legacy "project" to work_of_art
		return graph.EntityWorkOfArt
	case "LAW":
		return graph.EntityLaw
	case "LANGUAGE":
		return graph.EntityLanguage
	case "NORP":
		return graph.EntityNorp
	case "DATE":
		return graph.EntityDate
	case "TIME":
		return graph.EntityTime
	case "MONEY":
		return graph.EntityMoney
	case "PERCENT":
		return graph.EntityPercent
	case "QUANTITY":
		return graph.EntityQuantity
	case "CARDINAL":
		return graph.EntityCardinal
	case "ORDINAL":
		return graph.EntityOrdinal
	case "EMAIL":
		return graph.EntityEmail
	case "PET", "ANIMAL":
		return graph.EntityPet
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
