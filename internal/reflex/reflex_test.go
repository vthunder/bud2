package reflex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReflexMatch(t *testing.T) {
	reflex := &Reflex{
		Name: "test-url",
		Trigger: Trigger{
			Pattern: `summarize (https?://\S+)`,
			Extract: []string{"url"},
		},
	}

	// Should match
	result := reflex.Match("discord", "message", "summarize https://example.com/page")
	if !result.Matched {
		t.Error("Expected match")
	}
	if result.Extracted["url"] != "https://example.com/page" {
		t.Errorf("Expected url extraction, got: %v", result.Extracted)
	}

	// Should not match
	result = reflex.Match("discord", "message", "hello world")
	if result.Matched {
		t.Error("Expected no match")
	}
}

func TestReflexMatchWithFilters(t *testing.T) {
	reflex := &Reflex{
		Name: "discord-only",
		Trigger: Trigger{
			Source:  "discord",
			Pattern: `hello`,
		},
	}

	// Should match discord
	result := reflex.Match("discord", "message", "hello there")
	if !result.Matched {
		t.Error("Expected match for discord")
	}

	// Should not match github
	result = reflex.Match("github", "comment", "hello there")
	if result.Matched {
		t.Error("Expected no match for github")
	}
}

func TestActionRegistry(t *testing.T) {
	registry := NewActionRegistry()

	// Check built-in actions exist
	expectedActions := []string{"fetch_url", "read_file", "write_file", "ollama_prompt", "extract_json", "template", "log", "shell"}
	for _, name := range expectedActions {
		if _, ok := registry.Get(name); !ok {
			t.Errorf("Expected action %s to be registered", name)
		}
	}
}

func TestActionTemplate(t *testing.T) {
	ctx := context.Background()
	params := map[string]any{
		"template": "Hello {{.name}}, you said: {{.message}}",
	}
	vars := map[string]any{
		"name":    "User",
		"message": "test message",
	}

	result, err := actionTemplate(ctx, params, vars)
	if err != nil {
		t.Fatalf("Template action failed: %v", err)
	}

	expected := "Hello User, you said: test message"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestEngineLoadSave(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	// Create a test reflex
	reflex := &Reflex{
		Name:        "test-reflex",
		Description: "A test reflex",
		Trigger: Trigger{
			Pattern: "test (\\w+)",
			Extract: []string{"word"},
		},
		Pipeline: Pipeline{
			{Action: "log", Params: map[string]any{"message": "Matched: {{.word}}"}},
		},
	}

	// Save it
	if err := engine.SaveReflex(reflex); err != nil {
		t.Fatalf("SaveReflex failed: %v", err)
	}

	// Check file exists
	filename := filepath.Join(tmpDir, "system", "reflexes", "test-reflex.yaml")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Expected reflex file to exist")
	}

	// Load reflexes
	engine2 := NewEngine(tmpDir)
	if err := engine2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Check reflex was loaded
	loaded := engine2.Get("test-reflex")
	if loaded == nil {
		t.Fatal("Expected reflex to be loaded")
	}
	if loaded.Description != "A test reflex" {
		t.Errorf("Expected description to match, got: %s", loaded.Description)
	}
}

func TestImplicitPiping(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	// Pipeline where steps chain through $_ without explicit output names
	reflex := &Reflex{
		Name: "pipe-test",
		Trigger: Trigger{
			Pattern: "pipe (.*)",
			Extract: []string{"text"},
		},
		Pipeline: Pipeline{
			{Action: "template", Params: map[string]any{"template": "step1: {{.text}}"}},
			// Second step uses $_ implicitly via template
			{Action: "template", Params: map[string]any{"template": "wrapped({{._}})"}},
		},
	}
	engine.SaveReflex(reflex)

	ctx := context.Background()
	fired, results := engine.Process(ctx, "discord", "message", "pipe hello", map[string]any{})

	if !fired {
		t.Fatal("Expected reflex to fire")
	}
	if !results[0].Success {
		t.Fatalf("Expected success: %v", results[0].Error)
	}
	// $_ should hold the last step's output
	if results[0].Output["_"] != "wrapped(step1: hello)" {
		t.Errorf("Unexpected implicit pipe output: %v", results[0].Output["_"])
	}
}

