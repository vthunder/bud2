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

// TestBuildPrompt_ConflictFormatting verifies that conflicting trace pairs are
// grouped together with a "[CONFLICT]" label and the "contradicts above" annotation.
func TestBuildPrompt_ConflictFormatting(t *testing.T) {
	exec := newTestExecutive(t)

	ts := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	ts2 := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)

	// Trace A has short_id "abc12", says "prefers dark mode", and conflicts with "def34"
	// Trace B has short_id "def34", says "switched to light mode", and conflicts with "abc12"
	bundle := &focus.ContextBundle{
		Memories: []focus.MemorySummary{
			{
				ID:           "trace-a-full-id",
				ShortID:      "abc12",
				Summary:      "User prefers dark mode",
				Relevance:    0.9,
				Timestamp:    ts,
				HasConflict:  true,
				ConflictWith: "def34",
			},
			{
				ID:           "trace-b-full-id",
				ShortID:      "def34",
				Summary:      "User switched to light mode",
				Relevance:    0.8,
				Timestamp:    ts2,
				HasConflict:  true,
				ConflictWith: "abc12",
			},
		},
	}

	out := exec.buildPrompt(bundle)

	// Should contain CONFLICT label
	if !strings.Contains(out, "[CONFLICT]") {
		t.Errorf("expected [CONFLICT] label in output, got:\n%s", out)
	}
	// Should contain both summaries
	if !strings.Contains(out, "User prefers dark mode") {
		t.Errorf("expected trace A summary in output, got:\n%s", out)
	}
	if !strings.Contains(out, "User switched to light mode") {
		t.Errorf("expected trace B summary in output, got:\n%s", out)
	}
	// Should contain "contradicts above" annotation
	if !strings.Contains(out, "contradicts above") {
		t.Errorf("expected 'contradicts above' annotation, got:\n%s", out)
	}
	// Should NOT format either trace as a plain memory line (no double-listing)
	// Count occurrences of the summaries - each should appear exactly once
	countA := strings.Count(out, "User prefers dark mode")
	countB := strings.Count(out, "User switched to light mode")
	if countA != 1 {
		t.Errorf("trace A summary should appear exactly once, got %d times", countA)
	}
	if countB != 1 {
		t.Errorf("trace B summary should appear exactly once, got %d times", countB)
	}
}

// TestBuildPrompt_NonConflictFormatting verifies that normal (non-conflicted) memories
// are still formatted in the standard "[displayID] [timeStr] summary" style.
func TestBuildPrompt_NonConflictFormatting(t *testing.T) {
	exec := newTestExecutive(t)

	ts := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	bundle := &focus.ContextBundle{
		Memories: []focus.MemorySummary{
			{
				ID:        "trace-normal-id",
				ShortID:   "aa111",
				Summary:   "User prefers vim keybindings",
				Relevance: 0.7,
				Timestamp: ts,
			},
		},
	}

	out := exec.buildPrompt(bundle)

	if strings.Contains(out, "[CONFLICT]") {
		t.Errorf("unexpected [CONFLICT] label for non-conflicted memory, got:\n%s", out)
	}
	if !strings.Contains(out, "User prefers vim keybindings") {
		t.Errorf("expected summary in output, got:\n%s", out)
	}
}

// TestBuildPrompt_ConflictWithMissingPartner verifies that a conflicted trace whose
// partner is NOT in the retrieved set still renders as a normal (non-paired) memory.
func TestBuildPrompt_ConflictWithMissingPartner(t *testing.T) {
	exec := newTestExecutive(t)

	ts := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	bundle := &focus.ContextBundle{
		Memories: []focus.MemorySummary{
			{
				ID:           "trace-orphan-id",
				ShortID:      "xxx99",
				Summary:      "User prefers dark mode",
				Relevance:    0.9,
				Timestamp:    ts,
				HasConflict:  true,
				ConflictWith: "yyy00", // partner not in result set
			},
		},
	}

	out := exec.buildPrompt(bundle)

	// No CONFLICT label since partner is absent
	if strings.Contains(out, "[CONFLICT]") {
		t.Errorf("unexpected [CONFLICT] label when partner is absent, got:\n%s", out)
	}
	// Still shows the memory
	if !strings.Contains(out, "User prefers dark mode") {
		t.Errorf("expected orphan conflict summary in output, got:\n%s", out)
	}
}
