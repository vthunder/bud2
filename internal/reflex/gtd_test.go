package reflex

import (
	"context"
	"strings"
	"testing"

	"github.com/vthunder/bud2/internal/gtd"
)

func TestGTDActions(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	gtdStore := gtd.NewGTDStore(tmpDir)
	engine.SetGTDStore(gtdStore)

	ctx := context.Background()

	// Test gtd_add
	t.Run("gtd_add", func(t *testing.T) {
		action, ok := engine.actions.Get("gtd_add")
		if !ok {
			t.Fatal("gtd_add action not found")
		}

		result, err := action.Execute(ctx, map[string]any{"title": "Test task"}, map[string]any{})
		if err != nil {
			t.Fatalf("gtd_add failed: %v", err)
		}

		resultStr, ok := result.(string)
		if !ok {
			t.Fatalf("expected string result, got %T", result)
		}
		if !strings.Contains(resultStr, "Test task") {
			t.Errorf("unexpected result: %v", result)
		}

		// Verify task was added
		tasks := gtdStore.GetTasks("inbox", "", "")
		if len(tasks) != 1 {
			t.Errorf("expected 1 task, got %d", len(tasks))
		}
	})

	// Test gtd_list
	t.Run("gtd_list", func(t *testing.T) {
		action, ok := engine.actions.Get("gtd_list")
		if !ok {
			t.Fatal("gtd_list action not found")
		}

		result, err := action.Execute(ctx, map[string]any{"when": "inbox"}, map[string]any{})
		if err != nil {
			t.Fatalf("gtd_list failed: %v", err)
		}

		resultStr, ok := result.(string)
		if !ok {
			t.Fatalf("expected string result, got %T", result)
		}
		if !strings.Contains(resultStr, "Test task") {
			t.Errorf("expected result to contain 'Test task', got: %s", resultStr)
		}
	})

	// Test gtd_complete
	t.Run("gtd_complete", func(t *testing.T) {
		action, ok := engine.actions.Get("gtd_complete")
		if !ok {
			t.Fatal("gtd_complete action not found")
		}

		result, err := action.Execute(ctx, map[string]any{"title": "Test"}, map[string]any{})
		if err != nil {
			t.Fatalf("gtd_complete failed: %v", err)
		}

		resultStr, ok := result.(string)
		if !ok {
			t.Fatalf("expected string result, got %T", result)
		}
		if !strings.Contains(resultStr, "Completed") {
			t.Errorf("unexpected result: %s", resultStr)
		}
	})
}

func TestGTDActionsWithVariables(t *testing.T) {
	// Test that actions can resolve variables from vars map
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	gtdStore := gtd.NewGTDStore(tmpDir)
	engine.SetGTDStore(gtdStore)

	ctx := context.Background()

	// Test gtd_add with variable reference
	t.Run("gtd_add_with_var", func(t *testing.T) {
		action, ok := engine.actions.Get("gtd_add")
		if !ok {
			t.Fatal("gtd_add action not found")
		}

		// Pass title as variable reference
		result, err := action.Execute(ctx,
			map[string]any{"title": "$task_title"},
			map[string]any{"task_title": "Variable task"},
		)
		if err != nil {
			t.Fatalf("gtd_add failed: %v", err)
		}

		resultStr, ok := result.(string)
		if !ok {
			t.Fatalf("expected string result, got %T", result)
		}
		if !strings.Contains(resultStr, "Variable task") {
			t.Errorf("unexpected result: %v", result)
		}
	})
}