func TestInvokeReflex(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	// Register a callable sub-reflex
	sub := &Reflex{
		Name:     "my-sub",
		Callable: true,
		Pipeline: Pipeline{
			{Action: "template", Params: map[string]any{"template": "sub: {{.text}}"}},
		},
	}
	engine.SaveReflex(sub)

	// Register a dispatcher that invokes the sub-reflex
	dispatcher := &Reflex{
		Name: "my-dispatcher",
		Trigger: Trigger{
			Pattern: "dispatch (.*)",
			Extract: []string{"text"},
		},
		Pipeline: Pipeline{
			{Action: "invoke_reflex", Params: map[string]any{"name": "my-sub"}},
		},
	}
	engine.SaveReflex(dispatcher)

	// Reload so both are known
	engine.Load()

	ctx := context.Background()
	fired, results := engine.Process(ctx, "discord", "message", "dispatch world", map[string]any{})

	if !fired {
		t.Fatal("Expected dispatcher to fire")
	}
	if !results[0].Success {
		t.Fatalf("Expected success: %v", results[0].Error)
	}
	if results[0].Output["_"] != "sub: world" {
		t.Errorf("Expected 'sub: world', got: %v", results[0].Output["_"])
	}
}

func TestInvokeReflexOnMissingEscalate(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	// Dispatcher that tries to invoke a non-existent reflex and escalates
	dispatcher := &Reflex{
		Name: "escalate-test",
		Trigger: Trigger{
			Pattern: "escalate",
		},
		Pipeline: Pipeline{
			{Action: "invoke_reflex", Params: map[string]any{
				"name":       "nonexistent-reflex",
				"on_missing": "escalate",
			}},
		},
	}
	engine.SaveReflex(dispatcher)
	engine.Load()

	ctx := context.Background()
	fired, results := engine.Process(ctx, "discord", "message", "escalate", map[string]any{})

	// Should NOT have fired (escalated to executive instead)
	if fired {
		t.Error("Expected not fired (escalated)")
	}
	if len(results) == 0 || !results[0].Escalate {
		t.Error("Expected result with Escalate=true")
	}
}

func TestCallableReflexNotAutoMatched(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	// A callable reflex with a permissive pattern - should NOT auto-match
	callable := &Reflex{
		Name:     "callable-only",
		Callable: true,
		Trigger: Trigger{
			Pattern: ".*", // would match everything if not callable
		},
		Pipeline: Pipeline{
			{Action: "log", Params: map[string]any{"message": "should not run"}},
		},
	}
	engine.SaveReflex(callable)
	engine.Load()

	ctx := context.Background()
	fired, _ := engine.Process(ctx, "discord", "message", "hello world", map[string]any{})

	if fired {
		t.Error("Callable reflex should not auto-match percepts")
	}
}

func TestEngineProcess(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	// Create and save a test reflex
	reflex := &Reflex{
		Name: "echo-test",
		Trigger: Trigger{
			Pattern: "echo (.*)",
			Extract: []string{"text"},
		},
		Pipeline: Pipeline{
			{Action: "template", Output: "result", Params: map[string]any{"template": "Echo: {{.text}}"}},
		},
	}
	engine.SaveReflex(reflex)

	// Process a matching message
	ctx := context.Background()
	fired, results := engine.Process(ctx, "discord", "message", "echo hello world", map[string]any{})

	if !fired {
		t.Error("Expected reflex to fire")
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if !results[0].Success {
		t.Errorf("Expected success, got error: %v", results[0].Error)
	}
	if results[0].Output["result"] != "Echo: hello world" {
		t.Errorf("Unexpected output: %v", results[0].Output)
	}

	t.Logf("Reflex test passed: %v", results[0].Output)
}
