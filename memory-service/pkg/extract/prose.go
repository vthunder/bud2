package extract

import (
	"strings"

	"github.com/tsawler/prose/v3"
	"github.com/vthunder/bud2/memory-service/pkg/graph"
)

// ProseExtractor uses the prose NLP library for entity extraction
type ProseExtractor struct{}

// NewProseExtractor creates a new prose-based entity extractor
func NewProseExtractor() *ProseExtractor {
	return &ProseExtractor{}
}

// proseToEntityType maps prose labels to our EntityType
func proseToEntityType(label string) graph.EntityType {
	switch strings.ToUpper(label) {
	case "PERSON":
		return graph.EntityPerson
	case "ORG":
		return graph.EntityOrg
	case "GPE":
		return graph.EntityGPE
	case "LOC":
		return graph.EntityLoc
	case "FAC":
		return graph.EntityFac
	case "PRODUCT":
		return graph.EntityProduct
	case "EVENT":
		return graph.EntityEvent
	case "WORK_OF_ART":
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
	default:
		return graph.EntityOther
	}
}

// Extract performs entity extraction using prose NLP
func (e *ProseExtractor) Extract(text string) []ExtractedEntity {
	doc, err := prose.NewDocument(text)
	if err != nil {
		return nil
	}

	var entities []ExtractedEntity
	for _, ent := range doc.Entities() {
		entities = append(entities, ExtractedEntity{
			Name:       ent.Text,
			Type:       proseToEntityType(ent.Label),
			StartPos:   ent.Start,
			EndPos:     ent.End,
			Confidence: ent.Confidence,
		})
	}

	return entities
}

// ExtractSimple returns just the entity names as strings
func (e *ProseExtractor) ExtractSimple(text string) []string {
	entities := e.Extract(text)
	names := make([]string, len(entities))
	for i, ent := range entities {
		names[i] = ent.Name
	}
	return names
}
