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
	matched, extracted := reflex.Match("discord", "message", "summarize https://example.com/page")
	if !matched {
		t.Error("Expected match")
	}
	if extracted["url"] != "https://example.com/page" {
		t.Errorf("Expected url extraction, got: %v", extracted)
	}

	// Should not match
	matched, _ = reflex.Match("discord", "message", "hello world")
	if matched {
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
	matched, _ := reflex.Match("discord", "message", "hello there")
	if !matched {
		t.Error("Expected match for discord")
	}

	// Should not match github
	matched, _ = reflex.Match("github", "comment", "hello there")
	if matched {
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
	filename := filepath.Join(tmpDir, "reflexes", "test-reflex.yaml")
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
