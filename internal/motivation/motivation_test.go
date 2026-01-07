package motivation

import (
	"testing"
	"time"
)

func TestTaskStore(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewTaskStore(tmpDir)

	// Add a task
	overdue := time.Now().Add(-time.Hour)
	task := &Task{
		Task:     "Review PR #42",
		Due:      &overdue, // overdue
		Priority: 1,
		Context:  "Promised in standup",
	}
	store.Add(task)

	// Check it was added
	if len(store.GetPending()) != 1 {
		t.Errorf("Expected 1 pending task, got %d", len(store.GetPending()))
	}

	// Check overdue detection
	due := store.GetDue()
	if len(due) != 1 {
		t.Errorf("Expected 1 overdue task, got %d", len(due))
	}

	// Generate impulses
	impulses := store.GenerateImpulses()
	if len(impulses) != 1 {
		t.Errorf("Expected 1 impulse for overdue task, got %d", len(impulses))
	}
	if impulses[0].Intensity < 0.8 {
		t.Errorf("Expected high intensity for overdue, got %f", impulses[0].Intensity)
	}

	// Save and reload
	if err := store.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	store2 := NewTaskStore(tmpDir)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(store2.GetPending()) != 1 {
		t.Errorf("Expected 1 task after reload, got %d", len(store2.GetPending()))
	}

	// Complete the task
	store.Complete(task.ID)
	if len(store.GetPending()) != 0 {
		t.Errorf("Expected 0 pending after completion, got %d", len(store.GetPending()))
	}

	t.Log("Task store test passed")
}

func TestIdeaStore(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewIdeaStore(tmpDir)

	// Add an idea
	idea := &Idea{
		Idea:     "Research biological memory consolidation",
		SparkBy:  "conversation about memory",
		Priority: 1,
	}
	store.Add(idea)

	// Check it was added
	if store.Count() != 1 {
		t.Errorf("Expected 1 idea, got %d", store.Count())
	}
	if store.CountUnexplored() != 1 {
		t.Errorf("Expected 1 unexplored idea, got %d", store.CountUnexplored())
	}

	// Generate impulses (only during idle)
	impulses := store.GenerateImpulses(false)
	if len(impulses) != 0 {
		t.Errorf("Expected 0 impulses when not idle, got %d", len(impulses))
	}

	impulses = store.GenerateImpulses(true)
	if len(impulses) != 1 {
		t.Errorf("Expected 1 impulse when idle, got %d", len(impulses))
	}
	if impulses[0].Intensity > 0.5 {
		t.Errorf("Expected low intensity for exploration, got %f", impulses[0].Intensity)
	}

	// Mark explored
	store.MarkExplored(idea.ID, "Learned about synaptic plasticity")
	if store.CountUnexplored() != 0 {
		t.Errorf("Expected 0 unexplored after marking, got %d", store.CountUnexplored())
	}

	// No more impulses after explored
	impulses = store.GenerateImpulses(true)
	if len(impulses) != 0 {
		t.Errorf("Expected 0 impulses after exploration, got %d", len(impulses))
	}

	// Save and reload
	if err := store.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	store2 := NewIdeaStore(tmpDir)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if store2.Count() != 1 {
		t.Errorf("Expected 1 idea after reload, got %d", store2.Count())
	}

	t.Log("Idea store test passed")
}
