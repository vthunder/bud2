package extract

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/vthunder/bud2/internal/graph"
)

var (
	// Regex patterns for post-processing
	emailRegex         = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	calendarIDRegex    = regexp.MustCompile(`@(?:group|resource)\.calendar\.google\.com$`)
	fileExtensionRegex = regexp.MustCompile(`\.[a-zA-Z0-9]{2,6}$`)
	hyphenatedIDRegex  = regexp.MustCompile(`^[a-z]+(-[a-z]+)+$`)
	underscoredIDRegex = regexp.MustCompile(`^[a-z]+(_[a-z]+)+$`)
	moneyRegex         = regexp.MustCompile(`\$[\d,]+(?:\.\d{2})?[kKmMbB]?|\d+(?:,\d{3})*(?:\.\d{2})?\s*(?:dollars?|USD|EUR|GBP)`)

	// Noise entities to filter out (system artifacts, pronouns, conversational fragments)
	noiseEntities = map[string]bool{
		// Pronouns (subject + object forms)
		"i": true, "me": true, "my": true, "you": true, "your": true,
		"he": true, "him": true, "his": true,
		"she": true, "her": true,
		"it": true, "its": true,
		"they": true, "them": true, "their": true,
		"we": true, "us": true, "our": true,
		"this": true, "that": true, "these": true, "those": true,
		"speaker": true, "user": true, "owner": true,
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

// Pass 1: Entity extraction only. Focused prompt for better accuracy.
const entityExtractionPrompt = `Extract named entities from this text.

ENTITY TYPES (use these exact labels):
- PERSON: Individual people by name (not pronouns like "he", "she", "my", "I")
- ORG: Companies, organizations, brands, stores (Google, Blue Bottle, Stanford, UCSF)
- GPE: Cities, countries, states (Portland, USA, California, Seattle)
- LOC: Streets, addresses, non-GPE locations (Market Street, Highway 101)
- FAC: Buildings, hospitals, airports (UCSF Medical Center, SFO)
- DATE: Dates, years, relative dates (2015, next Tuesday, March 15th, Q1)
- TIME: Times of day (2pm, 6am, noon)
- PRODUCT: Named consumer products only (iPhone, Tesla Model 3, Nike shoes). NOT software or tech tools.
- TECHNOLOGY: Software, frameworks, programming languages, AI models, developer tools (Go, SQLite, React, Ollama, Claude, spaCy, GitHub Actions, Slack, Things 3)
- MONEY: Monetary values ($50k, $1 million)
- EMAIL: Email addresses
- PET: Named pets (cat named Pixel, dog named Max)

IMPORTANT:
- Be conservative. Only extract entities you are confident about.
- PERSON is the most important type. Extract all names of people mentioned.
- Do NOT extract generic nouns, technical terms, or conversational fragments.
- When "my", "I", or "me" refers to the speaker, do NOT extract "speaker" as an entity.

TEXT: "%s"

Return ONLY a JSON array:
[{"name":"...","type":"...","confidence":0.9}]

EXAMPLE for "My friend Sarah works at Google in Portland":
[{"name":"Sarah","type":"PERSON","confidence":0.95},{"name":"Google","type":"ORG","confidence":0.99},{"name":"Portland","type":"GPE","confidence":0.95}]

JSON:`

// Pass 2: Relationship extraction given known entities. Only runs if entities were found.
const relationshipExtractionPrompt = `Given these ENTITIES extracted from the text, identify relationships between them.

ENTITIES:
%s

RELATIONSHIP TYPES (use these exact labels):
- affiliated_with: Professional connection (works at, works on, part of, studied at, cofounded)
- kin_of: Family relationship (married to, sibling, parent, child)
- knows: Social connection (friend, met, acquainted with)
- located_in: Physical location only (lives in a city, office based in Berlin). Do NOT use for abstract containment (e.g. "X is inside software Y" or "X is in a data store").
- has: Possession or attribute (owns, has email, has pet, prefers, allergic to)

When "my", "I", or "me" refers to the speaker, use "speaker" as the subject/object.

TEXT: "%s"

Return ONLY a JSON array:
[{"subject":"...","predicate":"...","object":"...","confidence":0.9}]

EXAMPLE for entities [Sarah, Google, Stanford] from "My friend Sarah works at Google. We met at Stanford.":
[{"subject":"Sarah","predicate":"knows","object":"speaker","confidence":0.9},{"subject":"Sarah","predicate":"affiliated_with","object":"Google","confidence":0.95},{"subject":"Sarah","predicate":"affiliated_with","object":"Stanford","confidence":0.8}]

If no relationships are found, return: []

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

// Extract performs LLM-based entity extraction (returns entities only)
func (e *DeepExtractor) Extract(text string) ([]ExtractedEntity, error) {
	result, err := e.ExtractEntities(text)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ExtractEntities performs pass 1: entity-only extraction
func (e *DeepExtractor) ExtractEntities(text string) ([]ExtractedEntity, error) {
	if e.generator == nil {
		return nil, fmt.Errorf("no generator configured")
	}

	if len(text) > 2000 {
		text = text[:2000] + "..."
	}

	prompt := fmt.Sprintf(entityExtractionPrompt, text)
	response, err := e.generator.Generate(prompt)
	if err != nil {
		return nil, fmt.Errorf("generation failed: %w", err)
	}

	entities := parseEntityJSON(response)

	// Post-process
	filtered := postProcessEntityList(text, entities)
	return filtered, nil
}

// relationshipExtractionWithContextPrompt injects known entity relationships into the prompt.
// %s = entity list, %s = known context block, %s = text
const relationshipExtractionWithContextPrompt = `Given these ENTITIES extracted from the text, identify relationships between them.

ENTITIES:
%s

KNOWN CONTEXT (existing knowledge about these entities — use this to improve relationship extraction):
%s

RELATIONSHIP TYPES (use these exact labels):
- affiliated_with: Professional connection (works at, works on, part of, studied at, cofounded)
- kin_of: Family relationship (married to, sibling, parent, child)
- knows: Social connection (friend, met, acquainted with)
- located_in: Physical location only (lives in a city, office based in Berlin). Do NOT use for abstract containment (e.g. "X is inside software Y" or "X is in a data store").
- has: Possession or attribute (owns, has email, has pet, prefers, allergic to)

When "my", "I", or "me" refers to the speaker, use "speaker" as the subject/object.

TEXT: "%s"

Return ONLY a JSON array:
[{"subject":"...","predicate":"...","object":"...","confidence":0.9}]

If no relationships are found, return: []

JSON:`

// ExtractRelationships performs pass 2: relationship extraction given known entities
func (e *DeepExtractor) ExtractRelationships(text string, entities []ExtractedEntity) ([]ExtractedRelationship, error) {
	if e.generator == nil || len(entities) == 0 {
		return nil, nil
	}

	if len(text) > 2000 {
		text = text[:2000] + "..."
	}

	// Build entity list for prompt
	var entityList strings.Builder
	for _, ent := range entities {
		fmt.Fprintf(&entityList, "- %s (%s)\n", ent.Name, ent.Type)
	}

	prompt := fmt.Sprintf(relationshipExtractionPrompt, entityList.String(), text)
	response, err := e.generator.Generate(prompt)
	if err != nil {
		return nil, fmt.Errorf("generation failed: %w", err)
	}

	rels := parseRelationshipJSON(response)
	return rels, nil
}

// ExtractRelationshipsWithContext performs relationship extraction with known entity context injected.
// entityContexts maps lowercase entity name -> EntityContext (fetched from graph before calling).
func (e *DeepExtractor) ExtractRelationshipsWithContext(text string, entities []ExtractedEntity, entityContexts map[string]*graph.EntityContext) ([]ExtractedRelationship, error) {
	if e.generator == nil || len(entities) == 0 {
		return nil, nil
	}

	// Fall back to plain extraction if no context available
	if len(entityContexts) == 0 {
		return e.ExtractRelationships(text, entities)
	}

	if len(text) > 2000 {
		text = text[:2000] + "..."
	}

	// Build entity list
	var entityList strings.Builder
	for _, ent := range entities {
		fmt.Fprintf(&entityList, "- %s (%s)\n", ent.Name, ent.Type)
	}

	// Build context block from known relationships
	var contextBlock strings.Builder
	contextFound := false
	for _, ent := range entities {
		ec, ok := entityContexts[strings.ToLower(ent.Name)]
		if !ok || ec == nil || len(ec.Relations) == 0 {
			continue
		}
		contextFound = true
		fmt.Fprintf(&contextBlock, "- %s (%s):", ent.Name, string(ec.Entity.Type))
		for _, r := range ec.Relations {
			if r.Direction == "outgoing" {
				fmt.Fprintf(&contextBlock, " %s %s (%s),", r.RelationType, r.OtherName, r.OtherType)
			} else {
				fmt.Fprintf(&contextBlock, " %s (incoming from %s %s),", r.RelationType, r.OtherName, r.OtherType)
			}
		}
		contextBlock.WriteString("\n")
	}

	if !contextFound {
		return e.ExtractRelationships(text, entities)
	}

	prompt := fmt.Sprintf(relationshipExtractionWithContextPrompt, entityList.String(), contextBlock.String(), text)
	response, err := e.generator.Generate(prompt)
	if err != nil {
		return nil, fmt.Errorf("generation failed: %w", err)
	}

	rels := parseRelationshipJSON(response)
	return rels, nil
}

// ExtractAll performs two-pass extraction: entities first, then relationships.
func (e *DeepExtractor) ExtractAll(text string) (*ExtractionResult, error) {
	result := &ExtractionResult{}

	// Pass 1: Extract entities
	entities, err := e.ExtractEntities(text)
	if err != nil {
		return nil, err
	}
	result.Entities = entities

	// Pass 2: Extract relationships (only if we found real entities AFTER filtering)
	// Filter out garbage entities (e.g., "none" with type OTHER from LLM when it finds nothing)
	realEntities := make([]ExtractedEntity, 0, len(entities))
	for _, ent := range entities {
		// Skip garbage entities
		if strings.ToLower(ent.Name) == "none" || ent.Type == graph.EntityOther {
			continue
		}
		realEntities = append(realEntities, ent)
	}

	// IMPORTANT: Skip Pass 2 entirely if we have zero useful entities
	// This prevents LLM hallucinations for technical/meta content
	if len(realEntities) > 0 {
		rels, err := e.ExtractRelationships(text, realEntities)
		if err != nil {
			// Non-fatal: return entities without relationships
			log.Printf("[extract] ⚠️  Relationship extraction failed: %v", err)
			return result, nil
		}
		if len(rels) > 0 {
			for _, rel := range rels {
				log.Printf("[extract]   %s -[%s]-> %s (%.2f)", rel.Subject, rel.Predicate, rel.Object, rel.Confidence)
			}
		}
		result.Relationships = rels
	}

	return result, nil
}

// ExtractAllWithContext performs two-pass extraction with known entity context injected into pass 2.
// entityContexts maps lowercase entity name -> EntityContext (fetched from graph before calling).
func (e *DeepExtractor) ExtractAllWithContext(text string, entityContexts map[string]*graph.EntityContext) (*ExtractionResult, error) {
	result := &ExtractionResult{}

	// Pass 1: Extract entities (no context needed for entity detection)
	entities, err := e.ExtractEntities(text)
	if err != nil {
		return nil, err
	}
	result.Entities = entities

	realEntities := make([]ExtractedEntity, 0, len(entities))
	for _, ent := range entities {
		if strings.ToLower(ent.Name) == "none" || ent.Type == graph.EntityOther {
			continue
		}
		realEntities = append(realEntities, ent)
	}

	if len(realEntities) > 0 {
		rels, err := e.ExtractRelationshipsWithContext(text, realEntities, entityContexts)
		if err != nil {
			log.Printf("[extract] ⚠️  Relationship extraction (with context) failed: %v", err)
			return result, nil
		}
		if len(rels) > 0 {
			for _, rel := range rels {
				log.Printf("[extract]   %s -[%s]-> %s (%.2f) [ctx]", rel.Subject, rel.Predicate, rel.Object, rel.Confidence)
			}
		}
		result.Relationships = rels
	}

	return result, nil
}

// parseEntityJSON parses entity JSON from LLM response (handles various formats)
func parseEntityJSON(response string) []ExtractedEntity {
	response = cleanJSONResponse(response)

	// Try array format first
	var rawEntities []struct {
		Name       string  `json:"name"`
		Type       string  `json:"type"`
		Confidence float64 `json:"confidence"`
	}

	// Find array
	arrayStart := strings.Index(response, "[")
	arrayEnd := strings.LastIndex(response, "]")
	if arrayStart >= 0 && arrayEnd > arrayStart {
		if err := json.Unmarshal([]byte(response[arrayStart:arrayEnd+1]), &rawEntities); err == nil {
			var entities []ExtractedEntity
			for _, raw := range rawEntities {
				if raw.Name == "" {
					continue
				}
				confidence := raw.Confidence
				if confidence <= 0 {
					confidence = 0.7
				}
				entities = append(entities, ExtractedEntity{
					Name:       raw.Name,
					Type:       parseEntityType(raw.Type),
					Confidence: confidence,
				})
			}
			return entities
		}
	}

	// Try object format (legacy: {entities: [...]})
	objectStart := strings.Index(response, "{")
	objectEnd := strings.LastIndex(response, "}")
	if objectStart >= 0 && objectEnd > objectStart {
		var obj struct {
			Entities []struct {
				Name       string  `json:"name"`
				Type       string  `json:"type"`
				Confidence float64 `json:"confidence"`
			} `json:"entities"`
		}
		if err := json.Unmarshal([]byte(response[objectStart:objectEnd+1]), &obj); err == nil {
			var entities []ExtractedEntity
			for _, raw := range obj.Entities {
				if raw.Name == "" {
					continue
				}
				confidence := raw.Confidence
				if confidence <= 0 {
					confidence = 0.7
				}
				entities = append(entities, ExtractedEntity{
					Name:       raw.Name,
					Type:       parseEntityType(raw.Type),
					Confidence: confidence,
				})
			}
			return entities
		}
	}

	return nil
}

// parseRelationshipJSON parses relationship JSON from LLM response
func parseRelationshipJSON(response string) []ExtractedRelationship {
	response = cleanJSONResponse(response)

	var rawRels []struct {
		Subject    string  `json:"subject"`
		Predicate  string  `json:"predicate"`
		Object     string  `json:"object"`
		Confidence float64 `json:"confidence"`
	}

	// Find array
	arrayStart := strings.Index(response, "[")
	arrayEnd := strings.LastIndex(response, "]")
	if arrayStart >= 0 && arrayEnd > arrayStart {
		if err := json.Unmarshal([]byte(response[arrayStart:arrayEnd+1]), &rawRels); err == nil {
			var rels []ExtractedRelationship
			for _, raw := range rawRels {
				if raw.Subject == "" || raw.Object == "" || raw.Predicate == "" {
					continue
				}
				confidence := raw.Confidence
				if confidence <= 0 {
					confidence = 0.7
				}
				rels = append(rels, ExtractedRelationship{
					Subject:    raw.Subject,
					Predicate:  raw.Predicate,
					Object:     raw.Object,
					Confidence: confidence,
				})
			}
			return rels
		}
	}

	return nil
}

// cleanJSONResponse strips markdown fences and whitespace from LLM responses
func cleanJSONResponse(response string) string {
	response = strings.TrimSpace(response)
	if strings.HasPrefix(response, "```json") {
		response = strings.TrimPrefix(response, "```json")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	} else if strings.HasPrefix(response, "```") {
		response = strings.TrimPrefix(response, "```")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	}
	return response
}

