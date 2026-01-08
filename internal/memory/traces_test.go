package memory

import (
	"path/filepath"
	"testing"

	"github.com/vthunder/bud2/internal/types"
)

func TestTracePool_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewTracePool(filepath.Join(tmpDir, "traces.json"))

	// Add a trace
	trace := &types.Trace{ID: "test-1", Content: "test content"}
	pool.Add(trace)

	// Verify it exists
	if pool.Get("test-1") == nil {
		t.Fatal("trace should exist after Add")
	}

	// Delete it
	deleted := pool.Delete("test-1")
	if !deleted {
		t.Error("Delete should return true for existing trace")
	}

	// Verify it's gone
	if pool.Get("test-1") != nil {
		t.Error("trace should not exist after Delete")
	}

	// Delete non-existent should return false
	deleted = pool.Delete("non-existent")
	if deleted {
		t.Error("Delete should return false for non-existent trace")
	}
}

func TestTracePool_ClearNonCore(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewTracePool(filepath.Join(tmpDir, "traces.json"))

	// Add core and non-core traces
	pool.Add(&types.Trace{ID: "core-1", Content: "core", IsCore: true})
	pool.Add(&types.Trace{ID: "normal-1", Content: "normal", IsCore: false})
	pool.Add(&types.Trace{ID: "normal-2", Content: "normal 2", IsCore: false})

	// Clear non-core
	cleared := pool.ClearNonCore()
	if cleared != 2 {
		t.Errorf("expected 2 cleared, got %d", cleared)
	}

	// Verify core remains
	if pool.Get("core-1") == nil {
		t.Error("core trace should remain")
	}
	if pool.Get("normal-1") != nil {
		t.Error("non-core trace should be cleared")
	}
}

func TestTracePool_ClearCore(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewTracePool(filepath.Join(tmpDir, "traces.json"))

	pool.Add(&types.Trace{ID: "core-1", Content: "core", IsCore: true})
	pool.Add(&types.Trace{ID: "core-2", Content: "core 2", IsCore: true})
	pool.Add(&types.Trace{ID: "normal-1", Content: "normal", IsCore: false})

	cleared := pool.ClearCore()
	if cleared != 2 {
		t.Errorf("expected 2 cleared, got %d", cleared)
	}

	if pool.Get("core-1") != nil {
		t.Error("core trace should be cleared")
	}
	if pool.Get("normal-1") == nil {
		t.Error("non-core trace should remain")
	}
}
