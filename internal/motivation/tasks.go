package motivation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/types"
)

// Task represents a commitment ("I will do X")
type Task struct {
	ID         string     `json:"id"`
	Task       string     `json:"task"`
	Due        *time.Time `json:"due,omitempty"`
	Priority   int        `json:"priority"`            // 1 = highest
	Context    string     `json:"context"`             // why this task exists
	Status     string     `json:"status"`              // pending, in_progress, done
	Recurrence string     `json:"recurrence,omitempty"` // "daily", "weekly", "monthly", or duration like "24h"
	LastRun    *time.Time `json:"last_run,omitempty"`   // when this recurring task last ran
}

// TaskStore manages bud_tasks.json (Bud's commitments)
type TaskStore struct {
	path  string
	tasks map[string]*Task
	mu    sync.RWMutex
}

// NewTaskStore creates a new task store
func NewTaskStore(statePath string) *TaskStore {
	return &TaskStore{
		path:  filepath.Join(statePath, "bud_tasks.json"),
		tasks: make(map[string]*Task),
	}
}

// Load reads tasks from file
func (s *TaskStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			// Try to seed from defaults
			s.seedFromDefaults()
			return nil
		}
		return err
	}

	var tasks []*Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return err
	}

	s.tasks = make(map[string]*Task)
	for _, t := range tasks {
		s.tasks[t.ID] = t
	}
	return nil
}

// seedFromDefaults copies seed tasks if available (must hold lock)
func (s *TaskStore) seedFromDefaults() {
	seedPath := "seed/bud_tasks.json"
	data, err := os.ReadFile(seedPath)
	if err != nil {
		return // no seed file, that's fine
	}

	var tasks []*Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return
	}

	s.tasks = make(map[string]*Task)
	for _, t := range tasks {
		s.tasks[t.ID] = t
	}

	// Write directly to create the runtime file (we hold the lock)
	if saveData, err := json.MarshalIndent(tasks, "", "  "); err == nil {
		os.WriteFile(s.path, saveData, 0644)
	}
}

// Save writes tasks to file
func (s *TaskStore) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}

	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// Add adds a new task
func (s *TaskStore) Add(task *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if task.ID == "" {
		task.ID = fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	if task.Status == "" {
		task.Status = "pending"
	}
	s.tasks[task.ID] = task
}

// Get retrieves a task by ID
func (s *TaskStore) Get(id string) *Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tasks[id]
}

// Remove removes a task
func (s *TaskStore) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, id)
}

// Complete marks a task as done, or updates LastRun for recurring tasks
func (s *TaskStore) Complete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		if t.Recurrence != "" {
			// Recurring task: update LastRun, keep status pending
			now := time.Now()
			t.LastRun = &now
			t.Status = "pending"
		} else {
			t.Status = "done"
		}
	}
}

// GetPending returns all pending tasks
func (s *TaskStore) GetPending() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Task
	for _, t := range s.tasks {
		if t.Status == "pending" || t.Status == "in_progress" {
			result = append(result, t)
		}
	}
	return result
}

// GetDue returns tasks that are due or overdue
func (s *TaskStore) GetDue() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var result []*Task
	for _, t := range s.tasks {
		if t.Status != "done" && t.Due != nil && t.Due.Before(now) {
			result = append(result, t)
		}
	}
	return result
}

// GetUpcoming returns tasks due within the given duration
func (s *TaskStore) GetUpcoming(within time.Duration) []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	deadline := now.Add(within)
	var result []*Task
	for _, t := range s.tasks {
		if t.Status != "done" && t.Due != nil && t.Due.After(now) && t.Due.Before(deadline) {
			result = append(result, t)
		}
	}
	return result
}

// parseRecurrence converts recurrence string to duration
func parseRecurrence(rec string) time.Duration {
	switch rec {
	case "hourly":
		return time.Hour
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour // approximate
	default:
		// Try parsing as duration string (e.g., "24h", "12h")
		if d, err := time.ParseDuration(rec); err == nil {
			return d
		}
		return 0
	}
}

// GetRecurringDue returns recurring tasks that are due based on their interval
func (s *TaskStore) GetRecurringDue() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var result []*Task
	for _, t := range s.tasks {
		if t.Recurrence == "" || t.Status == "done" {
			continue
		}

		interval := parseRecurrence(t.Recurrence)
		if interval == 0 {
			continue
		}

		// If never run, it's due
		if t.LastRun == nil {
			result = append(result, t)
			continue
		}

		// If interval has passed since last run, it's due
		if now.Sub(*t.LastRun) >= interval {
			result = append(result, t)
		}
	}
	return result
}

// GenerateImpulses creates impulses for due and upcoming tasks
func (s *TaskStore) GenerateImpulses() []*types.Impulse {
	var impulses []*types.Impulse

	// High priority for overdue tasks
	for _, t := range s.GetDue() {
		impulses = append(impulses, &types.Impulse{
			ID:          fmt.Sprintf("impulse-task-due-%s", t.ID),
			Source:      types.ImpulseTask,
			Type:        "due",
			Intensity:   0.9, // overdue is urgent
			Timestamp:   time.Now(),
			Description: fmt.Sprintf("OVERDUE: %s", t.Task),
			Data: map[string]any{
				"task_id":  t.ID,
				"task":     t.Task,
				"due":      t.Due,
				"priority": t.Priority,
				"context":  t.Context,
			},
		})
	}

	// Medium priority for upcoming tasks (within 1 hour)
	for _, t := range s.GetUpcoming(time.Hour) {
		impulses = append(impulses, &types.Impulse{
			ID:          fmt.Sprintf("impulse-task-upcoming-%s", t.ID),
			Source:      types.ImpulseTask,
			Type:        "upcoming",
			Intensity:   0.6,
			Timestamp:   time.Now(),
			Description: fmt.Sprintf("Coming up: %s (due %s)", t.Task, t.Due.Format("15:04")),
			Data: map[string]any{
				"task_id":  t.ID,
				"task":     t.Task,
				"due":      t.Due,
				"priority": t.Priority,
				"context":  t.Context,
			},
		})
	}

	// Medium-low priority for recurring tasks that are due
	for _, t := range s.GetRecurringDue() {
		var lastRunStr string
		if t.LastRun != nil {
			lastRunStr = t.LastRun.Format("2006-01-02 15:04")
		} else {
			lastRunStr = "never"
		}
		impulses = append(impulses, &types.Impulse{
			ID:          fmt.Sprintf("impulse-task-recurring-%s", t.ID),
			Source:      types.ImpulseTask,
			Type:        "recurring",
			Intensity:   0.5, // lower than due/upcoming, but still notable
			Timestamp:   time.Now(),
			Description: fmt.Sprintf("Recurring (%s): %s (last: %s)", t.Recurrence, t.Task, lastRunStr),
			Data: map[string]any{
				"task_id":    t.ID,
				"task":       t.Task,
				"recurrence": t.Recurrence,
				"last_run":   t.LastRun,
				"priority":   t.Priority,
				"context":    t.Context,
			},
		})
	}

	return impulses
}
