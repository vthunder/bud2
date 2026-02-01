package extract

import (
	"testing"

	"github.com/vthunder/bud2/internal/graph"
)

// MockGenerator returns canned responses for testing
type MockGenerator struct {
	response  string
	responses []string // multiple responses for multi-pass tests
	callCount int
}

func (m *MockGenerator) Generate(prompt string) (string, error) {
	if len(m.responses) > 0 && m.callCount < len(m.responses) {
		resp := m.responses[m.callCount]
		m.callCount++
		return resp, nil
	}
	return m.response, nil
}

func TestExtractEntities(t *testing.T) {
	response := `[{"name":"Sarah Chen","type":"PERSON","confidence":0.95},{"name":"Stripe","type":"ORG","confidence":0.99},{"name":"Portland","type":"GPE","confidence":0.95}]`

	extractor := NewDeepExtractor(&MockGenerator{response: response})
	entities, err := extractor.ExtractEntities("My friend Sarah Chen works at Stripe in Portland.")
	if err != nil {
		t.Fatalf("ExtractEntities failed: %v", err)
	}

	if len(entities) != 3 {
		t.Errorf("Expected 3 entities, got %d", len(entities))
	}

	entityTypes := make(map[string]graph.EntityType)
	for _, e := range entities {
		entityTypes[e.Name] = e.Type
	}

	if entityTypes["Sarah Chen"] != graph.EntityPerson {
		t.Errorf("Expected Sarah Chen to be PERSON, got %s", entityTypes["Sarah Chen"])
	}
	if entityTypes["Stripe"] != graph.EntityOrg {
		t.Errorf("Expected Stripe to be ORG, got %s", entityTypes["Stripe"])
	}
	if entityTypes["Portland"] != graph.EntityGPE {
		t.Errorf("Expected Portland to be GPE, got %s", entityTypes["Portland"])
	}
}

func TestExtractRelationships(t *testing.T) {
	response := `[{"subject":"Sarah","predicate":"affiliated_with","object":"Google","confidence":0.95},{"subject":"Sarah","predicate":"knows","object":"speaker","confidence":0.9}]`

	extractor := NewDeepExtractor(&MockGenerator{response: response})
	entities := []ExtractedEntity{
		{Name: "Sarah", Type: graph.EntityPerson},
		{Name: "Google", Type: graph.EntityOrg},
	}

	rels, err := extractor.ExtractRelationships("My friend Sarah works at Google.", entities)
	if err != nil {
		t.Fatalf("ExtractRelationships failed: %v", err)
	}

	if len(rels) != 2 {
		t.Fatalf("Expected 2 relationships, got %d", len(rels))
	}

	hasAffiliated := false
	hasKnows := false
	for _, r := range rels {
		if r.Subject == "Sarah" && r.Predicate == "affiliated_with" && r.Object == "Google" {
			hasAffiliated = true
		}
		if r.Subject == "Sarah" && r.Predicate == "knows" && r.Object == "speaker" {
			hasKnows = true
		}
	}

	if !hasAffiliated {
		t.Error("Missing affiliated_with relationship")
	}
	if !hasKnows {
		t.Error("Missing knows relationship")
	}
}

func TestExtractAll_TwoPass(t *testing.T) {
	mock := &MockGenerator{
		responses: []string{
			// Pass 1: entities
			`[{"name":"Sarah Chen","type":"PERSON","confidence":0.95},{"name":"Stripe","type":"ORG","confidence":0.99},{"name":"Stanford","type":"ORG","confidence":0.9},{"name":"2015","type":"DATE","confidence":0.99}]`,
			// Pass 2: relationships
			`[{"subject":"Sarah Chen","predicate":"affiliated_with","object":"Stripe","confidence":0.95},{"subject":"Sarah Chen","predicate":"affiliated_with","object":"Stanford","confidence":0.8}]`,
		},
	}

	extractor := NewDeepExtractor(mock)
	result, err := extractor.ExtractAll("My friend Sarah Chen is a software engineer at Stripe. We met at Stanford in 2015.")
	if err != nil {
		t.Fatalf("ExtractAll failed: %v", err)
	}

	if len(result.Entities) != 4 {
		t.Errorf("Expected 4 entities, got %d", len(result.Entities))
	}
	if len(result.Relationships) != 2 {
		t.Errorf("Expected 2 relationships, got %d", len(result.Relationships))
	}

	// Verify two LLM calls were made
	if mock.callCount != 2 {
		t.Errorf("Expected 2 LLM calls (two passes), got %d", mock.callCount)
	}
}

func TestPredicateToEdgeType(t *testing.T) {
	tests := []struct {
		predicate string
		expected  graph.EdgeType
	}{
		// Meta-relationships
		{"affiliated_with", graph.EdgeAffiliatedWith},
		{"kin_of", graph.EdgeKinOf},
		{"knows", graph.EdgeKnows},
		{"located_in", graph.EdgeLocatedIn},
		{"has", graph.EdgeHas},

		// Legacy predicates → meta-relationships
		{"works_at", graph.EdgeAffiliatedWith},
		{"works_on", graph.EdgeAffiliatedWith},
		{"studied_at", graph.EdgeAffiliatedWith},
		{"lives_in", graph.EdgeLocatedIn},
		{"married_to", graph.EdgeKinOf},
		{"sibling_of", graph.EdgeKinOf},
		{"friend_of", graph.EdgeKnows},
		{"owner_of", graph.EdgeHas},
		{"has_email", graph.EdgeHas},
		{"has_pet", graph.EdgeHas},
		{"prefers", graph.EdgeHas},

		// Unknown
		{"unknown_predicate", graph.EdgeRelatedTo},
	}

	for _, tt := range tests {
		result := PredicateToEdgeType(tt.predicate)
		if result != tt.expected {
			t.Errorf("PredicateToEdgeType(%q) = %s, want %s", tt.predicate, result, tt.expected)
		}
	}
}

func TestExtractEntities_LegacyObjectFormat(t *testing.T) {
	// Old format with {entities: [...]} wrapper
	response := `{"entities":[{"name":"John","type":"PERSON","confidence":0.9}]}`

	extractor := NewDeepExtractor(&MockGenerator{response: response})
	entities, err := extractor.ExtractEntities("Hello John")
	if err != nil {
		t.Fatalf("ExtractEntities failed: %v", err)
	}

	if len(entities) != 1 {
		t.Errorf("Expected 1 entity, got %d", len(entities))
	}
	if entities[0].Name != "John" {
		t.Errorf("Expected John, got %s", entities[0].Name)
	}
}

func TestExtractAll_NoEntities_SkipsPass2(t *testing.T) {
	mock := &MockGenerator{
		responses: []string{
			// Pass 1: no entities
			`[]`,
		},
	}

	extractor := NewDeepExtractor(mock)
	result, err := extractor.ExtractAll("Nothing interesting here.")
	if err != nil {
		t.Fatalf("ExtractAll failed: %v", err)
	}

	if len(result.Entities) != 0 {
		t.Errorf("Expected 0 entities, got %d", len(result.Entities))
	}
	if len(result.Relationships) != 0 {
		t.Errorf("Expected 0 relationships, got %d", len(result.Relationships))
	}

	// Should only have made 1 LLM call (skipped pass 2)
	if mock.callCount != 1 {
		t.Errorf("Expected 1 LLM call (skip pass 2 with no entities), got %d", mock.callCount)
	}
}
