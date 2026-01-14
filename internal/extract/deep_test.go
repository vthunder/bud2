package extract

import (
	"testing"

	"github.com/vthunder/bud2/internal/graph"
)

// MockGenerator returns canned responses for testing
type MockGenerator struct {
	response string
}

func (m *MockGenerator) Generate(prompt string) (string, error) {
	return m.response, nil
}

func TestExtractAll_NewFormat(t *testing.T) {
	// Sample response in new format with entities and relationships
	response := `{"entities":[{"name":"Sarah Chen","type":"PERSON","confidence":0.95},{"name":"Stripe","type":"ORG","confidence":0.99},{"name":"Stanford","type":"ORG","confidence":0.9},{"name":"2015","type":"DATE","confidence":0.99}],"relationships":[{"subject":"Sarah Chen","predicate":"works_at","object":"Stripe","confidence":0.95},{"subject":"Sarah Chen","predicate":"studied_at","object":"Stanford","confidence":0.8}]}`

	extractor := NewDeepExtractor(&MockGenerator{response: response})
	result, err := extractor.ExtractAll("My friend Sarah Chen is a software engineer at Stripe. We met at Stanford in 2015.")
	if err != nil {
		t.Fatalf("ExtractAll failed: %v", err)
	}

	// Check entities
	if len(result.Entities) != 4 {
		t.Errorf("Expected 4 entities, got %d", len(result.Entities))
	}

	// Verify entity types
	entityTypes := make(map[string]graph.EntityType)
	for _, e := range result.Entities {
		entityTypes[e.Name] = e.Type
	}

	if entityTypes["Sarah Chen"] != graph.EntityPerson {
		t.Errorf("Expected Sarah Chen to be PERSON, got %s", entityTypes["Sarah Chen"])
	}
	if entityTypes["Stripe"] != graph.EntityOrg {
		t.Errorf("Expected Stripe to be ORG, got %s", entityTypes["Stripe"])
	}
	if entityTypes["Stanford"] != graph.EntityOrg {
		t.Errorf("Expected Stanford to be ORG, got %s", entityTypes["Stanford"])
	}

	// Check relationships
	if len(result.Relationships) != 2 {
		t.Errorf("Expected 2 relationships, got %d", len(result.Relationships))
	}

	// Verify relationships
	hasWorksAt := false
	hasStudiedAt := false
	for _, r := range result.Relationships {
		if r.Subject == "Sarah Chen" && r.Predicate == "works_at" && r.Object == "Stripe" {
			hasWorksAt = true
		}
		if r.Subject == "Sarah Chen" && r.Predicate == "studied_at" && r.Object == "Stanford" {
			hasStudiedAt = true
		}
	}

	if !hasWorksAt {
		t.Error("Missing works_at relationship")
	}
	if !hasStudiedAt {
		t.Error("Missing studied_at relationship")
	}
}

func TestPredicateToEdgeType(t *testing.T) {
	tests := []struct {
		predicate string
		expected  graph.EdgeType
	}{
		{"works_at", graph.EdgeWorksAt},
		{"WORKS_AT", graph.EdgeWorksAt},
		{"lives_in", graph.EdgeLivesIn},
		{"married_to", graph.EdgeMarriedTo},
		{"sibling_of", graph.EdgeSiblingOf},
		{"friend_of", graph.EdgeFriendOf},
		{"works_on", graph.EdgeWorksOn},
		{"unknown_predicate", graph.EdgeRelatedTo},
	}

	for _, tt := range tests {
		result := PredicateToEdgeType(tt.predicate)
		if result != tt.expected {
			t.Errorf("PredicateToEdgeType(%q) = %s, want %s", tt.predicate, result, tt.expected)
		}
	}
}

func TestExtractAll_LegacyFormat(t *testing.T) {
	// Old format with just an array of entities
	response := `[{"name":"John","type":"PERSON","confidence":0.9}]`

	extractor := NewDeepExtractor(&MockGenerator{response: response})
	result, err := extractor.ExtractAll("Hello John")
	if err != nil {
		t.Fatalf("ExtractAll failed: %v", err)
	}

	if len(result.Entities) != 1 {
		t.Errorf("Expected 1 entity, got %d", len(result.Entities))
	}
	if result.Entities[0].Name != "John" {
		t.Errorf("Expected John, got %s", result.Entities[0].Name)
	}
	if len(result.Relationships) != 0 {
		t.Errorf("Expected 0 relationships, got %d", len(result.Relationships))
	}
}
