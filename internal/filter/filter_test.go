package filter

import (
	"testing"
)

// MockEmbedder returns deterministic embeddings for testing
type MockEmbedder struct {
	// embeddings maps content to predetermined vectors
	embeddings map[string][]float64
	// defaultDim is the embedding dimension to use for unknown content
	defaultDim int
}

// NewMockEmbedder creates a mock embedder with default 8-dimensional vectors
func NewMockEmbedder() *MockEmbedder {
	return &MockEmbedder{
		embeddings: make(map[string][]float64),
		defaultDim: 8,
	}
}

// SetEmbedding sets a predetermined embedding for content
func (m *MockEmbedder) SetEmbedding(content string, embedding []float64) {
	m.embeddings[content] = embedding
}

// Embed returns a predetermined embedding or generates a simple hash-based one
func (m *MockEmbedder) Embed(text string) ([]float64, error) {
	if emb, ok := m.embeddings[text]; ok {
		return emb, nil
	}
	// Generate deterministic embedding based on content hash
	return hashToEmbedding(text, m.defaultDim), nil
}

// hashToEmbedding creates a deterministic embedding from content
func hashToEmbedding(content string, dim int) []float64 {
	emb := make([]float64, dim)
	for i, c := range content {
		emb[i%dim] += float64(c) / 1000.0
	}
	// Normalize
	var norm float64
	for _, v := range emb {
		norm += v * v
	}
	if norm > 0 {
		norm = sqrt(norm)
		for i := range emb {
			emb[i] /= norm
		}
	}
	return emb
}

