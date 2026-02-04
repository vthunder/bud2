package authorize

import (
	"testing"

	"github.com/vthunder/bud2/internal/embedding"
)

func TestClassifyText(t *testing.T) {
	// This test requires Ollama to be running locally
	ollama := embedding.NewClient("", "")
	ollama.SetGenerationModel("qwen2.5:7b")
	classifier := NewClassifier(ollama)

	tests := []struct {
		name     string
		text     string
		wantAuth bool
	}{
		{
			name:     "explicit authorization",
			text:     "thunder05521: yes go ahead and deploy",
			wantAuth: true,
		},
		{
			name:     "do it now",
			text:     "thunder05521: you can do it now",
			wantAuth: true,
		},
		{
			name:     "proceed",
			text:     "thunder05521: proceed with the changes",
			wantAuth: true,
		},
		{
			name:     "question - no authorization",
			text:     "thunder05521: what files will be affected?",
			wantAuth: false,
		},
		{
			name:     "statement - no authorization",
			text:     "thunder05521: I think the bug is in the parser",
			wantAuth: false,
		},
		{
			name:     "asking about status",
			text:     "thunder05521: did you finish the task?",
			wantAuth: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasAuth, err := classifier.ClassifyText(tt.text)
			if err != nil {
				t.Fatalf("ClassifyText() error = %v", err)
			}
			if hasAuth != tt.wantAuth {
				t.Errorf("ClassifyText(%q) = %v, want %v", tt.text, hasAuth, tt.wantAuth)
			}
		})
	}
}

func TestAnnotateIfAuthorized(t *testing.T) {
	ollama := embedding.NewClient("", "")
	ollama.SetGenerationModel("qwen2.5:7b")
	classifier := NewClassifier(ollama)

	text := "thunder05521: go ahead"
	annotated, hasAuth := classifier.AnnotateIfAuthorized(text)

	if !hasAuth {
		t.Errorf("Expected authorization to be detected")
	}

	if annotated == text {
		t.Errorf("Expected text to be annotated, got same text back")
	}

	expectedPrefix := "[HISTORICAL - re-confirm before acting]"
	if !contains(annotated, expectedPrefix) {
		t.Errorf("Annotated text should contain %q, got %q", expectedPrefix, annotated)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr
}
