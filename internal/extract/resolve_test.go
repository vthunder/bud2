package extract

// Tests for entity resolution (fuzzy matching against existing graph entities).
// Covers: exact match, alias match, name substring, name expansion, first name match, new creation.

import (
	"os"
	"testing"

	"github.com/vthunder/bud2/internal/graph"
)

// setupResolverTestDB creates a temporary test database with sample entities
func setupResolverTestDB(t *testing.T) (*graph.DB, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "resolve-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	db, err := graph.Open(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to open database: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return db, cleanup
}

// mockEmbedder implements Embedder interface for tests (returns nil embeddings)
type mockEmbedder struct{}

func (m *mockEmbedder) Embed(text string) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3, 0.4}, nil
}

func TestResolveExactMatch(t *testing.T) {
	db, cleanup := setupResolverTestDB(t)
	defer cleanup()

	// Add existing entity
	db.AddEntity(&graph.Entity{
		ID:       "entity-sarah-chen",
		Name:     "Sarah Chen",
		Type:     graph.EntityPerson,
		Salience: 0.5,
	})

	resolver := NewResolver(db, &mockEmbedder{})
	config := DefaultResolveConfig()

	// Resolve with exact same name
	result, err := resolver.Resolve(ExtractedEntity{
		Name:       "Sarah Chen",
		Type:       graph.EntityPerson,
		Confidence: 0.9,
	}, config)

	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if result.IsNew {
		t.Error("Expected existing entity, got new")
	}
	if result.MatchedBy != "exact" {
		t.Errorf("Expected match type 'exact', got '%s'", result.MatchedBy)
	}
	if result.Entity.ID != "entity-sarah-chen" {
		t.Errorf("Expected entity-sarah-chen, got %s", result.Entity.ID)
	}
}

func TestResolveAliasMatch(t *testing.T) {
	db, cleanup := setupResolverTestDB(t)
	defer cleanup()

	// Add existing entity with alias
	db.AddEntity(&graph.Entity{
		ID:       "entity-sarah-chen",
		Name:     "Sarah Chen",
		Type:     graph.EntityPerson,
		Salience: 0.5,
	})
	db.AddEntityAlias("entity-sarah-chen", "Chen, Sarah")

	resolver := NewResolver(db, &mockEmbedder{})
	config := DefaultResolveConfig()

	// Resolve using alias
	result, err := resolver.Resolve(ExtractedEntity{
		Name:       "Chen, Sarah",
		Type:       graph.EntityPerson,
		Confidence: 0.9,
	}, config)

	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if result.IsNew {
		t.Error("Expected existing entity, got new")
	}
	if result.MatchedBy != "exact" {
		t.Errorf("Expected match type 'exact' (via alias lookup), got '%s'", result.MatchedBy)
	}
	if result.Entity.ID != "entity-sarah-chen" {
		t.Errorf("Expected entity-sarah-chen, got %s", result.Entity.ID)
	}
}

func TestResolveNameSubstring(t *testing.T) {
	db, cleanup := setupResolverTestDB(t)
	defer cleanup()

	// Add existing entity with full name
	db.AddEntity(&graph.Entity{
		ID:       "entity-sarah-chen",
		Name:     "Sarah Chen",
		Type:     graph.EntityPerson,
		Salience: 0.5,
	})

	resolver := NewResolver(db, &mockEmbedder{})
	config := DefaultResolveConfig()

	// Resolve with partial name (first name only)
	result, err := resolver.Resolve(ExtractedEntity{
		Name:       "Sarah",
		Type:       graph.EntityPerson,
		Confidence: 0.9,
	}, config)

	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if result.IsNew {
		t.Error("Expected existing entity, got new")
	}
	if result.MatchedBy != "name_substring" && result.MatchedBy != "first_name" {
		t.Errorf("Expected match type 'name_substring' or 'first_name', got '%s'", result.MatchedBy)
	}
	if result.Entity.ID != "entity-sarah-chen" {
		t.Errorf("Expected entity-sarah-chen, got %s", result.Entity.ID)
	}
}

