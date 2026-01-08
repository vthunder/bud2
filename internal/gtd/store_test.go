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

func TestGTDStore_GetTasks_FilterByWhen(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	store.AddTask(&Task{Title: "Inbox task", When: "inbox"})
	store.AddTask(&Task{Title: "Today task", When: "today"})
	store.AddTask(&Task{Title: "Anytime task", When: "anytime"})

	inboxTasks := store.GetTasks("inbox", "", "")
	if len(inboxTasks) != 1 {
		t.Errorf("Expected 1 inbox task, got %d", len(inboxTasks))
	}

	todayTasks := store.GetTasks("today", "", "")
	if len(todayTasks) != 1 {
		t.Errorf("Expected 1 today task, got %d", len(todayTasks))
	}
}

func TestGTDStore_GetTasks_FilterByProject(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	proj := &Project{Title: "My Project", When: "anytime"}
	store.AddProject(proj)

	store.AddTask(&Task{Title: "Task 1", When: "anytime", Project: proj.ID})
	store.AddTask(&Task{Title: "Task 2", When: "anytime", Project: proj.ID})
	store.AddTask(&Task{Title: "Other task", When: "anytime"})

	projectTasks := store.GetTasks("", proj.ID, "")
	if len(projectTasks) != 2 {
		t.Errorf("Expected 2 project tasks, got %d", len(projectTasks))
	}
}

func TestGTDStore_GetTasks_FilterByArea(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	area := &Area{Title: "Work"}
	store.AddArea(area)

	store.AddTask(&Task{Title: "Work task", When: "anytime", Area: area.ID})
	store.AddTask(&Task{Title: "Personal task", When: "anytime"})

	areaTasks := store.GetTasks("", "", area.ID)
	if len(areaTasks) != 1 {
		t.Errorf("Expected 1 area task, got %d", len(areaTasks))
	}
}

func TestGTDStore_UpdateTask(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	task := &Task{Title: "Original title", When: "inbox"}
	store.AddTask(task)

	task.Title = "Updated title"
	task.When = "today"
	if err := store.UpdateTask(task); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}

	updated := store.GetTask(task.ID)
	if updated.Title != "Updated title" {
		t.Errorf("Expected title 'Updated title', got '%s'", updated.Title)
	}
	if updated.When != "today" {
		t.Errorf("Expected when 'today', got '%s'", updated.When)
	}
}

func TestGTDStore_CompleteTask(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	task := &Task{Title: "Complete me", When: "today"}
	store.AddTask(task)

	if err := store.CompleteTask(task.ID); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	completed := store.GetTask(task.ID)
	if completed.Status != "completed" {
		t.Errorf("Expected status 'completed', got '%s'", completed.Status)
	}
	if completed.CompletedAt == nil {
		t.Error("Expected CompletedAt to be set")
	}
}

func TestGTDStore_CompleteTask_Repeating(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	task := &Task{
		Title:  "Daily standup",
		When:   "today",
		Repeat: "daily",
		Checklist: []ChecklistItem{
			{Text: "Check emails", Done: true},
			{Text: "Review PRs", Done: false},
		},
	}
	store.AddTask(task)
	originalID := task.ID

	if err := store.CompleteTask(task.ID); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	// Original should be completed
	original := store.GetTask(originalID)
	if original.Status != "completed" {
		t.Errorf("Expected original status 'completed', got '%s'", original.Status)
	}

	// Should have created a new occurrence
	allTasks := store.GetTasks("", "", "")
	if len(allTasks) != 2 {
		t.Errorf("Expected 2 tasks (original + new occurrence), got %d", len(allTasks))
	}

	// Find the new task
	var newTask *Task
	for _, tk := range allTasks {
		if tk.ID != originalID {
			newTask = &tk
			break
		}
	}

	if newTask == nil {
		t.Fatal("Expected new occurrence to be created")
	}
	if newTask.Title != "Daily standup" {
		t.Errorf("Expected title 'Daily standup', got '%s'", newTask.Title)
	}
	if newTask.Status != "open" {
		t.Errorf("Expected new task status 'open', got '%s'", newTask.Status)
	}
	// Checklist should be reset
	for _, item := range newTask.Checklist {
		if item.Done {
			t.Error("Expected checklist items to be reset to unchecked")
		}
	}
}

func TestGTDStore_Areas(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	store.AddArea(&Area{Title: "Work"})
	store.AddArea(&Area{Title: "Personal"})

	areas := store.GetAreas()
	if len(areas) != 2 {
		t.Errorf("Expected 2 areas, got %d", len(areas))
	}
}

