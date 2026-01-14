package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspector_Summary(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestState(t, tmpDir)

	inspector := NewInspector(tmpDir, nil)
	summary, err := inspector.Summary()
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	if summary.Traces.Total < 0 {
		t.Error("traces total should be >= 0")
	}
}

func TestInspector_Health(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestState(t, tmpDir)

	inspector := NewInspector(tmpDir, nil)
	health, err := inspector.Health()
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}

	if health.Status == "" {
		t.Error("health status should not be empty")
	}
}

func setupTestState(t *testing.T, dir string) {
	t.Helper()
	// Create system directory structure
	systemDir := filepath.Join(dir, "system")
	queuesDir := filepath.Join(systemDir, "queues")
	os.MkdirAll(queuesDir, 0755)
	os.WriteFile(filepath.Join(systemDir, "traces.json"), []byte(`{"traces":[]}`), 0644)
	os.WriteFile(filepath.Join(systemDir, "threads.json"), []byte(`{"threads":[]}`), 0644)
	os.WriteFile(filepath.Join(systemDir, "sessions.json"), []byte(`{}`), 0644)
	os.WriteFile(filepath.Join(queuesDir, "percepts.json"), []byte(`{"percepts":[]}`), 0644)
}
