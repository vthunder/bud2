package gtd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const storeFilename = "user_tasks.json"

// GTDStore manages GTD data with thread-safe operations
type GTDStore struct {
	path string
	data Store
	mu   sync.RWMutex
}

// NewGTDStore creates a new GTD store with the given state directory path
func NewGTDStore(statePath string) *GTDStore {
	return &GTDStore{
		path: filepath.Join(statePath, storeFilename),
		data: Store{
			Areas:    []Area{},
			Projects: []Project{},
			Tasks:    []Task{},
		},
	}
}

// Load reads the GTD data from disk
func (s *GTDStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		// File doesn't exist yet, start with empty store
		s.data = Store{
			Areas:    []Area{},
			Projects: []Project{},
			Tasks:    []Task{},
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read GTD store: %w", err)
	}

	if err := json.Unmarshal(data, &s.data); err != nil {
		return fmt.Errorf("failed to parse GTD store: %w", err)
	}

	// Ensure slices are not nil
	if s.data.Areas == nil {
		s.data.Areas = []Area{}
	}
	if s.data.Projects == nil {
		s.data.Projects = []Project{}
	}
	if s.data.Tasks == nil {
		s.data.Tasks = []Task{}
	}

	return nil
}

// Save writes the GTD data to disk
func (s *GTDStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal GTD store: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0644); err != nil {
		return fmt.Errorf("failed to write GTD store: %w", err)
	}

	return nil
}

// generateID creates a unique ID based on current timestamp
// idCounter ensures unique IDs even when called within the same nanosecond
var idCounter int64

func generateID() string {
	count := atomic.AddInt64(&idCounter, 1)
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), count)
}

// AddArea adds a new area to the store
func (s *GTDStore) AddArea(area *Area) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if area.ID == "" {
		area.ID = generateID()
	}
	s.data.Areas = append(s.data.Areas, *area)
}

// GetAreas returns all areas
func (s *GTDStore) GetAreas() []Area {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Area, len(s.data.Areas))
	copy(result, s.data.Areas)
	return result
}

// GetArea returns an area by ID
func (s *GTDStore) GetArea(id string) *Area {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range s.data.Areas {
		if s.data.Areas[i].ID == id {
			a := s.data.Areas[i]
			return &a
		}
	}
	return nil
}

// UpdateArea updates an existing area
func (s *GTDStore) UpdateArea(area *Area) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Areas {
		if s.data.Areas[i].ID == area.ID {
			s.data.Areas[i] = *area
			return nil
		}
	}
	return fmt.Errorf("area not found: %s", area.ID)
}

// AddProject adds a new project to the store
func (s *GTDStore) AddProject(project *Project) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if project.ID == "" {
		project.ID = generateID()
	}
	if project.Status == "" {
		project.Status = "open"
	}
	if project.Order == 0 {
		project.Order = float64(time.Now().UnixNano())
	}
	if project.Headings == nil {
		project.Headings = []string{}
	}
	s.data.Projects = append(s.data.Projects, *project)
}

// GetProjects returns projects filtered by when and/or area
func (s *GTDStore) GetProjects(when, areaID string) []Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Project
	for _, p := range s.data.Projects {
		if when != "" && p.When != when {
			continue
		}
		if areaID != "" && p.Area != areaID {
			continue
		}
		result = append(result, p)
	}

	// Sort by order
	sort.Slice(result, func(i, j int) bool {
		return result[i].Order < result[j].Order
	})

	return result
}

// GetProject returns a project by ID
func (s *GTDStore) GetProject(id string) *Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range s.data.Projects {
		if s.data.Projects[i].ID == id {
			p := s.data.Projects[i]
			return &p
		}
	}
	return nil
}

// UpdateProject updates an existing project
func (s *GTDStore) UpdateProject(project *Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Projects {
		if s.data.Projects[i].ID == project.ID {
			s.data.Projects[i] = *project
			return nil
		}
	}
	return fmt.Errorf("project not found: %s", project.ID)
}

// AddTask adds a new task to the store
func (s *GTDStore) AddTask(task *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if task.ID == "" {
		task.ID = generateID()
	}
	if task.Status == "" {
		task.Status = "open"
	}
	if task.When == "" {
		task.When = "inbox"
	}
	if task.Order == 0 {
		task.Order = float64(time.Now().UnixNano())
	}
	s.data.Tasks = append(s.data.Tasks, *task)
}

