package memory

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/vthunder/bud2/internal/types"
)

func TestPerceptPool_Clear(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewPerceptPool(filepath.Join(tmpDir, "percepts.json"))

	pool.Add(&types.Percept{ID: "p1", Timestamp: time.Now()})
	pool.Add(&types.Percept{ID: "p2", Timestamp: time.Now()})

	if pool.Count() != 2 {
		t.Fatalf("expected 2 percepts, got %d", pool.Count())
	}

	cleared := pool.Clear()
	if cleared != 2 {
		t.Errorf("expected 2 cleared, got %d", cleared)
	}

	if pool.Count() != 0 {
		t.Errorf("expected 0 percepts after clear, got %d", pool.Count())
	}
}

func TestPerceptPool_Count(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewPerceptPool(filepath.Join(tmpDir, "percepts.json"))

	if pool.Count() != 0 {
		t.Errorf("expected 0, got %d", pool.Count())
	}

	pool.Add(&types.Percept{ID: "p1", Timestamp: time.Now()})
	if pool.Count() != 1 {
		t.Errorf("expected 1, got %d", pool.Count())
	}
}