func TestGTDStore_Projects(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	area := &Area{Title: "Work"}
	store.AddArea(area)

	proj := &Project{Title: "Big Project", When: "anytime", Area: area.ID}
	store.AddProject(proj)

	if proj.ID == "" {
		t.Error("Expected project ID to be set")
	}
	if proj.Status != "open" {
		t.Errorf("Expected status 'open', got '%s'", proj.Status)
	}

	projects := store.GetProjects("", area.ID)
	if len(projects) != 1 {
		t.Errorf("Expected 1 project, got %d", len(projects))
	}

	found := store.GetProject(proj.ID)
	if found == nil {
		t.Fatal("Expected to find project by ID")
	}
	if found.Title != "Big Project" {
		t.Errorf("Expected title 'Big Project', got '%s'", found.Title)
	}
}

func TestGTDStore_GetTask_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	task := store.GetTask("nonexistent")
	if task != nil {
		t.Error("Expected nil for nonexistent task")
	}
}

func TestGTDStore_UpdateTask_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	err := store.UpdateTask(&Task{ID: "nonexistent", Title: "Test"})
	if err == nil {
		t.Error("Expected error for nonexistent task")
	}
}

func TestGTDStore_CompleteTask_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	err := store.CompleteTask("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent task")
	}
}

func TestGTDStore_TaskOrdering(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	// Add tasks - they should get increasing order values
	task1 := &Task{Title: "First", When: "inbox"}
	task2 := &Task{Title: "Second", When: "inbox"}
	task3 := &Task{Title: "Third", When: "inbox"}

	store.AddTask(task1)
	store.AddTask(task2)
	store.AddTask(task3)

	tasks := store.GetTasks("inbox", "", "")
	if len(tasks) != 3 {
		t.Fatalf("Expected 3 tasks, got %d", len(tasks))
	}

	// Should be sorted by order
	if tasks[0].Title != "First" {
		t.Errorf("Expected first task 'First', got '%s'", tasks[0].Title)
	}
	if tasks[1].Title != "Second" {
		t.Errorf("Expected second task 'Second', got '%s'", tasks[1].Title)
	}
	if tasks[2].Title != "Third" {
		t.Errorf("Expected third task 'Third', got '%s'", tasks[2].Title)
	}
}

func TestGTDStore_FindTaskByTitle(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewGTDStore(tmpDir)

	store.AddTask(&Task{Title: "Buy groceries", When: "inbox"})
	store.AddTask(&Task{Title: "Call the doctor", When: "today"})
	store.AddTask(&Task{Title: "Review pull request", When: "today"})

	// Should find by partial match (case-insensitive)
	task := store.FindTaskByTitle("groceries")
	if task == nil {
		t.Fatal("Expected to find task by partial title")
	}
	if task.Title != "Buy groceries" {
		t.Errorf("Expected 'Buy groceries', got '%s'", task.Title)
	}

	// Should find by case-insensitive match
	task = store.FindTaskByTitle("CALL")
	if task == nil {
		t.Fatal("Expected to find task by case-insensitive match")
	}
	if task.Title != "Call the doctor" {
		t.Errorf("Expected 'Call the doctor', got '%s'", task.Title)
	}

	// Should return nil for no match
	task = store.FindTaskByTitle("nonexistent")
	if task != nil {
		t.Error("Expected nil for nonexistent task")
	}

	// Should find first match if multiple matches
	task = store.FindTaskByTitle("the")
	if task == nil {
		t.Fatal("Expected to find task with 'the'")
	}
	// Should return the first task containing "the"
	if task.Title != "Call the doctor" {
		t.Errorf("Expected first match 'Call the doctor', got '%s'", task.Title)
	}
}

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

	// Test when field validation
	// Invalid when value
	invalidWhenTask := &Task{Title: "Test", When: "invalid_value"}
	if err := store.ValidateTask(invalidWhenTask); err == nil {
		t.Error("Expected validation error for invalid when value")
	}

	// Valid when values
	validWhenValues := []string{"inbox", "today", "anytime", "someday", "2024-01-15", ""}
	for _, when := range validWhenValues {
		taskWithWhen := &Task{Title: "Test", When: when}
		// Note: "inbox" tasks can't have project/area, but we're just testing the when value here
		if when == "inbox" || when == "" {
			taskWithWhen.Project = ""
			taskWithWhen.Area = ""
		}
		if err := store.ValidateTask(taskWithWhen); err != nil {
			t.Errorf("Unexpected validation error for when='%s': %v", when, err)
		}
	}

	// Test invalid date format
	invalidDateTask := &Task{Title: "Test", When: "01-15-2024"}
	if err := store.ValidateTask(invalidDateTask); err == nil {
		t.Error("Expected validation error for invalid date format (MM-DD-YYYY)")
	}
}
