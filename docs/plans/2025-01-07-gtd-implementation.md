# GTD Task System Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add GTD-style task management for owner's tasks, separate from Bud's commitments.

**Architecture:** New `internal/gtd/` package with store, types, and validation. MCP tools for Bud to manage tasks. Existing bud tasks renamed to clarify ownership.

**Tech Stack:** Go, JSON file storage, MCP tool registration pattern from existing motivation package.

---

## Task 1: Rename tasks.json to bud_tasks.json

**Files:**
- Modify: `internal/motivation/tasks.go:34`

**Step 1: Update TaskStore path**

In `internal/motivation/tasks.go`, change line 34:

```go
// Before:
path:  filepath.Join(statePath, "tasks.json"),

// After:
path:  filepath.Join(statePath, "bud_tasks.json"),
```

**Step 2: Rename existing file (if exists)**

```bash
mv state/tasks.json state/bud_tasks.json 2>/dev/null || true
```

**Step 3: Run tests**

```bash
go test ./internal/motivation/... -v
```

Expected: PASS

**Step 4: Commit**

```bash
git add internal/motivation/tasks.go
git commit -m "refactor: rename tasks.json to bud_tasks.json"
```

---

## Task 2: Rename MCP task tools to bud_task

**Files:**
- Modify: `internal/mcp/server.go:358-404`
- Modify: `cmd/bud-mcp/main.go:325-390`

**Step 1: Update tool definitions in server.go**

Change tool names in the tools list (around line 358):

```go
// Before:
Name:        "add_task",
Description: "Add a task (commitment) to your task queue. Use this to track things you've committed to do.",

// After:
Name:        "add_bud_task",
Description: "Add a task (Bud's commitment) to your task queue. Use this to track things you've committed to do.",
```

```go
// Before:
Name:        "list_tasks",
Description: "List pending tasks. Use this to see what you've committed to do.",

// After:
Name:        "list_bud_tasks",
Description: "List pending Bud tasks. Use this to see what you've committed to do.",
```

```go
// Before:
Name:        "complete_task",
Description: "Mark a task as complete.",

// After:
Name:        "complete_bud_task",
Description: "Mark a Bud task as complete.",
```

**Step 2: Update tool registrations in bud-mcp/main.go**

Change RegisterTool calls (around line 325):

```go
// Before:
server.RegisterTool("add_task", func(ctx any, args map[string]any) (string, error) {

// After:
server.RegisterTool("add_bud_task", func(ctx any, args map[string]any) (string, error) {
```

```go
// Before:
server.RegisterTool("list_tasks", func(ctx any, args map[string]any) (string, error) {

// After:
server.RegisterTool("list_bud_tasks", func(ctx any, args map[string]any) (string, error) {
```

```go
// Before:
server.RegisterTool("complete_task", func(ctx any, args map[string]any) (string, error) {

// After:
server.RegisterTool("complete_bud_task", func(ctx any, args map[string]any) (string, error) {
```

**Step 3: Build to verify**

```bash
go build ./...
```

Expected: Success

**Step 4: Commit**

```bash
git add internal/mcp/server.go cmd/bud-mcp/main.go
git commit -m "refactor: rename task tools to bud_task for clarity"
```

---

## Task 3: Create GTD types

**Files:**
- Create: `internal/gtd/types.go`

**Step 1: Create the gtd package directory**

```bash
mkdir -p internal/gtd
```

**Step 2: Write the types file**

Create `internal/gtd/types.go`:

```go
package gtd

import "time"

// ChecklistItem represents a sub-task within a task
type ChecklistItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// Task represents a GTD task (owner's task, not Bud's commitment)
type Task struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Notes       string          `json:"notes,omitempty"`
	Checklist   []ChecklistItem `json:"checklist,omitempty"`
	When        string          `json:"when"`                   // inbox, today, anytime, someday, or YYYY-MM-DD
	Project     string          `json:"project,omitempty"`      // project ID
	Heading     string          `json:"heading,omitempty"`      // heading name within project
	Area        string          `json:"area,omitempty"`         // area ID (only if not in project)
	Repeat      string          `json:"repeat,omitempty"`       // daily, weekly, monthly, etc.
	Status      string          `json:"status"`                 // open, completed, canceled
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	Order       float64         `json:"order"`
}

// Project represents a GTD project (multi-step outcome)
type Project struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Notes    string   `json:"notes,omitempty"`
	When     string   `json:"when"`     // anytime, someday, or YYYY-MM-DD
	Area     string   `json:"area"`     // area ID
	Headings []string `json:"headings"` // ordered list of heading names
	Status   string   `json:"status"`   // open, completed, canceled
	Order    float64  `json:"order"`
}

// Area represents a GTD area of responsibility
type Area struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Store represents the complete GTD data
type Store struct {
	Areas    []Area    `json:"areas"`
	Projects []Project `json:"projects"`
	Tasks    []Task    `json:"tasks"`
}
```

