package executive

import (
	"strings"
	"testing"
	"time"

	"github.com/vthunder/bud2/internal/focus"
)

// newTestExecutive creates a minimal ExecutiveV2 for prompt-building tests.
// memory and reflexLog are nil; statePath is a temp dir.
func newTestExecutive(t *testing.T) *ExecutiveV2 {
	t.Helper()
	statePath := t.TempDir()
	return NewExecutiveV2(nil, nil, statePath, ExecutiveV2Config{})
}

// TestBuildPrompt_NonConflictFormatting verifies that normal memories
// are still formatted in the standard "[displayID] [timeStr] summary" style.
func TestBuildPrompt_NonConflictFormatting(t *testing.T) {
	exec := newTestExecutive(t)

	ts := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	bundle := &focus.ContextBundle{
		Memories: []focus.MemorySummary{
			{
				ID:        "trace-normal-id",
				Summary:   "User prefers vim keybindings",
				Relevance: 0.7,
				Timestamp: ts,
			},
		},
	}

	out := exec.buildPrompt(bundle)

	if !strings.Contains(out, "User prefers vim keybindings") {
		t.Errorf("expected summary in output, got:\n%s", out)
	}
}