func TestGTDDispatch(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	gtdStore := gtd.NewGTDStore(tmpDir)
	engine.SetGTDStore(gtdStore)

	ctx := context.Background()

	// Add some test tasks first
	gtdStore.AddTask(&gtd.Task{Title: "Morning task", When: "today"})
	gtdStore.AddTask(&gtd.Task{Title: "Inbox item", When: "inbox"})

	action, ok := engine.actions.Get("gtd_dispatch")
	if !ok {
		t.Fatal("gtd_dispatch action not found")
	}

	t.Run("show_today", func(t *testing.T) {
		result, err := action.Execute(ctx, map[string]any{}, map[string]any{
			"intent":  "gtd_show_today",
			"content": "show today's tasks",
		})
		if err != nil {
			t.Fatalf("gtd_dispatch failed: %v", err)
		}

		resultStr, ok := result.(string)
		if !ok {
			t.Fatalf("expected string result, got %T", result)
		}
		if !strings.Contains(resultStr, "Morning task") {
			t.Errorf("expected result to contain 'Morning task', got: %s", resultStr)
		}
	})

	t.Run("show_inbox", func(t *testing.T) {
		result, err := action.Execute(ctx, map[string]any{}, map[string]any{
			"intent":  "gtd_show_inbox",
			"content": "show inbox",
		})
		if err != nil {
			t.Fatalf("gtd_dispatch failed: %v", err)
		}

		resultStr, ok := result.(string)
		if !ok {
			t.Fatalf("expected string result, got %T", result)
		}
		if !strings.Contains(resultStr, "Inbox item") {
			t.Errorf("expected result to contain 'Inbox item', got: %s", resultStr)
		}
	})

	t.Run("add_inbox", func(t *testing.T) {
		result, err := action.Execute(ctx, map[string]any{}, map[string]any{
			"intent":  "gtd_add_inbox",
			"content": "add buy groceries to inbox",
		})
		if err != nil {
			t.Fatalf("gtd_dispatch failed: %v", err)
		}

		resultStr, ok := result.(string)
		if !ok {
			t.Fatalf("expected string result, got %T", result)
		}
		if !strings.Contains(resultStr, "buy groceries") {
			t.Errorf("expected result to contain 'buy groceries', got: %s", resultStr)
		}

		// Verify task was added
		task := gtdStore.FindTaskByTitle("buy groceries")
		if task == nil {
			t.Error("expected task to be added")
		}
	})

	t.Run("unknown_intent", func(t *testing.T) {
		_, err := action.Execute(ctx, map[string]any{}, map[string]any{
			"intent":  "unknown_intent",
			"content": "some message",
		})
		if err == nil {
			t.Error("expected error for unknown intent")
		}
	})
}

func TestGTDActionsWithoutStore(t *testing.T) {
	// Test that actions return error when GTD store is not configured
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)
	// Intentionally not setting GTD store

	ctx := context.Background()

	actions := []string{"gtd_add", "gtd_list", "gtd_complete", "gtd_dispatch"}
	for _, actionName := range actions {
		t.Run(actionName+"_no_store", func(t *testing.T) {
			action, ok := engine.actions.Get(actionName)
			if !ok {
				t.Fatalf("%s action not found", actionName)
			}

			_, err := action.Execute(ctx, map[string]any{"title": "test"}, map[string]any{})
			if err == nil {
				t.Error("expected error when GTD store not configured")
			}
			if !strings.Contains(err.Error(), "GTD store not configured") {
				t.Errorf("expected 'GTD store not configured' error, got: %v", err)
			}
		})
	}
}

func TestGateAction(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	ctx := context.Background()

	action, ok := engine.actions.Get("gate")
	if !ok {
		t.Fatal("gate action not found")
	}

	// Test condition that should stop
	t.Run("stop_when_matched", func(t *testing.T) {
		_, err := action.Execute(ctx,
			map[string]any{"condition": "{{.intent}} == not_gtd", "stop": true},
			map[string]any{"intent": "not_gtd"},
		)
		if err != ErrStopPipeline {
			t.Errorf("expected ErrStopPipeline, got: %v", err)
		}
	})

	// Test condition that should pass
	t.Run("pass_when_not_matched", func(t *testing.T) {
		result, err := action.Execute(ctx,
			map[string]any{"condition": "{{.intent}} == not_gtd", "stop": true},
			map[string]any{"intent": "gtd_show_today"},
		)
		if err != nil {
			t.Errorf("expected pass, got error: %v", err)
		}
		if result != "gate passed" {
			t.Errorf("unexpected result: %v", result)
		}
	})

	// Test with different variables
	t.Run("stop_with_different_var", func(t *testing.T) {
		_, err := action.Execute(ctx,
			map[string]any{"condition": "{{.status}} == blocked", "stop": true},
			map[string]any{"status": "blocked"},
		)
		if err != ErrStopPipeline {
			t.Errorf("expected ErrStopPipeline, got: %v", err)
		}
	})

	// Test without stop flag (should pass even when matched)
	t.Run("match_without_stop", func(t *testing.T) {
		result, err := action.Execute(ctx,
			map[string]any{"condition": "{{.intent}} == not_gtd"},
			map[string]any{"intent": "not_gtd"},
		)
		if err != nil {
			t.Errorf("expected pass, got error: %v", err)
		}
		if result != "gate passed" {
			t.Errorf("unexpected result: %v", result)
		}
	})

	// Test with != condition (inequality check via non-match)
	t.Run("inequality_check", func(t *testing.T) {
		result, err := action.Execute(ctx,
			map[string]any{"condition": "{{.value}} == expected", "stop": true},
			map[string]any{"value": "different"},
		)
		if err != nil {
			t.Errorf("expected pass (no match), got error: %v", err)
		}
		if result != "gate passed" {
			t.Errorf("unexpected result: %v", result)
		}
	})
}