// postProcessEntityList filters noise and fixes types on an entity list
func postProcessEntityList(text string, entities []ExtractedEntity) []ExtractedEntity {
	seenNames := make(map[string]bool)
	var filtered []ExtractedEntity

	for _, e := range entities {
		nameLower := strings.ToLower(e.Name)

		// Filter noise entities
		if noiseEntities[nameLower] {
			continue
		}
		if e.Type == graph.EntityProduct && productNoise[nameLower] {
			continue
		}
		if len(e.Name) <= 2 {
			continue
		}

		// Detect email addresses first (before file extension check, since .com matches both)
		isEmail := emailRegex.MatchString(e.Name)

		// Filter calendar IDs (e.g., c_*@group.calendar.google.com)
		if isEmail && calendarIDRegex.MatchString(e.Name) {
			log.Printf("[extract] Filtered calendar ID: %s", e.Name)
			continue
		}

		// Filter technical artifacts (file names, hyphenated/underscored identifiers)
		// BUT: don't filter emails that happen to end in .com/.org/etc
		if !isEmail && fileExtensionRegex.MatchString(e.Name) {
			log.Printf("[extract] Filtered file name: %s (type: %s)", e.Name, e.Type)
			continue
		}
		if hyphenatedIDRegex.MatchString(e.Name) || underscoredIDRegex.MatchString(e.Name) {
			log.Printf("[extract] Filtered technical identifier: %s (type: %s)", e.Name, e.Type)
			continue
		}

		// Filter generic product names
		if e.Type == graph.EntityProduct && isGenericProductName(e.Name) {
			continue
		}

		// Fix misclassified types based on content
		if isEmail {
			e.Type = graph.EntityEmail
		} else if moneyRegex.MatchString(e.Name) {
			e.Type = graph.EntityMoney
		}

		if !seenNames[nameLower] {
			seenNames[nameLower] = true
			filtered = append(filtered, e)
		}
	}

	// Add emails found by regex that LLM missed (but skip calendar IDs)
	for _, match := range emailRegex.FindAllString(text, -1) {
		nameLower := strings.ToLower(match)

		// Skip calendar IDs
		if calendarIDRegex.MatchString(match) {
			continue
		}

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

	return filtered
}

// postProcessEntities wraps postProcessEntityList for ExtractionResult (used by legacy callers)
func postProcessEntities(text string, result *ExtractionResult) *ExtractionResult {
	result.Entities = postProcessEntityList(text, result.Entities)
	return result
}

// isGenericProductName returns true if the name looks like a generic description
// rather than a named product.
func isGenericProductName(name string) bool {
	lower := strings.ToLower(name)
	words := strings.Fields(lower)

	if len(words) == 1 {
		return false
	}

	genericTerms := []string{
		"system", "project", "session", "sessions", "service", "model",
		"memory", "wake", "wake-up", "prompt", "repo", "subscription",
		"reflex", "improvement", "evaluation", "check", "page", "doc",
		"interactive", "autonomous", "converter", "notes", "guild",
		"feature", "platform", "dashboard", "pipeline", "workflow",
		"integration", "module", "component", "endpoint", "interface",
	}
	for _, term := range genericTerms {
		if strings.Contains(lower, term) {
			return true
		}
	}

	return false
}

// PredicateToEdgeType converts a predicate string to an EdgeType.
// Supports both meta-relationship predicates and legacy specific predicates.
func PredicateToEdgeType(predicate string) graph.EdgeType {
	switch strings.ToLower(predicate) {
	// Meta-relationships (new)
	case "affiliated_with":
		return graph.EdgeAffiliatedWith
	case "kin_of":
		return graph.EdgeKinOf
	case "knows":
		return graph.EdgeKnows
	case "located_in":
		return graph.EdgeLocatedIn
	case "has":
		return graph.EdgeHas

	// Legacy predicates → map to meta-relationships
	case "works_at", "works_on", "part_of", "studied_at", "cofounder_of":
		return graph.EdgeAffiliatedWith
	case "married_to", "sibling_of", "parent_of", "child_of":
		return graph.EdgeKinOf
	case "friend_of", "met_at":
		return graph.EdgeKnows
	case "lives_in":
		return graph.EdgeLocatedIn
	case "owner_of", "has_email", "has_pet", "prefers", "allergic_to":
		return graph.EdgeHas

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
	case "WORK_OF_ART", "PROJECT":
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
	case "TECHNOLOGY", "TECH", "FRAMEWORK", "LIBRARY":
		return graph.EntityTechnology
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

	for _, e := range deepEntities {
		key := strings.ToLower(e.Name)
		seen[key] = true
		merged = append(merged, e)
	}

	for _, e := range fastEntities {
		key := strings.ToLower(e.Name)
		if !seen[key] {
			seen[key] = true
			merged = append(merged, e)
		}
	}

	return merged, nil
}