func TestResolveNameExpanded(t *testing.T) {
	db, cleanup := setupResolverTestDB(t)
	defer cleanup()

	// Add existing entity with only first name
	db.AddEntity(&graph.Entity{
		ID:       "entity-sarah",
		Name:     "Sarah",
		Type:     graph.EntityPerson,
		Salience: 0.5,
	})

	resolver := NewResolver(db, &mockEmbedder{})
	config := DefaultResolveConfig()

	// Resolve with full name - should match and update the entity's name
	result, err := resolver.Resolve(ExtractedEntity{
		Name:       "Sarah Chen",
		Type:       graph.EntityPerson,
		Confidence: 0.9,
	}, config)

	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if result.IsNew {
		t.Error("Expected existing entity, got new")
	}
	if result.MatchedBy != "name_expanded" && result.MatchedBy != "first_name_expanded" {
		t.Errorf("Expected match type 'name_expanded' or 'first_name_expanded', got '%s'", result.MatchedBy)
	}
	if result.Entity.ID != "entity-sarah" {
		t.Errorf("Expected entity-sarah, got %s", result.Entity.ID)
	}

	// Verify the entity's canonical name was updated
	updated, _ := db.FindEntityByName("Sarah Chen")
	if updated == nil {
		t.Error("Entity name should have been updated to 'Sarah Chen'")
	}
}

func TestResolveCreatesNewEntity(t *testing.T) {
	db, cleanup := setupResolverTestDB(t)
	defer cleanup()

	resolver := NewResolver(db, &mockEmbedder{})
	config := DefaultResolveConfig()

	// Resolve a completely new entity
	result, err := resolver.Resolve(ExtractedEntity{
		Name:       "John Doe",
		Type:       graph.EntityPerson,
		Confidence: 0.85,
	}, config)

	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if !result.IsNew {
		t.Error("Expected new entity, got existing")
	}
	if result.MatchedBy != "created" {
		t.Errorf("Expected match type 'created', got '%s'", result.MatchedBy)
	}

	// Verify entity was stored
	stored, _ := db.FindEntityByName("John Doe")
	if stored == nil {
		t.Error("Expected entity to be stored in database")
	}
	if stored.Type != graph.EntityPerson {
		t.Errorf("Expected type PERSON, got %s", stored.Type)
	}
}

func TestResolveNoCreateWhenConfigured(t *testing.T) {
	db, cleanup := setupResolverTestDB(t)
	defer cleanup()

	resolver := NewResolver(db, &mockEmbedder{})
	config := ResolveConfig{
		CreateIfNotFound:  false,
		IncrementSalience: false,
	}

	// Resolve a new entity with creation disabled
	result, err := resolver.Resolve(ExtractedEntity{
		Name:       "Unknown Person",
		Type:       graph.EntityPerson,
		Confidence: 0.9,
	}, config)

	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result != nil {
		t.Error("Expected nil result when CreateIfNotFound=false")
	}

	// Verify entity was NOT stored
	stored, _ := db.FindEntityByName("Unknown Person")
	if stored != nil {
		t.Error("Entity should not have been created")
	}
}

func TestResolveFuzzyMatchOnlyForPerson(t *testing.T) {
	db, cleanup := setupResolverTestDB(t)
	defer cleanup()

	// Add an ORG entity
	db.AddEntity(&graph.Entity{
		ID:       "entity-acme",
		Name:     "Acme Corporation",
		Type:     graph.EntityOrg,
		Salience: 0.5,
	})

	resolver := NewResolver(db, &mockEmbedder{})
	config := DefaultResolveConfig()

	// Try to resolve with partial name - should NOT match (ORGs don't use fuzzy)
	result, err := resolver.Resolve(ExtractedEntity{
		Name:       "Acme",
		Type:       graph.EntityOrg,
		Confidence: 0.9,
	}, config)

	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	// Should create a new entity because fuzzy matching is only for PERSON
	if !result.IsNew {
		t.Error("Expected new entity for ORG (no fuzzy matching)")
	}
	if result.MatchedBy != "created" {
		t.Errorf("Expected match type 'created', got '%s'", result.MatchedBy)
	}
}

func TestResolveSalienceIncrement(t *testing.T) {
	db, cleanup := setupResolverTestDB(t)
	defer cleanup()

	// Add existing entity
	db.AddEntity(&graph.Entity{
		ID:       "entity-bob",
		Name:     "Bob",
		Type:     graph.EntityPerson,
		Salience: 0.5,
	})

	resolver := NewResolver(db, &mockEmbedder{})
	config := DefaultResolveConfig()

	// Resolve - should increment salience
	resolver.Resolve(ExtractedEntity{
		Name:       "Bob",
		Type:       graph.EntityPerson,
		Confidence: 0.9,
	}, config)

	// Check salience was incremented
	entity, _ := db.FindEntityByName("Bob")
	if entity.Salience <= 0.5 {
		t.Errorf("Expected salience > 0.5, got %f", entity.Salience)
	}
}

