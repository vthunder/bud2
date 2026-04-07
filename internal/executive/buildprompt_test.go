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
				Timestamp: ts,
			},
		},
	}

	out := exec.buildPrompt(bundle)

	if !strings.Contains(out, "User prefers vim keybindings") {
		t.Errorf("expected summary in output, got:\n%s", out)
	}
}

// TestBuildPrompt_EmptyBundle verifies that an empty bundle produces no output.
func TestBuildPrompt_EmptyBundle(t *testing.T) {
	exec := newTestExecutive(t)
	out := exec.buildPrompt(&focus.ContextBundle{})
	if out != "" {
		t.Errorf("expected empty output for empty bundle, got:\n%s", out)
	}
}

// TestBuildPrompt_CoreIdentity verifies that CoreIdentity is included along
// with the separator and Session Context section.
func TestBuildPrompt_CoreIdentity(t *testing.T) {
	exec := newTestExecutive(t)
	bundle := &focus.ContextBundle{
		CoreIdentity: "# You are Bud",
	}
	out := exec.buildPrompt(bundle)

	checks := []string{
		"# You are Bud",
		"---",
		"## Session Context",
		"Session started:",
		"Messages and memories from before session start are historical context only.",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

// TestBuildPrompt_ReflexLog verifies that recent reflex activity is formatted
// as bullet points with query and response.
func TestBuildPrompt_ReflexLog(t *testing.T) {
	exec := newTestExecutive(t)
	bundle := &focus.ContextBundle{
		ReflexLog: []focus.ReflexActivity{
			{Query: "what time is it", Response: "It is 3pm"},
		},
	}
	out := exec.buildPrompt(bundle)

	checks := []string{
		"## Recent Reflex Activity",
		"what time is it",
		"It is 3pm",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

// TestBuildPrompt_MemoryCompressionLevel verifies that memories with a
// compression level are formatted as "[displayID, C<N>] [timeStr] summary".
func TestBuildPrompt_MemoryCompressionLevel(t *testing.T) {
	exec := newTestExecutive(t)
	ts := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	bundle := &focus.ContextBundle{
		Memories: []focus.MemorySummary{
			{
				ID:        "abc123xyz",
				Summary:   "compressed memory",
				Level:     4,
				Timestamp: ts,
			},
		},
	}
	out := exec.buildPrompt(bundle)

	// Display ID is first 5 chars of trace ID
	if !strings.Contains(out, "[abc12, C4]") {
		t.Errorf("expected compressed memory format [abc12, C4], got:\n%s", out)
	}
	if !strings.Contains(out, "compressed memory") {
		t.Errorf("expected summary in output, got:\n%s", out)
	}
}

// TestBuildPrompt_MemorySortedChronologically verifies that memories are
// sorted oldest-first regardless of their order in the input slice.
func TestBuildPrompt_MemorySortedChronologically(t *testing.T) {
	exec := newTestExecutive(t)
	older := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	bundle := &focus.ContextBundle{
		Memories: []focus.MemorySummary{
			{ID: "newer111", Summary: "newer memory", Timestamp: newer},
			{ID: "older111", Summary: "older memory", Timestamp: older},
		},
	}
	out := exec.buildPrompt(bundle)

	olderPos := strings.Index(out, "older memory")
	newerPos := strings.Index(out, "newer memory")
	if olderPos == -1 || newerPos == -1 {
		t.Fatalf("expected both summaries in output, got:\n%s", out)
	}
	if olderPos > newerPos {
		t.Errorf("expected older memory to appear before newer memory in prompt")
	}
}

// TestBuildPrompt_MemoryEvalSection verifies that the memory eval instruction
// is included only when memories are present.
func TestBuildPrompt_MemoryEvalSection(t *testing.T) {
	exec := newTestExecutive(t)
	ts := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	// With memories: eval section must appear
	with := exec.buildPrompt(&focus.ContextBundle{
		Memories: []focus.MemorySummary{
			{ID: "mem1", Summary: "some fact", Timestamp: ts},
		},
	})
	if !strings.Contains(with, "## Memory Eval") {
		t.Errorf("expected ## Memory Eval when memories present, got:\n%s", with)
	}

	// Without memories: eval section must NOT appear
	exec2 := newTestExecutive(t)
	without := exec2.buildPrompt(&focus.ContextBundle{})
	if strings.Contains(without, "## Memory Eval") {
		t.Errorf("expected no ## Memory Eval when no memories, got:\n%s", without)
	}
}

// TestBuildPrompt_PriorMemoriesCount verifies that the Recalled Memories
// section header is shown even when no new memories exist, as long as
// PriorMemoriesCount > 0.
func TestBuildPrompt_PriorMemoriesCount(t *testing.T) {
	exec := newTestExecutive(t)
	bundle := &focus.ContextBundle{
		PriorMemoriesCount: 3,
	}
	out := exec.buildPrompt(bundle)

	if !strings.Contains(out, "## Recalled Memories") {
		t.Errorf("expected ## Recalled Memories section when PriorMemoriesCount > 0, got:\n%s", out)
	}
}

// TestBuildPrompt_BufferContent verifies that conversation buffer content
// is included under ## Recent Conversation.
func TestBuildPrompt_BufferContent(t *testing.T) {
	exec := newTestExecutive(t)
	bundle := &focus.ContextBundle{
		BufferContent: "[user] hello\n[bud] hi there",
	}
	out := exec.buildPrompt(bundle)

	checks := []string{
		"## Recent Conversation",
		"[user] hello",
		"[bud] hi there",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

// TestBuildPrompt_AuthorizationsWarning verifies that the authorization
// warning banner appears when HasAuthorizations is true.
func TestBuildPrompt_AuthorizationsWarning(t *testing.T) {
	exec := newTestExecutive(t)
	bundle := &focus.ContextBundle{
		BufferContent:     "some conversation with approval",
		HasAuthorizations: true,
	}
	out := exec.buildPrompt(bundle)

	if !strings.Contains(out, "WARNING") {
		t.Errorf("expected WARNING banner when HasAuthorizations=true, got:\n%s", out)
	}
	if !strings.Contains(out, "user approvals") {
		t.Errorf("expected 'user approvals' in warning, got:\n%s", out)
	}
}

// TestBuildPrompt_NoAuthorizationsWarning verifies that when HasAuthorizations
// is false there is no warning banner.
func TestBuildPrompt_NoAuthorizationsWarning(t *testing.T) {
	exec := newTestExecutive(t)
	bundle := &focus.ContextBundle{
		BufferContent:     "clean conversation",
		HasAuthorizations: false,
	}
	out := exec.buildPrompt(bundle)

	if strings.Contains(out, "WARNING") {
		t.Errorf("expected no WARNING when HasAuthorizations=false, got:\n%s", out)
	}
}

// TestBuildPrompt_SuspendedTasks verifies that suspended items are listed
// under ## Suspended Tasks.
func TestBuildPrompt_SuspendedTasks(t *testing.T) {
	exec := newTestExecutive(t)
	bundle := &focus.ContextBundle{
		Suspended: []*focus.PendingItem{
			{Type: "user_input", Content: "remind me about the meeting"},
		},
	}
	out := exec.buildPrompt(bundle)

	checks := []string{
		"## Suspended Tasks",
		"user_input",
		"remind me about the meeting",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in suspended tasks output, got:\n%s", want, out)
		}
	}
}

// TestBuildPrompt_CurrentFocusBasic verifies section header mapping and bare content rendering.
func TestBuildPrompt_CurrentFocusBasic(t *testing.T) {
	cases := []struct {
		name           string
		item           *focus.PendingItem
		expectedHeader string
	}{
		{
			name: "inbox message uses ## Message",
			item: &focus.PendingItem{
				Type: "message", Source: "inbox", Content: "can you help me with this?",
			},
			expectedHeader: "## Message",
		},
		{
			name: "wake type uses ## Autonomous Wake",
			item: &focus.PendingItem{
				Type: "wake", Source: "impulse:system", Content: "Periodic autonomous wake-up.",
			},
			expectedHeader: "## Autonomous Wake",
		},
		{
			name: "startup impulse uses ## Startup",
			item: &focus.PendingItem{
				Type: "unknown", Source: "impulse:system", Content: "impulse:startup",
			},
			expectedHeader: "## Startup",
		},
		{
			name: "unknown type falls back to ## Current Focus",
			item: &focus.PendingItem{
				Type: "due", Source: "impulse:task", Content: "task reminder",
			},
			expectedHeader: "## Current Focus",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec := newTestExecutive(t)
			out := exec.buildPrompt(&focus.ContextBundle{CurrentFocus: tc.item})
			if !strings.Contains(out, tc.expectedHeader) {
				t.Errorf("expected header %q, got:\n%s", tc.expectedHeader, out)
			}
			if !strings.Contains(out, tc.item.Content) {
				t.Errorf("expected bare content %q in output, got:\n%s", tc.item.Content, out)
			}
			for _, unwanted := range []string{"Type:", "Priority:", "Source:", "Content:"} {
				if strings.Contains(out, unwanted) {
					t.Errorf("expected %q absent, got:\n%s", unwanted, out)
				}
			}
		})
	}
}

// TestBuildPrompt_CurrentFocusMetadata verifies that message_id appears as "id: <value>"
// with no Metadata wrapper, and that channel_id and timestamp are omitted.
func TestBuildPrompt_CurrentFocusMetadata(t *testing.T) {
	exec := newTestExecutive(t)
	ts := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	bundle := &focus.ContextBundle{
		CurrentFocus: &focus.PendingItem{
			Type:      "user_input",
			Priority:  focus.P1UserInput,
			Content:   "test",
			ChannelID: "chan-999",
			Timestamp: ts,
			Data: map[string]any{
				"message_id": "msg-42",
			},
		},
	}
	out := exec.buildPrompt(bundle)

	if !strings.Contains(out, "id: msg-42") {
		t.Errorf("expected \"id: msg-42\" in output, got:\n%s", out)
	}
	for _, unwanted := range []string{"Metadata:", "message_id:", "channel_id:", "2026-02-01"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("expected %q to be absent from output, got:\n%s", unwanted, out)
		}
	}
}

// TestBuildPrompt_CurrentFocusAttachments verifies that attachments are
// rendered as "filename (content_type): url".
func TestBuildPrompt_CurrentFocusAttachments(t *testing.T) {
	exec := newTestExecutive(t)
	bundle := &focus.ContextBundle{
		CurrentFocus: &focus.PendingItem{
			Type:     "user_input",
			Priority: focus.P1UserInput,
			Content:  "check this file",
			Data: map[string]any{
				"attachments": []interface{}{
					map[string]interface{}{
						"filename":     "screenshot.png",
						"content_type": "image/png",
						"url":          "https://cdn.example.com/screenshot.png",
					},
				},
			},
		},
	}
	out := exec.buildPrompt(bundle)

	checks := []string{
		"Attachments:",
		"screenshot.png (image/png): https://cdn.example.com/screenshot.png",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

// TestBuildPrompt_BatchedMessages verifies that multiple messages render under
// ## Messages with inline [id] prefixes.
func TestBuildPrompt_BatchedMessages(t *testing.T) {
	exec := newTestExecutive(t)
	bundle := &focus.ContextBundle{
		CurrentFocus: &focus.PendingItem{
			Type:    "message",
			Source:  "inbox",
			Content: "first message",
			Data:    map[string]any{"message_id": "id-aaa"},
		},
		AdditionalFocus: []*focus.PendingItem{
			{
				Type:    "message",
				Source:  "inbox",
				Content: "second message",
				Data:    map[string]any{"message_id": "id-bbb"},
			},
		},
	}
	out := exec.buildPrompt(bundle)

	if !strings.Contains(out, "## Messages") {
		t.Errorf("expected ## Messages header for batched messages, got:\n%s", out)
	}
	if strings.Contains(out, "## Message\n") {
		t.Errorf("expected no single ## Message header in batched case, got:\n%s", out)
	}
	if !strings.Contains(out, "[id-aaa] first message") {
		t.Errorf("expected inline ID for first message, got:\n%s", out)
	}
	if !strings.Contains(out, "[id-bbb] second message") {
		t.Errorf("expected inline ID for second message, got:\n%s", out)
	}
}

// TestBuildPrompt_WakeFocus verifies that wake-type focus items inject the
// WakeupInstructions and WakeSessionContext into the prompt.
func TestBuildPrompt_WakeFocus(t *testing.T) {
	statePath := t.TempDir()
	exec := NewExecutiveV2(nil, nil, statePath, ExecutiveV2Config{
		WakeupInstructions: "# Autonomous Wake\nCheck tasks and do background work.",
	})

	bundle := &focus.ContextBundle{
		CurrentFocus: &focus.PendingItem{
			Type:     "wake",
			Priority: focus.P3ActiveWork,
			Content:  "Periodic autonomous wake-up.",
		},
		WakeSessionContext: "[user] testing once more\n[bud] all good",
	}
	out := exec.buildPrompt(bundle)

	checks := []string{
		"## Autonomous Wake",
		"# Autonomous Wake",
		"Check tasks and do background work.",
		"## Conversation Log",
		"[user] testing once more",
		"[bud] all good",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in wake prompt, got:\n%s", want, out)
		}
	}
}

// TestBuildPrompt_ActiveSchemas verifies that the ## Active Schemas section
// appears when ActiveSchemas is non-empty, with truncated 8-char IDs.
func TestBuildPrompt_ActiveSchemas(t *testing.T) {
	exec := newTestExecutive(t)
	bundle := &focus.ContextBundle{
		ActiveSchemas: []*focus.SchemaSummary{
			{
				ID:      "46b6c630abcdef1234", // full hash — display uses first 8 chars
				Name:    "Memory System Debugging",
				Summary: "validate via DB inspection; fix consolidation pipeline",
			},
			{
				ID:      "39b431d7deadbeef99",
				Name:    "System Infrastructure Optimization",
				Summary: "identify bottleneck, implement fix, measure latency impact",
			},
		},
	}
	out := exec.buildPrompt(bundle)

	checks := []string{
		"## Active Schemas",
		"get_schema",
		"[46b6c630] Memory System Debugging",
		"validate via DB inspection",
		"[39b431d7] System Infrastructure Optimization",
		"identify bottleneck",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in active schemas output, got:\n%s", want, out)
		}
	}
}

// TestBuildPrompt_ActiveSchemasEmpty verifies that the ## Active Schemas section
// is absent when ActiveSchemas is nil or empty.
func TestBuildPrompt_ActiveSchemasEmpty(t *testing.T) {
	exec := newTestExecutive(t)
	out := exec.buildPrompt(&focus.ContextBundle{})

	if strings.Contains(out, "## Active Schemas") {
		t.Errorf("expected no ## Active Schemas section when ActiveSchemas is empty, got:\n%s", out)
	}
}

// TestBuildPrompt_ActiveSchemasShortID verifies that schema IDs shorter than
// 8 chars are used as-is (no panic from slicing).
func TestBuildPrompt_ActiveSchemasShortID(t *testing.T) {
	exec := newTestExecutive(t)
	bundle := &focus.ContextBundle{
		ActiveSchemas: []*focus.SchemaSummary{
			{ID: "abc", Name: "Short", Summary: "short id schema"},
		},
	}
	out := exec.buildPrompt(bundle)

	if !strings.Contains(out, "[abc]") {
		t.Errorf("expected short ID [abc] in output, got:\n%s", out)
	}
}

// TestBuildPrompt_WakeFocusNoInstructions verifies that wake context is NOT
// injected when WakeupInstructions is empty.
func TestBuildPrompt_WakeFocusNoInstructions(t *testing.T) {
	exec := newTestExecutive(t) // WakeupInstructions is empty
	bundle := &focus.ContextBundle{
		CurrentFocus: &focus.PendingItem{
			Type:    "wake",
			Content: "Periodic wake.",
		},
		WakeSessionContext: "some recent context",
	}
	out := exec.buildPrompt(bundle)

	// Without WakeupInstructions, the wake context block should not appear
	if strings.Contains(out, "## Conversation Log") {
		t.Errorf("expected no wake context without WakeupInstructions, got:\n%s", out)
	}
}