**Step 3: Build to verify**

```bash
go build ./internal/gtd/...
```

Expected: Success

**Step 4: Commit**

```bash
git add internal/gtd/types.go
git commit -m "feat(gtd): add GTD data types"
```

---

## Task 4: Create GTD store with Load/Save

**Files:**
- Create: `internal/gtd/store.go`
- Create: `internal/gtd/store_test.go`

**Step 1: Write the failing test**

Create `internal/gtd/store_test.go`:

```go
package gtd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGTDStore_LoadSave(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	// Should start empty
	if err := store.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(store.data.Areas) != 0 {
		t.Errorf("Expected 0 areas, got %d", len(store.data.Areas))
	}

	// Add data
	store.AddArea(&Area{Title: "Work"})
	store.AddArea(&Area{Title: "Life"})

	// Save
	if err := store.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists
	path := filepath.Join(tmpDir, "user_tasks.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("user_tasks.json not created")
	}

	// Load into new store
	store2 := NewGTDStore(tmpDir)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(store2.data.Areas) != 2 {
		t.Errorf("Expected 2 areas, got %d", len(store2.data.Areas))
	}
}

func TestGTDStore_AddTask(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	task := &Task{
		Title: "Buy milk",
		When:  "inbox",
	}
	store.AddTask(task)

	if task.ID == "" {
		t.Error("Expected task ID to be set")
	}
	if task.Status != "open" {
		t.Errorf("Expected status 'open', got '%s'", task.Status)
	}
	if task.Order == 0 {
		t.Error("Expected order to be set")
	}

	tasks := store.GetTasks("inbox", "", "")
	if len(tasks) != 1 {
		t.Errorf("Expected 1 task, got %d", len(tasks))
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/gtd/... -v
```

Expected: FAIL (NewGTDStore not defined)

**Step 3: Write the store implementation**

Create `internal/gtd/store.go`:

```go
package gtd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// GTDStore manages the GTD data file
type GTDStore struct {
	path string
	data Store
	mu   sync.RWMutex
}

// NewGTDStore creates a new GTD store
func NewGTDStore(statePath string) *GTDStore {
	return &GTDStore{
		path: filepath.Join(statePath, "user_tasks.json"),
		data: Store{
			Areas:    []Area{},
			Projects: []Project{},
			Tasks:    []Task{},
		},
	}
}

// Load reads data from file
func (s *GTDStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return json.Unmarshal(data, &s.data)
}

// Save writes data to file
func (s *GTDStore) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// AddArea adds a new area
func (s *GTDStore) AddArea(area *Area) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if area.ID == "" {
		area.ID = fmt.Sprintf("area-%d", time.Now().UnixNano())
	}
	s.data.Areas = append(s.data.Areas, *area)
}

// GetAreas returns all areas
func (s *GTDStore) GetAreas() []Area {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Areas
}

// AddProject adds a new project
func (s *GTDStore) AddProject(project *Project) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if project.ID == "" {
		project.ID = fmt.Sprintf("proj-%d", time.Now().UnixNano())
	}
	if project.Status == "" {
		project.Status = "open"
	}
	if project.When == "" {
		project.When = "anytime"
	}
	if project.Order == 0 {
		project.Order = float64(time.Now().UnixNano())
	}
	s.data.Projects = append(s.data.Projects, *project)
}

// GetProjects returns projects, optionally filtered by area
func (s *GTDStore) GetProjects(area string) []Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Project
	for _, p := range s.data.Projects {
		if p.Status == "open" && (area == "" || p.Area == area) {
			result = append(result, p)
		}
	}
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
			return &s.data.Projects[i]
		}
	}
	return nil
}

// AddTask adds a new task
func (s *GTDStore) AddTask(task *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if task.ID == "" {
		task.ID = fmt.Sprintf("gtd-%d", time.Now().UnixNano())
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
			return &s.data.Tasks[i]
		}
	}
	return nil
}

// GetTasks returns tasks filtered by when, project, and/or area
func (s *GTDStore) GetTasks(when, project, area string) []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Task
	for _, t := range s.data.Tasks {
		if t.Status != "open" {
			continue
		}
		if when != "" && t.When != when {
			continue
		}
		if project != "" && t.Project != project {
			continue
		}
		if area != "" && t.Area != area && t.Project == "" {
			// Only filter by area if task is not in a project
			continue
		}
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Order < result[j].Order
	})
	return result
}

// UpdateTask updates a task by ID
func (s *GTDStore) UpdateTask(id string, updates map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Tasks {
		if s.data.Tasks[i].ID == id {
			t := &s.data.Tasks[i]
			if title, ok := updates["title"].(string); ok {
				t.Title = title
			}
			if notes, ok := updates["notes"].(string); ok {
				t.Notes = notes
			}
			if when, ok := updates["when"].(string); ok {
				t.When = when
			}
			if project, ok := updates["project"].(string); ok {
				t.Project = project
			}
			if heading, ok := updates["heading"].(string); ok {
				t.Heading = heading
			}
			if area, ok := updates["area"].(string); ok {
				t.Area = area
			}
			if order, ok := updates["order"].(float64); ok {
				t.Order = order
			}
			return nil
		}
	}
	return fmt.Errorf("task not found: %s", id)
}

// CompleteTask marks a task as completed
func (s *GTDStore) CompleteTask(id string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Tasks {
		if s.data.Tasks[i].ID == id {
			t := &s.data.Tasks[i]
			t.Status = "completed"
			now := time.Now()
			t.CompletedAt = &now

			// Handle repeating tasks
			if t.Repeat != "" {
				next := s.createNextOccurrence(t)
				s.data.Tasks = append(s.data.Tasks, *next)
			}
			return t, nil
		}
	}
	return nil, fmt.Errorf("task not found: %s", id)
}

// createNextOccurrence creates the next occurrence of a repeating task
func (s *GTDStore) createNextOccurrence(t *Task) *Task {
	next := &Task{
		ID:        fmt.Sprintf("gtd-%d", time.Now().UnixNano()),
		Title:     t.Title,
		Notes:     t.Notes,
		Checklist: resetChecklist(t.Checklist),
		When:      t.When,
		Project:   t.Project,
		Heading:   t.Heading,
		Area:      t.Area,
		Repeat:    t.Repeat,
		Status:    "open",
		Order:     float64(time.Now().UnixNano()),
	}
	return next
}

func resetChecklist(items []ChecklistItem) []ChecklistItem {
	result := make([]ChecklistItem, len(items))
	for i, item := range items {
		result[i] = ChecklistItem{Text: item.Text, Done: false}
	}
	return result
}
```

**Step 4: Run tests**

```bash
go test ./internal/gtd/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/gtd/store.go internal/gtd/store_test.go
git commit -m "feat(gtd): add GTD store with Load/Save"
```

---

## Task 5: Add GTD validation

**Files:**
- Create: `internal/gtd/validation.go`
- Modify: `internal/gtd/store_test.go`

**Step 1: Write the failing test**

Add to `internal/gtd/store_test.go`:

```go
func TestGTDStore_Validation(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	// Add an area and project for testing
	store.AddArea(&Area{ID: "work", Title: "Work"})
	store.AddProject(&Project{ID: "proj1", Title: "Project 1", Area: "work", Headings: []string{"Phase 1"}})

	// Inbox task cannot have project/area
	task := &Task{Title: "Test", When: "inbox", Project: "proj1"}
	if err := store.ValidateTask(task); err == nil {
		t.Error("Expected validation error for inbox task with project")
	}

	// Task with heading must have project
	task2 := &Task{Title: "Test", When: "today", Heading: "Phase 1"}
	if err := store.ValidateTask(task2); err == nil {
		t.Error("Expected validation error for task with heading but no project")
	}

	// Valid task
	task3 := &Task{Title: "Test", When: "today", Project: "proj1", Heading: "Phase 1"}
	if err := store.ValidateTask(task3); err != nil {
		t.Errorf("Unexpected validation error: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/gtd/... -v -run TestGTDStore_Validation
```

Expected: FAIL (ValidateTask not defined)

**Step 3: Write the validation implementation**

