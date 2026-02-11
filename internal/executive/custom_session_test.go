package executive

import (
	"context"
	"testing"
	"time"
)

func TestRunCustomSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prompt := `You are a helpful assistant. Answer the following question in JSON format:

Question: What is 2 + 2?

Return your answer as:
{
  "answer": <number>,
  "explanation": "<brief explanation>"
}
`

	cfg := CustomSessionConfig{
		Model:   "claude-sonnet-4-5",
		Verbose: true,
	}

	result := RunCustomSession(ctx, prompt, cfg)

	if result.Error != nil {
		t.Fatalf("RunCustomSession failed: %v", result.Error)
	}

	if result.Output == "" {
		t.Fatal("Expected non-empty output")
	}

	if result.Usage == nil {
		t.Fatal("Expected usage metrics")
	}

	t.Logf("Output: %s", result.Output)
	t.Logf("Usage: input=%d output=%d duration=%dms",
		result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.DurationMs)
}