// GetTask returns a task by ID
func (s *GTDStore) GetTask(id string) *Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range s.data.Tasks {
		if s.data.Tasks[i].ID == id {
			t := s.data.Tasks[i]
			return &t
		}
	}
	return nil
}

// GetTasks returns tasks filtered by when, project, and/or area
func (s *GTDStore) GetTasks(when, projectID, areaID string) []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Task
	for _, t := range s.data.Tasks {
		if when != "" && t.When != when {
			continue
		}
		if projectID != "" && t.Project != projectID {
			continue
		}
		if areaID != "" && t.Area != areaID {
			continue
		}
		result = append(result, t)
	}

	// Sort by order
	sort.Slice(result, func(i, j int) bool {
		return result[i].Order < result[j].Order
	})

	return result
}

// UpdateTask updates an existing task
func (s *GTDStore) UpdateTask(task *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Tasks {
		if s.data.Tasks[i].ID == task.ID {
			s.data.Tasks[i] = *task
			return nil
		}
	}
	return fmt.Errorf("task not found: %s", task.ID)
}

// CompleteTask marks a task as completed and creates next occurrence for repeating tasks
func (s *GTDStore) CompleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var taskIndex = -1
	for i := range s.data.Tasks {
		if s.data.Tasks[i].ID == id {
			taskIndex = i
			break
		}
	}

	if taskIndex == -1 {
		return fmt.Errorf("task not found: %s", id)
	}

	task := &s.data.Tasks[taskIndex]
	now := time.Now()
	task.Status = "completed"
	task.CompletedAt = &now

	// If repeating, create next occurrence
	if task.Repeat != "" {
		nextTask := s.createNextOccurrence(task)
		s.data.Tasks = append(s.data.Tasks, *nextTask)
	}

	return nil
}

// createNextOccurrence creates the next occurrence of a repeating task
func (s *GTDStore) createNextOccurrence(task *Task) *Task {
	next := &Task{
		ID:        generateID(),
		Title:     task.Title,
		Notes:     task.Notes,
		Checklist: resetChecklist(task.Checklist),
		When:      task.When,
		Project:   task.Project,
		Heading:   task.Heading,
		Area:      task.Area,
		Repeat:    task.Repeat,
		Status:    "open",
		Order:     float64(time.Now().UnixNano()),
	}

	// If When was a specific date, calculate next date
	if len(task.When) == 10 && task.When[4] == '-' { // YYYY-MM-DD format
		baseDate, err := time.Parse("2006-01-02", task.When)
		if err == nil {
			nextDate := calculateNextDate(baseDate, task.Repeat)
			next.When = nextDate.Format("2006-01-02")
		}
	}

	return next
}

// resetChecklist returns a copy of the checklist with all items unchecked
func resetChecklist(items []ChecklistItem) []ChecklistItem {
	if items == nil {
		return nil
	}
	result := make([]ChecklistItem, len(items))
	for i, item := range items {
		result[i] = ChecklistItem{
			Text: item.Text,
			Done: false,
		}
	}
	return result
}

// calculateNextDate calculates the next occurrence date based on repeat pattern
func calculateNextDate(base time.Time, repeat string) time.Time {
	switch repeat {
	case "daily":
		return base.AddDate(0, 0, 1)
	case "weekly":
		return base.AddDate(0, 0, 7)
	case "biweekly":
		return base.AddDate(0, 0, 14)
	case "monthly":
		return base.AddDate(0, 1, 0)
	case "quarterly":
		return base.AddDate(0, 3, 0)
	case "yearly":
		return base.AddDate(1, 0, 0)
	default:
		// Default to daily if unknown
		return base.AddDate(0, 0, 1)
	}
}

// FindTaskByTitle finds a task by partial title match (case-insensitive)
func (s *GTDStore) FindTaskByTitle(title string) *Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	title = strings.ToLower(title)
	for i := range s.data.Tasks {
		if strings.Contains(strings.ToLower(s.data.Tasks[i].Title), title) {
			task := s.data.Tasks[i]
			return &task
		}
	}
	return nil
}