Create `internal/gtd/validation.go`:

```go
package gtd

import "fmt"

// ValidateTask validates a task against GTD rules
func (s *GTDStore) ValidateTask(task *Task) error {
	// Inbox tasks cannot have project, area, or heading
	if task.When == "inbox" {
		if task.Project != "" {
			return fmt.Errorf("inbox tasks cannot be in a project")
		}
		if task.Area != "" {
			return fmt.Errorf("inbox tasks cannot be in an area")
		}
		if task.Heading != "" {
			return fmt.Errorf("inbox tasks cannot have a heading")
		}
	}

	// Task with heading must have a project
	if task.Heading != "" && task.Project == "" {
		return fmt.Errorf("task with heading must be in a project")
	}

	// If project is set, verify it exists and heading is valid
	if task.Project != "" {
		project := s.GetProject(task.Project)
		if project == nil {
			return fmt.Errorf("project not found: %s", task.Project)
		}

		// Verify heading exists in project
		if task.Heading != "" {
			found := false
			for _, h := range project.Headings {
				if h == task.Heading {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("heading '%s' not found in project '%s'", task.Heading, project.Title)
			}
		}
	}

	// If area is set, verify it exists
	if task.Area != "" {
		found := false
		for _, a := range s.data.Areas {
			if a.ID == task.Area {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("area not found: %s", task.Area)
		}
	}

	return nil
}

// ValidateProject validates a project
func (s *GTDStore) ValidateProject(project *Project) error {
	// Project must have an area
	if project.Area == "" {
		return fmt.Errorf("project must have an area")
	}

	// Verify area exists
	found := false
	for _, a := range s.data.Areas {
		if a.ID == project.Area {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("area not found: %s", project.Area)
	}

	return nil
}
```

**Step 4: Run tests**

```bash
go test ./internal/gtd/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/gtd/validation.go internal/gtd/store_test.go
git commit -m "feat(gtd): add validation rules"
```

---

## Task 6: Register GTD MCP tools - gtd_add and gtd_list

**Files:**
- Modify: `internal/mcp/server.go`
- Modify: `cmd/bud-mcp/main.go`

**Step 1: Add tool definitions to server.go**

Add after the existing tool definitions (around line 450):

```go
		// GTD tools
		{
			Name:        "gtd_add",
			Description: "Add a task to your GTD system. Defaults to inbox if 'when' not specified.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"title": {
						Type:        "string",
						Description: "Task title",
					},
					"notes": {
						Type:        "string",
						Description: "Additional notes (optional)",
					},
					"when": {
						Type:        "string",
						Description: "When to do it: inbox, today, anytime, someday, or YYYY-MM-DD (default: inbox)",
					},
					"project": {
						Type:        "string",
						Description: "Project ID (optional)",
					},
					"area": {
						Type:        "string",
						Description: "Area ID (optional, only if not in project)",
					},
				},
				Required: []string{"title"},
			},
		},
		{
			Name:        "gtd_list",
			Description: "List tasks from your GTD system with optional filters.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"when": {
						Type:        "string",
						Description: "Filter by when: inbox, today, anytime, someday (optional)",
					},
					"project": {
						Type:        "string",
						Description: "Filter by project ID (optional)",
					},
					"area": {
						Type:        "string",
						Description: "Filter by area ID (optional)",
					},
				},
			},
		},
```

**Step 2: Add handler registrations to bud-mcp/main.go**

First, add the import and initialize GTD store after taskStore initialization:

```go
// Add import
"github.com/vthunder/bud2/internal/gtd"

// After taskStore initialization (around line 320):
gtdStore := gtd.NewGTDStore(statePath)
gtdStore.Load()
```

Then register the tools:

```go
	// Register gtd_add tool
	server.RegisterTool("gtd_add", func(ctx any, args map[string]any) (string, error) {
		title, ok := args["title"].(string)
		if !ok || title == "" {
			return "", fmt.Errorf("title is required")
		}

		task := &gtd.Task{
			Title: title,
			When:  "inbox",
		}

		if notes, ok := args["notes"].(string); ok {
			task.Notes = notes
		}
		if when, ok := args["when"].(string); ok && when != "" {
			task.When = when
		}
		if project, ok := args["project"].(string); ok {
			task.Project = project
		}
		if area, ok := args["area"].(string); ok {
			task.Area = area
		}

		if err := gtdStore.ValidateTask(task); err != nil {
			return "", fmt.Errorf("validation failed: %w", err)
		}

		gtdStore.AddTask(task)
		if err := gtdStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("GTD: Added task '%s' to %s", title, task.When)
		return fmt.Sprintf("Added to %s: %s (ID: %s)", task.When, title, task.ID), nil
	})

	// Register gtd_list tool
	server.RegisterTool("gtd_list", func(ctx any, args map[string]any) (string, error) {
		when, _ := args["when"].(string)
		project, _ := args["project"].(string)
		area, _ := args["area"].(string)

		tasks := gtdStore.GetTasks(when, project, area)
		if len(tasks) == 0 {
			if when != "" {
				return fmt.Sprintf("No tasks in %s.", when), nil
			}
			return "No tasks found.", nil
		}

		data, _ := json.MarshalIndent(tasks, "", "  ")
		return string(data), nil
	})
```

**Step 3: Build to verify**

```bash
go build ./...
```

Expected: Success

**Step 4: Commit**

```bash
git add internal/mcp/server.go cmd/bud-mcp/main.go
git commit -m "feat(gtd): add gtd_add and gtd_list MCP tools"
```

---

## Task 7: Register GTD MCP tools - gtd_update and gtd_complete

**Files:**
- Modify: `internal/mcp/server.go`
- Modify: `cmd/bud-mcp/main.go`

**Step 1: Add tool definitions to server.go**

```go
		{
			Name:        "gtd_update",
			Description: "Update a GTD task - move it, change title, etc.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"id": {
						Type:        "string",
						Description: "Task ID",
					},
					"title": {
						Type:        "string",
						Description: "New title (optional)",
					},
					"notes": {
						Type:        "string",
						Description: "New notes (optional)",
					},
					"when": {
						Type:        "string",
						Description: "Move to: inbox, today, anytime, someday, or YYYY-MM-DD (optional)",
					},
					"project": {
						Type:        "string",
						Description: "Move to project ID (optional)",
					},
					"area": {
						Type:        "string",
						Description: "Move to area ID (optional)",
					},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "gtd_complete",
			Description: "Mark a GTD task as complete. Creates next occurrence for repeating tasks.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"id": {
						Type:        "string",
						Description: "Task ID",
					},
				},
				Required: []string{"id"},
			},
		},
```

**Step 2: Add handler registrations to bud-mcp/main.go**

```go
	// Register gtd_update tool
	server.RegisterTool("gtd_update", func(ctx any, args map[string]any) (string, error) {
		id, ok := args["id"].(string)
		if !ok || id == "" {
			return "", fmt.Errorf("id is required")
		}

		task := gtdStore.GetTask(id)
		if task == nil {
			return "", fmt.Errorf("task not found: %s", id)
		}

		updates := make(map[string]any)
		for _, field := range []string{"title", "notes", "when", "project", "area", "heading"} {
			if val, ok := args[field]; ok {
				updates[field] = val
			}
		}

		if err := gtdStore.UpdateTask(id, updates); err != nil {
			return "", err
		}

		if err := gtdStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("GTD: Updated task %s", id)
		return fmt.Sprintf("Updated task: %s", id), nil
	})

	// Register gtd_complete tool
	server.RegisterTool("gtd_complete", func(ctx any, args map[string]any) (string, error) {
		id, ok := args["id"].(string)
		if !ok || id == "" {
			return "", fmt.Errorf("id is required")
		}

		task, err := gtdStore.CompleteTask(id)
		if err != nil {
			return "", err
		}

		if err := gtdStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		result := fmt.Sprintf("Completed: %s", task.Title)
		if task.Repeat != "" {
			result += " (next occurrence created)"
		}

		log.Printf("GTD: Completed task %s", id)
		return result, nil
	})
```

**Step 3: Build to verify**

```bash
go build ./...
```

Expected: Success

**Step 4: Commit**

```bash
git add internal/mcp/server.go cmd/bud-mcp/main.go
git commit -m "feat(gtd): add gtd_update and gtd_complete MCP tools"
```

---

## Task 8: Register GTD MCP tools - gtd_areas and gtd_projects

**Files:**
- Modify: `internal/mcp/server.go`
- Modify: `cmd/bud-mcp/main.go`

**Step 1: Add tool definitions to server.go**