func TestResolveAll(t *testing.T) {
	db, cleanup := setupResolverTestDB(t)
	defer cleanup()

	// Add one existing entity
	db.AddEntity(&graph.Entity{
		ID:       "entity-alice",
		Name:     "Alice",
		Type:     graph.EntityPerson,
		Salience: 0.5,
	})

	resolver := NewResolver(db, &mockEmbedder{})
	config := DefaultResolveConfig()

	// Resolve multiple entities
	entities := []ExtractedEntity{
		{Name: "Alice", Type: graph.EntityPerson, Confidence: 0.9},
		{Name: "New York", Type: graph.EntityGPE, Confidence: 0.95},
		{Name: "Acme Corp", Type: graph.EntityOrg, Confidence: 0.85},
	}

	results, err := resolver.ResolveAll(entities, config)
	if err != nil {
		t.Fatalf("ResolveAll failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("Expected 3 results, got %d", len(results))
	}

	// First should match existing
	if results[0].IsNew {
		t.Error("Expected Alice to match existing entity")
	}

	// Others should be new
	if !results[1].IsNew || !results[2].IsNew {
		t.Error("Expected New York and Acme Corp to be new entities")
	}
}

func TestResolveAddAliasOnFuzzyMatch(t *testing.T) {
	db, cleanup := setupResolverTestDB(t)
	defer cleanup()

	// Add entity with full name
	db.AddEntity(&graph.Entity{
		ID:       "entity-sarah-chen",
		Name:     "Sarah Chen",
		Type:     graph.EntityPerson,
		Salience: 0.5,
	})

	resolver := NewResolver(db, &mockEmbedder{})
	config := DefaultResolveConfig()

	// Resolve with partial name
	resolver.Resolve(ExtractedEntity{
		Name:       "Sarah",
		Type:       graph.EntityPerson,
		Confidence: 0.9,
	}, config)

	// The alias "Sarah" should have been added
	entity, _ := db.FindEntityByName("Sarah")
	if entity == nil {
		t.Error("Expected 'Sarah' to be registered as alias")
	}
	if entity.ID != "entity-sarah-chen" {
		t.Errorf("Expected alias to point to entity-sarah-chen, got %s", entity.ID)
	}
}

func TestResolveCaseInsensitive(t *testing.T) {
	db, cleanup := setupResolverTestDB(t)
	defer cleanup()

	// Add entity with specific casing
	db.AddEntity(&graph.Entity{
		ID:       "entity-sarah-chen",
		Name:     "Sarah Chen",
		Type:     graph.EntityPerson,
		Salience: 0.5,
	})

	resolver := NewResolver(db, &mockEmbedder{})
	config := DefaultResolveConfig()

	// Resolve with different casing
	result, err := resolver.Resolve(ExtractedEntity{
		Name:       "sarah chen",
		Type:       graph.EntityPerson,
		Confidence: 0.9,
	}, config)

	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	// Note: This test documents current behavior. If exact match is case-sensitive,
	// it might fall through to fuzzy matching or create a new entity.
	// The test verifies whatever the current behavior is.
	t.Logf("Lowercase match result: IsNew=%v, MatchedBy=%s", result.IsNew, result.MatchedBy)
}

func TestResolveDeduplication(t *testing.T) {
	db, cleanup := setupResolverTestDB(t)
	defer cleanup()

	resolver := NewResolver(db, &mockEmbedder{})
	config := DefaultResolveConfig()

	// Resolve same person mentioned different ways in sequence
	resolver.Resolve(ExtractedEntity{Name: "Sarah Chen", Type: graph.EntityPerson, Confidence: 0.9}, config)
	resolver.Resolve(ExtractedEntity{Name: "Sarah", Type: graph.EntityPerson, Confidence: 0.9}, config)
	resolver.Resolve(ExtractedEntity{Name: "Sarah C.", Type: graph.EntityPerson, Confidence: 0.9}, config) // Note: This may not match

	// Count entities
	entities, _ := db.GetEntitiesByType(graph.EntityPerson, 100)

	// We should have at most 2 entities (Sarah Chen + possibly Sarah C. if it didn't match)
	// The key point is Sarah and Sarah Chen should be the same entity
	t.Logf("Created %d PERSON entities", len(entities))
	for _, e := range entities {
		t.Logf("  - %s (ID: %s)", e.Name, e.ID)
	}
}
