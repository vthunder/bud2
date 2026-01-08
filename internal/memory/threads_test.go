package memory

import (
	"path/filepath"
	"testing"

	"github.com/vthunder/bud2/internal/types"
)

func TestThreadPool_Clear(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewThreadPool(filepath.Join(tmpDir, "threads.json"))

	pool.Add(&types.Thread{ID: "t1", Status: types.StatusActive})
	pool.Add(&types.Thread{ID: "t2", Status: types.StatusFrozen})

	if pool.Count() != 2 {
		t.Fatalf("expected 2, got %d", pool.Count())
	}

	cleared := pool.Clear()
	if cleared != 2 {
		t.Errorf("expected 2 cleared, got %d", cleared)
	}

	if pool.Count() != 0 {
		t.Errorf("expected 0 after clear, got %d", pool.Count())
	}
}

func TestThreadPool_ClearByStatus(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewThreadPool(filepath.Join(tmpDir, "threads.json"))

	pool.Add(&types.Thread{ID: "t1", Status: types.StatusActive})
	pool.Add(&types.Thread{ID: "t2", Status: types.StatusFrozen})
	pool.Add(&types.Thread{ID: "t3", Status: types.StatusFrozen})

	cleared := pool.ClearByStatus(types.StatusFrozen)
	if cleared != 2 {
		t.Errorf("expected 2 cleared, got %d", cleared)
	}

	if pool.Get("t1") == nil {
		t.Error("active thread should remain")
	}
	if pool.Get("t2") != nil {
		t.Error("frozen thread should be cleared")
	}
}

func TestThreadPool_Count(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewThreadPool(filepath.Join(tmpDir, "threads.json"))

	if pool.Count() != 0 {
		t.Errorf("expected 0, got %d", pool.Count())
	}

	pool.Add(&types.Thread{ID: "t1"})
	if pool.Count() != 1 {
		t.Errorf("expected 1, got %d", pool.Count())
	}
}