```go
		{
			Name:        "gtd_areas",
			Description: "List or create GTD areas (e.g., Work, Life).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"action": {
						Type:        "string",
						Description: "Action: list or create (default: list)",
					},
					"title": {
						Type:        "string",
						Description: "Area title (required for create)",
					},
				},
			},
		},
		{
			Name:        "gtd_projects",
			Description: "List or create GTD projects.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"action": {
						Type:        "string",
						Description: "Action: list or create (default: list)",
					},
					"area": {
						Type:        "string",
						Description: "Filter by area ID (for list) or area ID for new project (for create)",
					},
					"title": {
						Type:        "string",
						Description: "Project title (required for create)",
					},
					"headings": {
						Type:        "string",
						Description: "Comma-separated heading names (optional, for create)",
					},
				},
			},
		},
```

**Step 2: Add handler registrations to bud-mcp/main.go**

```go
	// Register gtd_areas tool
	server.RegisterTool("gtd_areas", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			areas := gtdStore.GetAreas()
			if len(areas) == 0 {
				return "No areas defined. Create one with action='create' and title='Work'.", nil
			}
			data, _ := json.MarshalIndent(areas, "", "  ")
			return string(data), nil

		case "create":
			title, ok := args["title"].(string)
			if !ok || title == "" {
				return "", fmt.Errorf("title is required for create")
			}

			area := &gtd.Area{Title: title}
			gtdStore.AddArea(area)
			if err := gtdStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save: %w", err)
			}

			log.Printf("GTD: Created area '%s'", title)
			return fmt.Sprintf("Created area: %s (ID: %s)", title, area.ID), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	// Register gtd_projects tool
	server.RegisterTool("gtd_projects", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			area, _ := args["area"].(string)
			projects := gtdStore.GetProjects(area)
			if len(projects) == 0 {
				return "No projects found.", nil
			}
			data, _ := json.MarshalIndent(projects, "", "  ")
			return string(data), nil

		case "create":
			title, ok := args["title"].(string)
			if !ok || title == "" {
				return "", fmt.Errorf("title is required for create")
			}
			area, ok := args["area"].(string)
			if !ok || area == "" {
				return "", fmt.Errorf("area is required for create")
			}

			project := &gtd.Project{
				Title: title,
				Area:  area,
				When:  "anytime",
			}

			if headingsStr, ok := args["headings"].(string); ok && headingsStr != "" {
				project.Headings = strings.Split(headingsStr, ",")
				for i := range project.Headings {
					project.Headings[i] = strings.TrimSpace(project.Headings[i])
				}
			}

			if err := gtdStore.ValidateProject(project); err != nil {
				return "", fmt.Errorf("validation failed: %w", err)
			}

			gtdStore.AddProject(project)
			if err := gtdStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save: %w", err)
			}

			log.Printf("GTD: Created project '%s'", title)
			return fmt.Sprintf("Created project: %s (ID: %s)", title, project.ID), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})
```

**Step 3: Build to verify**

```bash
go build ./...
```

Expected: Success

**Step 4: Commit**

```bash
git add internal/mcp/server.go cmd/bud-mcp/main.go
git commit -m "feat(gtd): add gtd_areas and gtd_projects MCP tools"
```

---

## Task 9: Create initial user_tasks.json

**Files:**
- Create: `state/user_tasks.json`

**Step 1: Create the initial file**

Create `state/user_tasks.json`:

```json
{
  "areas": [],
  "projects": [],
  "tasks": []
}
```

**Step 2: Commit**

```bash
git add state/user_tasks.json
git commit -m "feat(gtd): add initial user_tasks.json"
```

---

## Task 10: Run all tests and final verification

**Step 1: Run all tests**

```bash
go test ./... -v
```

Expected: All PASS

**Step 2: Build all binaries**

```bash
go build ./...
```

Expected: Success

**Step 3: Final commit (if any uncommitted changes)**

```bash
git status
# If clean, proceed. If not, commit remaining changes.
```

---

## Summary

This plan implements:
1. Renamed bud_tasks (Bud's commitments)
2. New GTD system with Areas, Projects, Tasks
3. MCP tools: gtd_add, gtd_list, gtd_update, gtd_complete, gtd_areas, gtd_projects
4. Validation rules for GTD constraints
5. Repeating task support

**Not included in this plan (Phase 4 from design):**
- GTD guide in core memory
- Scheduled reviews (morning/evening/weekly)
- Impulse generation for Today tasks

These can be added in a follow-up implementation.
