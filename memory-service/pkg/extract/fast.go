package extract

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/vthunder/bud2/memory-service/pkg/graph"
)

// FastExtractor performs quick regex-based entity extraction
type FastExtractor struct {
	patterns    map[graph.EntityType][]*regexp.Regexp
	personSkip  map[string]bool // Words that shouldn't be extracted as persons
}

// NewFastExtractor creates a new fast entity extractor
func NewFastExtractor() *FastExtractor {
	e := &FastExtractor{
		patterns: make(map[graph.EntityType][]*regexp.Regexp),
		personSkip: map[string]bool{
			// Pronouns
			"me": true, "you": true, "him": true, "her": true, "them": true, "us": true,
			"it": true, "this": true, "that": true, "these": true, "those": true,
			// Common verbs that get incorrectly captured
			"call": true, "email": true, "text": true, "meet": true, "talk": true,
			"ask": true, "tell": true, "remind": true, "say": true, "test": true,
			"go": true, "get": true, "see": true, "do": true, "make": true, "take": true,
			// Articles/determiners
			"the": true, "a": true, "an": true, "some": true, "any": true,
			// Prepositions
			"to": true, "for": true, "about": true, "with": true, "from": true,
			// Other common words
			"new": true, "things": true, "something": true, "anything": true,
			// Companies/organizations (should not be typed as person)
			"microsoft": true, "google": true, "amazon": true, "apple": true, "meta": true,
			"facebook": true, "twitter": true, "github": true, "slack": true, "notion": true,
		},
	}

	// Person patterns
	e.patterns[graph.EntityPerson] = compilePatterns([]string{
		`@(\w+)`,                              // Discord mention
		`(?:my |the )?(?:friend|colleague|boss|manager|wife|husband|partner) (\w+)`,
		`(?:call|email|text|meet|talk to|ask|tell|remind) (\w+)`, // Action + person
		`(?:with|from|to) ([A-Z][a-z]+)(?:\s|$|,|\.)`,            // Preposition + capitalized name
	})

	// Date patterns (absolute or relative dates)
	e.patterns[graph.EntityDate] = compilePatterns([]string{
		`\b(\d{1,2}/\d{1,2}(?:/\d{2,4})?)\b`,                  // Date MM/DD
		`\b(\d{4}-\d{2}-\d{2})\b`,                              // ISO date
		`\b(today|tomorrow|yesterday|next week|last week)\b`,  // Relative time
		`\b(monday|tuesday|wednesday|thursday|friday|saturday|sunday)\b`,
	})

	// Time patterns (times smaller than a day)
	e.patterns[graph.EntityTime] = compilePatterns([]string{
		`\b(\d{1,2}:\d{2}(?:\s*[ap]m)?)\b`, // Time HH:MM
	})

	// GPE patterns (geopolitical entities - cities, countries, states)
	e.patterns[graph.EntityGPE] = compilePatterns([]string{
		`(?:at|in|to) (?:the )?(\w+ (?:office|building|room|cafe|restaurant|store))`,
	})

	return e
}

func compilePatterns(patterns []string) []*regexp.Regexp {
	result := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile("(?i)" + p)
		if err == nil {
			result = append(result, re)
		}
	}
	return result
}

// ExtractedEntity represents an entity found in text
type ExtractedEntity struct {
	Name       string
	Type       graph.EntityType
	StartPos   int
	EndPos     int
	Confidence float64
}

// Extract performs fast entity extraction on text
func (e *FastExtractor) Extract(text string) []ExtractedEntity {
	var entities []ExtractedEntity

	// Pattern-based extraction
	for entityType, patterns := range e.patterns {
		for _, re := range patterns {
			matches := re.FindAllStringSubmatchIndex(text, -1)
			for _, match := range matches {
				if len(match) >= 4 {
					// Get the captured group (not the full match)
					start, end := match[2], match[3]
					name := text[start:end]

					// Skip common words for person entities
					if entityType == graph.EntityPerson && e.personSkip[strings.ToLower(name)] {
						continue
					}

					entities = append(entities, ExtractedEntity{
						Name:       name,
						Type:       entityType,
						StartPos:   start,
						EndPos:     end,
						Confidence: 0.7,
					})
				}
			}
		}
	}

	// Capitalized word extraction (potential proper nouns)
	entities = append(entities, extractCapitalized(text)...)

	// Deduplicate
	entities = deduplicateEntities(entities)

	return entities
}

// extractCapitalized finds capitalized words that might be names
func extractCapitalized(text string) []ExtractedEntity {
	var entities []ExtractedEntity
	words := strings.Fields(text)

	// Common words to skip
	skipWords := map[string]bool{
		"I": true, "The": true, "A": true, "An": true, "This": true, "That": true,
		"It": true, "Is": true, "Are": true, "Was": true, "Were": true,
		"He": true, "She": true, "They": true, "We": true, "You": true,
		"My": true, "Your": true, "His": true, "Her": true, "Its": true,
		"What": true, "When": true, "Where": true, "Who": true, "Why": true, "How": true,
		"But": true, "And": true, "Or": true, "So": true, "If": true, "Then": true,
		"Yes": true, "No": true, "Ok": true, "Sure": true, "Thanks": true,
		"Hello": true, "Hi": true, "Hey": true, "Bye": true,
	}

	position := 0
	for i, word := range words {
		cleanWord := strings.Trim(word, ".,!?;:'\"()[]{}@#")

		// Skip if empty or common word
		if cleanWord == "" || skipWords[cleanWord] {
			position += len(word) + 1
			continue
		}

		// Check if capitalized (and not start of sentence, roughly)
		runes := []rune(cleanWord)
		if len(runes) > 1 && unicode.IsUpper(runes[0]) && unicode.IsLower(runes[1]) {
			// Skip if it's likely start of sentence (first word or after period)
			if i > 0 && !strings.HasSuffix(words[i-1], ".") && !strings.HasSuffix(words[i-1], "!") && !strings.HasSuffix(words[i-1], "?") {
				entities = append(entities, ExtractedEntity{
					Name:       cleanWord,
					Type:       graph.EntityOther, // Could be person, project, etc.
					StartPos:   position,
					EndPos:     position + len(cleanWord),
					Confidence: 0.5, // Lower confidence for heuristic
				})
			}
		}

		position += len(word) + 1
	}

	return entities
}

// deduplicateEntities removes duplicate entities, preferring specific types over EntityOther
func deduplicateEntities(entities []ExtractedEntity) []ExtractedEntity {
	// First pass: collect best entity for each name (prefer specific types over "other")
	bestByName := make(map[string]ExtractedEntity)

	for _, e := range entities {
		key := strings.ToLower(e.Name)
		existing, found := bestByName[key]
		if !found {
			bestByName[key] = e
		} else {
			// Prefer specific types over "other", and higher confidence
			if e.Type != graph.EntityOther && existing.Type == graph.EntityOther {
				bestByName[key] = e
			} else if e.Type == existing.Type && e.Confidence > existing.Confidence {
				bestByName[key] = e
			}
		}
	}

	// Convert map to slice
	result := make([]ExtractedEntity, 0, len(bestByName))
	for _, e := range bestByName {
		result = append(result, e)
	}

	return result
}

// ExtractSimple returns just the entity names as strings
func (e *FastExtractor) ExtractSimple(text string) []string {
	entities := e.Extract(text)
	names := make([]string, len(entities))
	for i, ent := range entities {
		names[i] = ent.Name
	}
	return names
}
