package metacog

import (
	"testing"
	"time"
)

// TestPatternDetection tests basic pattern detection
func TestPatternDetection(t *testing.T) {
	config := DefaultPatternConfig()
	config.MinRepetitions = 3
	detector := NewPatternDetector(config)

	// Record the same pattern 3 times (minimum for candidate)
	for i := 0; i < 3; i++ {
		detector.Record("show today", "Here are today's tasks...", "gtd", false)
	}

	// Should be a candidate now
	candidates := detector.GetCandidates()
	if len(candidates) != 1 {
		t.Fatalf("Expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Occurrences != 3 {
		t.Errorf("Expected 3 occurrences, got %d", candidates[0].Occurrences)
	}

	if candidates[0].InputExample != "show today" {
		t.Errorf("Expected input example 'show today', got %q", candidates[0].InputExample)
	}
}

// TestPatternNotEnoughRepetitions tests that patterns need minimum repetitions
func TestPatternNotEnoughRepetitions(t *testing.T) {
	config := DefaultPatternConfig()
	config.MinRepetitions = 5
	detector := NewPatternDetector(config)

	// Record pattern only 3 times (not enough for 5-min requirement)
	for i := 0; i < 3; i++ {
		detector.Record("show inbox", "Here's your inbox...", "gtd", false)
	}

	// Should NOT be a candidate
	candidates := detector.GetCandidates()
	if len(candidates) != 0 {
		t.Error("Expected no candidates with insufficient repetitions")
	}
}

// TestPatternCorrections tests that corrections reduce success rate
func TestPatternCorrections(t *testing.T) {
	config := DefaultPatternConfig()
	config.MinRepetitions = 3
	config.SuccessRateMin = 0.9 // Require 90% success
	detector := NewPatternDetector(config)

	// Record pattern 5 times, but 2 were corrected
	detector.Record("show calendar", "Events...", "calendar", false)
	detector.Record("show calendar", "Events...", "calendar", false)
	detector.Record("show calendar", "Events...", "calendar", true) // Corrected!
	detector.Record("show calendar", "Events...", "calendar", false)
	detector.Record("show calendar", "Events...", "calendar", true) // Corrected!

	// Success rate is 3/5 = 60%, below 90% threshold
	candidates := detector.GetCandidates()
	if len(candidates) != 0 {
		t.Error("Expected no candidates due to low success rate")
	}

	// Verify stats
	stats := detector.Stats()
	if stats["candidate"] != 0 {
		t.Errorf("Expected 0 candidates in stats, got %d", stats["candidate"])
	}
}

// TestPatternNormalization tests that inputs are normalized
func TestPatternNormalization(t *testing.T) {
	config := DefaultPatternConfig()
	config.MinRepetitions = 3
	detector := NewPatternDetector(config)

	// These should all normalize to the same pattern
	detector.Record("show today", "Tasks...", "gtd", false)
	detector.Record("SHOW TODAY", "Tasks...", "gtd", false)     // Uppercase
	detector.Record("show today?", "Tasks...", "gtd", false)    // With punctuation
	detector.Record("  show  today  ", "Tasks...", "gtd", false) // Extra spaces

	// Should all be one pattern
	stats := detector.Stats()
	if stats["total"] != 1 {
		t.Errorf("Expected 1 pattern (normalized), got %d", stats["total"])
	}

	candidates := detector.GetCandidates()
	if len(candidates) != 1 {
		t.Fatalf("Expected 1 candidate, got %d", len(candidates))
	}

	// Occurrences should be 4
	if candidates[0].Occurrences != 4 {
		t.Errorf("Expected 4 occurrences, got %d", candidates[0].Occurrences)
	}
}

// TestMarkProposed tests marking patterns as proposed
func TestMarkProposed(t *testing.T) {
	config := DefaultPatternConfig()
	config.MinRepetitions = 2
	detector := NewPatternDetector(config)

	// Create a candidate pattern
	detector.Record("good morning", "Good morning! ☀️", "greeting", false)
	detector.Record("good morning", "Good morning! ☀️", "greeting", false)

	candidates := detector.GetCandidates()
	if len(candidates) != 1 {
		t.Fatal("Expected 1 candidate")
	}

	patternID := candidates[0].ID

	// Mark as proposed
	detector.MarkProposed(patternID)

	// Should no longer be a candidate
	candidates = detector.GetCandidates()
	if len(candidates) != 0 {
		t.Error("Expected no candidates after marking as proposed")
	}

	// Stats should show proposed
	stats := detector.Stats()
	if stats["proposed"] != 1 {
		t.Errorf("Expected 1 proposed in stats, got %d", stats["proposed"])
	}
}

// TestMarkRejected tests marking patterns as rejected
func TestMarkRejected(t *testing.T) {
	config := DefaultPatternConfig()
	config.MinRepetitions = 2
	detector := NewPatternDetector(config)

	// Create a candidate pattern
	detector.Record("bye", "Goodbye!", "greeting", false)
	detector.Record("bye", "Goodbye!", "greeting", false)

	candidates := detector.GetCandidates()
	if len(candidates) != 1 {
		t.Fatal("Expected 1 candidate")
	}

	patternID := candidates[0].ID

	// Mark as rejected (user didn't want this reflex)
	detector.MarkRejected(patternID)

	// Should no longer be a candidate
	candidates = detector.GetCandidates()
	if len(candidates) != 0 {
		t.Error("Expected no candidates after marking as rejected")
	}

	stats := detector.Stats()
	if stats["rejected"] != 1 {
		t.Errorf("Expected 1 rejected in stats, got %d", stats["rejected"])
	}
}

// TestPrune tests removing old patterns
func TestPrune(t *testing.T) {
	config := DefaultPatternConfig()
	config.MaxPatternAge = 1 * time.Millisecond // Very short for testing
	detector := NewPatternDetector(config)

	// Create a pattern
	detector.Record("old pattern", "Response", "test", false)

	// Wait for it to expire
	time.Sleep(5 * time.Millisecond)

	// Prune should remove it
	removed := detector.Prune()
	if removed != 1 {
		t.Errorf("Expected 1 pattern pruned, got %d", removed)
	}

	stats := detector.Stats()
	if stats["total"] != 0 {
		t.Error("Expected no patterns after prune")
	}
}

// TestSuccessRate tests the success rate calculation
func TestSuccessRate(t *testing.T) {
	pattern := &Pattern{
		Occurrences: 10,
		Successes:   8,
	}

	rate := pattern.SuccessRate()
	if rate != 0.8 {
		t.Errorf("Expected success rate 0.8, got %f", rate)
	}

	// Zero occurrences
	emptyPattern := &Pattern{}
	if emptyPattern.SuccessRate() != 0 {
		t.Error("Expected 0 success rate for zero occurrences")
	}
}

// TestDefaultConfig tests default configuration values
func TestDefaultConfig(t *testing.T) {
	config := DefaultPatternConfig()

	if config.MinRepetitions != 3 {
		t.Errorf("Expected MinRepetitions 3, got %d", config.MinRepetitions)
	}

	if config.SuccessRateMin != 1.0 {
		t.Errorf("Expected SuccessRateMin 1.0, got %f", config.SuccessRateMin)
	}

	if config.SimilarityMin != 0.9 {
		t.Errorf("Expected SimilarityMin 0.9, got %f", config.SimilarityMin)
	}
}

// TestMultipleCategories tests patterns across different categories
func TestMultipleCategories(t *testing.T) {
	config := DefaultPatternConfig()
	config.MinRepetitions = 2
	detector := NewPatternDetector(config)

	// GTD patterns
	detector.Record("show inbox", "Inbox items...", "gtd", false)
	detector.Record("show inbox", "Inbox items...", "gtd", false)

	// Calendar patterns
	detector.Record("show calendar", "Events...", "calendar", false)
	detector.Record("show calendar", "Events...", "calendar", false)

	// Greeting patterns
	detector.Record("hello", "Hello!", "greeting", false)
	detector.Record("hello", "Hello!", "greeting", false)

	candidates := detector.GetCandidates()
	if len(candidates) != 3 {
		t.Errorf("Expected 3 candidates from different categories, got %d", len(candidates))
	}

	// Verify categories
	categories := make(map[string]bool)
	for _, c := range candidates {
		categories[c.Category] = true
	}

	if !categories["gtd"] || !categories["calendar"] || !categories["greeting"] {
		t.Error("Expected candidates from all three categories")
	}
}

// TestResponseUpdates tests that successful responses update the pattern
func TestResponseUpdates(t *testing.T) {
	config := DefaultPatternConfig()
	config.MinRepetitions = 2
	detector := NewPatternDetector(config)

	// First response
	detector.Record("what time", "It's 3pm", "time", false)

	// Second response (different, but successful)
	detector.Record("what time", "It's 4pm", "time", false)

	candidates := detector.GetCandidates()
	if len(candidates) != 1 {
		t.Fatal("Expected 1 candidate")
	}

	// Response should be updated to the latest successful one
	if candidates[0].Response != "It's 4pm" {
		t.Errorf("Expected response to be updated to latest, got %q", candidates[0].Response)
	}
}

// TestKnowledgeCompilationScenario tests a realistic knowledge compilation scenario
func TestKnowledgeCompilationScenario(t *testing.T) {
	config := DefaultPatternConfig()
	config.MinRepetitions = 3
	config.SuccessRateMin = 1.0 // Must be 100% successful
	detector := NewPatternDetector(config)

	// User asks "show inbox" 3 times, always same response pattern
	for i := 0; i < 3; i++ {
		detector.Record(
			"show inbox",
			"Here are your inbox items:\n- Task 1\n- Task 2",
			"gtd",
			false, // Not corrected
		)
	}

	// This pattern should now be a candidate for reflex compilation
	candidates := detector.GetCandidates()
	if len(candidates) != 1 {
		t.Fatalf("Expected 1 candidate for reflex compilation, got %d", len(candidates))
	}

	candidate := candidates[0]

	// Verify it meets compilation criteria
	if candidate.Occurrences < 3 {
		t.Error("Pattern doesn't meet repetition threshold")
	}

	if candidate.SuccessRate() < 1.0 {
		t.Error("Pattern doesn't meet success rate threshold")
	}

	if candidate.Category != "gtd" {
		t.Error("Pattern category should be preserved")
	}

	respPreview := candidate.Response
	if len(respPreview) > 50 {
		respPreview = respPreview[:50]
	}
	t.Logf("Pattern ID %s ready for compilation: %q -> %q",
		candidate.ID, candidate.InputExample, respPreview)
}