// TestDialogueActClassification tests the dialogue act classifier
func TestDialogueActClassification(t *testing.T) {
	tests := []struct {
		input    string
		expected DialogueAct
	}{
		// Backchannels
		{"yes", ActBackchannel},
		{"yeah", ActBackchannel},
		{"ok", ActBackchannel},
		{"okay", ActBackchannel},
		{"got it", ActBackchannel},
		{"👍", ActBackchannel},
		{"thanks", ActBackchannel},
		{"sure", ActBackchannel},
		{"cool", ActBackchannel},
		{"", ActBackchannel},

		// Greetings
		{"hi", ActGreeting},
		{"hello", ActGreeting},
		{"hey", ActGreeting},
		{"good morning", ActGreeting},
		{"bye", ActGreeting},

		// Questions
		{"what time is it?", ActQuestion},
		{"where is the file?", ActQuestion},
		{"can you help me?", ActQuestion},
		{"how does this work?", ActQuestion},

		// Commands
		{"run the tests", ActCommand},
		{"show me the logs", ActCommand},
		{"please fix this", ActCommand},
		{"add a new task", ActCommand},

		// Statements (default)
		{"I finished the report yesterday", ActStatement},
		{"The meeting is at 3pm", ActStatement},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ClassifyDialogueAct(tt.input)
			if got != tt.expected {
				t.Errorf("ClassifyDialogueAct(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

// TestIsBackchannel tests the backchannel detection
func TestIsBackchannel(t *testing.T) {
	backchannels := []string{"yes", "ok", "sure", "got it", "👍", "thanks", ""}
	for _, bc := range backchannels {
		if !IsBackchannel(bc) {
			t.Errorf("IsBackchannel(%q) = false, want true", bc)
		}
	}

	notBackchannels := []string{"yes, I'll do that tomorrow", "ok let me think about it", "what?", "run tests"}
	for _, nbc := range notBackchannels {
		if IsBackchannel(nbc) {
			t.Errorf("IsBackchannel(%q) = true, want false", nbc)
		}
	}
}

// TestIsLowInfo tests low-information detection
func TestIsLowInfo(t *testing.T) {
	lowInfo := []string{"yes", "hi", "hello", "ok", "bye", "👍"}
	for _, li := range lowInfo {
		if !IsLowInfo(li) {
			t.Errorf("IsLowInfo(%q) = false, want true", li)
		}
	}

	highInfo := []string{"I need to finish the project by Friday", "The API is returning 500 errors", "run tests"}
	for _, hi := range highInfo {
		if IsLowInfo(hi) {
			t.Errorf("IsLowInfo(%q) = true, want false", hi)
		}
	}
}

// TestShouldAttachToPrevious tests attachment detection
func TestShouldAttachToPrevious(t *testing.T) {
	shouldAttach := []string{"yes", "ok", "sure", "got it"}
	for _, s := range shouldAttach {
		if !ShouldAttachToPrevious(s) {
			t.Errorf("ShouldAttachToPrevious(%q) = false, want true", s)
		}
	}

	shouldNotAttach := []string{
		"what is the status of the project?",
		"I finished implementing the feature",
		"Please review this pull request",
	}
	for _, s := range shouldNotAttach {
		if ShouldAttachToPrevious(s) {
			t.Errorf("ShouldAttachToPrevious(%q) = true, want false", s)
		}
	}
}

// TestEntropyFilter tests the entropy-based quality filter
func TestEntropyFilter(t *testing.T) {
	mock := NewMockEmbedder()
	filter := NewEntropyFilter(mock)

	// Test entity novelty detection
	t.Run("EntityNovelty", func(t *testing.T) {
		// High entity content (names, proper nouns)
		highEntity := "John Smith met with Maria Garcia at Google HQ on Monday"
		result, err := filter.Score(highEntity)
		if err != nil {
			t.Fatalf("Score failed: %v", err)
		}
		if result.EntityNovelty < 0.3 {
			t.Errorf("Expected high entity novelty for %q, got %f", highEntity, result.EntityNovelty)
		}

		// Low entity content
		lowEntity := "yes ok sure"
		result, err = filter.Score(lowEntity)
		if err != nil {
			t.Fatalf("Score failed: %v", err)
		}
		if result.EntityNovelty > 0.2 {
			t.Errorf("Expected low entity novelty for %q, got %f", lowEntity, result.EntityNovelty)
		}
	})

	// Test semantic divergence
	t.Run("SemanticDivergence", func(t *testing.T) {
		filter.ClearHistory()

		// First message should be maximally divergent
		result1, _ := filter.Score("The project deadline is next Friday")
		if result1.SemanticDiv < 0.9 {
			t.Errorf("First message should have high semantic divergence, got %f", result1.SemanticDiv)
		}

		// Similar message should have lower divergence
		filter.ClearHistory()
		filter.Score("The project deadline is next Friday")
		result2, _ := filter.Score("The project deadline is next Monday")
		if result2.SemanticDiv > result1.SemanticDiv {
			t.Errorf("Similar content should have lower divergence")
		}
	})

	// Test threshold behavior
	t.Run("Threshold", func(t *testing.T) {
		filter.ClearHistory()
		filter.SetThreshold(0.5)

		// High-info message should pass
		shouldPass, score, _ := filter.ShouldCreateEpisode("John Smith scheduled a meeting with the CEO for Monday at 10am")
		if !shouldPass {
			t.Errorf("High-info message should pass threshold, score=%f", score)
		}

		// After several similar messages, divergence drops
		filter.ClearHistory()
		filter.SetThreshold(0.8)
		for i := 0; i < 5; i++ {
			filter.Score("ok")
		}
		shouldPass, score, _ = filter.ShouldCreateEpisode("ok")
		if shouldPass {
			t.Errorf("Repeated low-info message should fail threshold, score=%f", score)
		}
	})
}

// TestEntropyFilterNilEmbedder tests filter behavior without embedder
func TestEntropyFilterNilEmbedder(t *testing.T) {
	filter := NewEntropyFilter(nil)

	result, err := filter.Score("Test message")
	if err != nil {
		t.Fatalf("Score failed: %v", err)
	}

	// Should fall back to entity novelty only
	if result.SemanticDiv != 0.5 {
		t.Errorf("Expected default semantic divergence of 0.5, got %f", result.SemanticDiv)
	}
}