func TestGateActionTemplateErrors(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	ctx := context.Background()

	action, ok := engine.actions.Get("gate")
	if !ok {
		t.Fatal("gate action not found")
	}

	// Test invalid template
	t.Run("invalid_template", func(t *testing.T) {
		_, err := action.Execute(ctx,
			map[string]any{"condition": "{{.invalid", "stop": true},
			map[string]any{},
		)
		if err == nil {
			t.Error("expected error for invalid template")
		}
	})
}

func TestOllamaClassifier(t *testing.T) {
	// Skip if Ollama not available
	t.Skip("Requires running Ollama server")

	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	ctx := context.Background()

	// Test classification
	intent, err := engine.ClassifyWithOllama(ctx, "show me today's tasks",
		[]string{"gtd_show_today", "gtd_add_inbox", "not_gtd"}, "", "")
	if err != nil {
		t.Fatalf("classification failed: %v", err)
	}

	if intent != "gtd_show_today" {
		t.Errorf("expected gtd_show_today, got: %s", intent)
	}
}

func TestGTDListEmpty(t *testing.T) {
	// Test gtd_list with no tasks
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	gtdStore := gtd.NewGTDStore(tmpDir)
	engine.SetGTDStore(gtdStore)

	ctx := context.Background()

	action, ok := engine.actions.Get("gtd_list")
	if !ok {
		t.Fatal("gtd_list action not found")
	}

	result, err := action.Execute(ctx, map[string]any{"when": "today"}, map[string]any{})
	if err != nil {
		t.Fatalf("gtd_list failed: %v", err)
	}

	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.Contains(resultStr, "No tasks") {
		t.Errorf("expected 'No tasks' message, got: %s", resultStr)
	}
}

func TestGTDCompleteNotFound(t *testing.T) {
	// Test gtd_complete with non-existent task
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	gtdStore := gtd.NewGTDStore(tmpDir)
	engine.SetGTDStore(gtdStore)

	ctx := context.Background()

	action, ok := engine.actions.Get("gtd_complete")
	if !ok {
		t.Fatal("gtd_complete action not found")
	}

	_, err := action.Execute(ctx, map[string]any{"title": "nonexistent"}, map[string]any{})
	if err == nil {
		t.Error("expected error for non-existent task")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestGTDAddMissingTitle(t *testing.T) {
	// Test gtd_add without title
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	gtdStore := gtd.NewGTDStore(tmpDir)
	engine.SetGTDStore(gtdStore)

	ctx := context.Background()

	action, ok := engine.actions.Get("gtd_add")
	if !ok {
		t.Fatal("gtd_add action not found")
	}

	_, err := action.Execute(ctx, map[string]any{}, map[string]any{})
	if err == nil {
		t.Error("expected error when title is missing")
	}
	if !strings.Contains(err.Error(), "title is required") {
		t.Errorf("expected 'title is required' error, got: %v", err)
	}
}

func TestGTDAddWithNotes(t *testing.T) {
	// Test gtd_add with notes
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	gtdStore := gtd.NewGTDStore(tmpDir)
	engine.SetGTDStore(gtdStore)

	ctx := context.Background()

	action, ok := engine.actions.Get("gtd_add")
	if !ok {
		t.Fatal("gtd_add action not found")
	}

	result, err := action.Execute(ctx, map[string]any{
		"title": "Task with notes",
		"notes": "These are some notes",
	}, map[string]any{})
	if err != nil {
		t.Fatalf("gtd_add failed: %v", err)
	}

	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.Contains(resultStr, "Task with notes") {
		t.Errorf("unexpected result: %v", result)
	}

	// Verify task was added with notes
	task := gtdStore.FindTaskByTitle("Task with notes")
	if task == nil {
		t.Fatal("expected task to be added")
	}
	if task.Notes != "These are some notes" {
		t.Errorf("expected notes to be set, got: %s", task.Notes)
	}
}

func TestGTDListDefaultsToToday(t *testing.T) {
	// Test that gtd_list defaults to "today" when no "when" param provided
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	gtdStore := gtd.NewGTDStore(tmpDir)
	engine.SetGTDStore(gtdStore)

	// Add tasks to different lists
	gtdStore.AddTask(&gtd.Task{Title: "Today task", When: "today"})
	gtdStore.AddTask(&gtd.Task{Title: "Inbox task", When: "inbox"})

	ctx := context.Background()

	action, ok := engine.actions.Get("gtd_list")
	if !ok {
		t.Fatal("gtd_list action not found")
	}

	// Call without "when" param
	result, err := action.Execute(ctx, map[string]any{}, map[string]any{})
	if err != nil {
		t.Fatalf("gtd_list failed: %v", err)
	}

	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}

	// Should show today tasks, not inbox
	if !strings.Contains(resultStr, "Today task") {
		t.Errorf("expected 'Today task', got: %s", resultStr)
	}
	if strings.Contains(resultStr, "Inbox task") {
		t.Errorf("should not contain 'Inbox task', got: %s", resultStr)
	}
}
