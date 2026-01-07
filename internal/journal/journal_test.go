package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestJournalLog(t *testing.T) {
	tmpDir := t.TempDir()
	j := New(tmpDir)

	// Log a decision
	err := j.LogDecision("user asked about weather", "need current info", "fetching weather API")
	if err != nil {
		t.Fatalf("LogDecision failed: %v", err)
	}

	// Log an impulse
	err = j.LogImpulse("tasks", "review PR #42", "processing now")
	if err != nil {
		t.Fatalf("LogImpulse failed: %v", err)
	}

	// Log a reflex
	err = j.LogReflex("greeting pattern", "wave emoji", map[string]any{"channel": "general"})
	if err != nil {
		t.Fatalf("LogReflex failed: %v", err)
	}

	// Read back entries
	entries, err := j.Recent(10)
	if err != nil {
		t.Fatalf("Recent failed: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(entries))
	}

	// Verify first entry
	if entries[0].Type != EntryDecision {
		t.Errorf("Expected decision type, got %s", entries[0].Type)
	}
	if entries[0].Context != "user asked about weather" {
		t.Errorf("Unexpected context: %s", entries[0].Context)
	}

	// Verify file format
	data, _ := os.ReadFile(filepath.Join(tmpDir, "journal.jsonl"))
	lines := splitLines(data)
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Errorf("Invalid JSON line: %s", line)
		}
	}

	t.Logf("Journal test passed with %d entries", len(entries))
}

func TestJournalToday(t *testing.T) {
	tmpDir := t.TempDir()
	j := New(tmpDir)

	// Log some entries today
	j.LogAction("tested something", "test context", nil)
	j.LogAction("tested another thing", "test context", nil)

	entries, err := j.Today()
	if err != nil {
		t.Fatalf("Today failed: %v", err)
	}

	if len(entries) != 2 {
		t.Errorf("Expected 2 entries today, got %d", len(entries))
	}
}
