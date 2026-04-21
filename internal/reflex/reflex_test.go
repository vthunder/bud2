package reflex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockSpawner is a test double for SubagentSpawner.
type mockSpawner struct {
	response string
	err      error
	lastTask string // records the task passed to SpawnSync
}

func (m *mockSpawner) SpawnSync(_ context.Context, systemPrompt, task string) (string, error) {
	m.lastTask = task
	return m.response, m.err
}

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
		"template": "Hello {{name}}, you said: {{message}}",
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
			{Action: "log", Params: map[string]any{"message": "Matched: {{word}}"}},
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
			{Action: "template", Params: map[string]any{"template": "step1: {{text}}"}},
			// Second step uses $_ implicitly via template
			{Action: "template", Params: map[string]any{"template": "wrapped({{_}})"}},
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
			{Action: "template", Params: map[string]any{"template": "sub: {{text}}"}},
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
			{Action: "template", Output: "result", Params: map[string]any{"template": "Echo: {{text}}"}},
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

// ─── WS4 / M3 tests ──────────────────────────────────────────────────────────

func TestInvokeStep(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	// A callable sub-workflow
	sub := &Reflex{
		Name:     "greet",
		Callable: true,
		Pipeline: Pipeline{
			{Action: "template", Params: map[string]any{"template": "Hello, {{who}}!"}},
		},
	}
	engine.SaveReflex(sub)

	// Dispatcher using type:invoke
	dispatcher := &Reflex{
		Name: "greet-dispatcher",
		Trigger: Trigger{
			Pattern: "greet (.*)",
			Extract: []string{"who"},
		},
		Pipeline: Pipeline{
			{Type: "invoke", Workflow: "greet", Output: "greeting"},
			{Action: "template", Params: map[string]any{"template": "{{greeting}}"}},
		},
	}
	engine.SaveReflex(dispatcher)
	engine.Load()

	ctx := context.Background()
	fired, results := engine.Process(ctx, "discord", "message", "greet World", map[string]any{})
	if !fired {
		t.Fatal("Expected dispatcher to fire")
	}
	if !results[0].Success {
		t.Fatalf("Expected success: %v", results[0].Error)
	}
	if results[0].Output["_"] != "Hello, World!" {
		t.Errorf("Expected 'Hello, World!', got: %v", results[0].Output["_"])
	}
}

func TestInvokeStepTemplateWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	// Named sub-workflows for different intents
	for _, intent := range []string{"capture", "review"} {
		i := intent
		engine.SaveReflex(&Reflex{
			Name:     "gtd-" + i,
			Callable: true,
			Pipeline: Pipeline{
				{Action: "template", Params: map[string]any{
					"template": fmt.Sprintf("handled by %s: {{content}}", i),
				}},
			},
		})
	}

	// Dispatcher with template expression in workflow: field
	engine.SaveReflex(&Reflex{
		Name: "gtd-dispatcher",
		Trigger: Trigger{
			Pattern: "dispatch (capture|review) (.*)",
			Extract: []string{"intent", "content"},
		},
		Pipeline: Pipeline{
			{Type: "invoke", Workflow: "gtd-{{intent}}", Output: "result"},
		},
	})
	engine.Load()

	ctx := context.Background()
	for _, tc := range []struct{ msg, want string }{
		{"dispatch capture buy milk", "handled by capture: buy milk"},
		{"dispatch review pr#42", "handled by review: pr#42"},
	} {
		fired, results := engine.Process(ctx, "discord", "message", tc.msg, map[string]any{})
		if !fired || !results[0].Success {
			t.Fatalf("msg=%q: expected success, got %v", tc.msg, results[0].Error)
		}
		if results[0].Output["result"] != tc.want {
			t.Errorf("msg=%q: expected %q, got %q", tc.msg, tc.want, results[0].Output["result"])
		}
	}
}

func TestInvokeStepOnMissingEscalate(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	engine.SaveReflex(&Reflex{
		Name: "invoke-missing",
		Trigger: Trigger{Pattern: "invoke missing"},
		Pipeline: Pipeline{
			{Type: "invoke", Workflow: "does-not-exist", Params: map[string]any{"on_missing": "escalate"}},
		},
	})
	engine.Load()

	ctx := context.Background()
	fired, results := engine.Process(ctx, "discord", "message", "invoke missing", map[string]any{})
	if fired {
		t.Error("Expected not fired (should escalate)")
	}
	if len(results) == 0 || !results[0].Escalate {
		t.Errorf("Expected Escalate=true, got results=%v", results)
	}
}

func TestSubagentStep(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	spawner := &mockSpawner{response: "agent output: task done"}
	engine.SetSubagentSpawner(spawner)

	engine.SaveReflex(&Reflex{
		Name: "subagent-test",
		Trigger: Trigger{
			Pattern: "run agent (.*)",
			Extract: []string{"task"},
		},
		Pipeline: Pipeline{
			{Type: "subagent", Agent: "researcher", Output: "research"},
			{Action: "template", Params: map[string]any{"template": "result: {{research}}"}},
		},
	})
	engine.Load()

	ctx := context.Background()
	fired, results := engine.Process(ctx, "discord", "message", "run agent summarize docs", map[string]any{})
	if !fired || !results[0].Success {
		t.Fatalf("Expected success, got: %v", results[0].Error)
	}
	if results[0].Output["_"] != "result: agent output: task done" {
		t.Errorf("Unexpected output: %v", results[0].Output["_"])
	}
	if !strings.Contains(spawner.lastTask, "researcher") {
		t.Errorf("Expected task to reference agent name, got: %s", spawner.lastTask)
	}
}

func TestSubagentStepNoSpawner(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)
	// Deliberately do NOT call SetSubagentSpawner

	engine.SaveReflex(&Reflex{
		Name: "no-spawner",
		Trigger: Trigger{Pattern: "no spawner"},
		Pipeline: Pipeline{
			{Type: "subagent", Agent: "researcher"},
		},
	})
	engine.Load()

	ctx := context.Background()
	fired, results := engine.Process(ctx, "discord", "message", "no spawner", map[string]any{})
	if fired {
		t.Error("Expected not fired (error)")
	}
	if len(results) == 0 || results[0].Success {
		t.Error("Expected failure result")
	}
}

func TestOnErrorSkip(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	engine.SaveReflex(&Reflex{
		Name: "skip-test",
		Trigger: Trigger{Pattern: "skip error test"},
		Pipeline: Pipeline{
			// Step 1: fails (unknown action), but on_error:skip continues
			{Action: "totally_nonexistent_action_xyz", Output: "step1", OnError: "skip"},
			// Step 2: uses _error set by skip
			{Action: "template", Params: map[string]any{"template": "caught: {{_error}}"}},
		},
	})
	engine.Load()

	ctx := context.Background()
	fired, results := engine.Process(ctx, "discord", "message", "skip error test", map[string]any{})
	if !fired {
		t.Fatal("Expected reflex to fire despite error")
	}
	if !results[0].Success {
		t.Fatalf("Expected success with skip, got: %v", results[0].Error)
	}
	out, _ := results[0].Output["_"].(string)
	if !strings.HasPrefix(out, "caught: ") {
		t.Errorf("Expected _error to be captured, got: %q", out)
	}
	if results[0].Output["step1"] != nil {
		t.Errorf("Expected step1 output to be nil after skip, got: %v", results[0].Output["step1"])
	}
}

func TestOnErrorRetry(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	attempts := 0
	engine.actions.Register("fail_twice", ActionFunc(func(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
		attempts++
		if attempts < 3 {
			return nil, fmt.Errorf("attempt %d failed", attempts)
		}
		return "succeeded on attempt 3", nil
	}))

	engine.SaveReflex(&Reflex{
		Name: "retry-test",
		Trigger: Trigger{Pattern: "retry test"},
		Pipeline: Pipeline{
			{Action: "fail_twice", Output: "result", OnError: "retry", MaxRetries: 3, RetryDelaySecs: 0},
		},
	})
	engine.Load()

	ctx := context.Background()
	fired, results := engine.Process(ctx, "discord", "message", "retry test", map[string]any{})
	if !fired || !results[0].Success {
		t.Fatalf("Expected success after retry, got: %v", results[0].Error)
	}
	if results[0].Output["result"] != "succeeded on attempt 3" {
		t.Errorf("Unexpected output: %v", results[0].Output["result"])
	}
	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestReturnsField(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	engine.SaveReflex(&Reflex{
		Name:    "returns-test",
		Returns: "answer",
		Trigger: Trigger{Pattern: "returns test"},
		Pipeline: Pipeline{
			{Action: "template", Output: "answer", Params: map[string]any{"template": "the answer"}},
			{Action: "template", Output: "noise", Params: map[string]any{"template": "ignored"}},
		},
	})
	engine.Load()

	ctx := context.Background()
	fired, results := engine.Process(ctx, "discord", "message", "returns test", map[string]any{})
	if !fired || !results[0].Success {
		t.Fatalf("Expected success: %v", results[0].Error)
	}
	if results[0].Output["_"] != "the answer" {
		t.Errorf("returns: field should set $_ to 'the answer', got: %v", results[0].Output["_"])
	}
}

func TestWorkflowParams(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	engine.SaveReflex(&Reflex{
		Name: "params-test",
		Trigger: Trigger{Pattern: "params test"},
		Params: map[string]WorkflowParam{
			"greeting": {Type: "string", Default: "Hi"},
			"required_field": {Type: "string", Required: true},
		},
		Pipeline: Pipeline{
			{Action: "template", Params: map[string]any{"template": "{{greeting}} from params-test"}},
		},
	})
	engine.Load()

	// Missing required param → fail
	ctx := context.Background()
	_, results := engine.Process(ctx, "discord", "message", "params test", map[string]any{})
	if results[0].Success {
		t.Error("Expected failure when required param missing")
	}

	// Provide required param, default should apply for greeting
	reflex := engine.Get("params-test")
	result, _ := engine.Execute(ctx, reflex, nil, map[string]any{"required_field": "present"})
	if !result.Success {
		t.Fatalf("Expected success with required param: %v", result.Error)
	}
	if result.Output["_"] != "Hi from params-test" {
		t.Errorf("Expected default greeting, got: %v", result.Output["_"])
	}

	// Override default
	result2, _ := engine.Execute(ctx, reflex, nil, map[string]any{"required_field": "present", "greeting": "Hello"})
	if !result2.Success {
		t.Fatalf("Expected success: %v", result2.Error)
	}
	if result2.Output["_"] != "Hello from params-test" {
		t.Errorf("Expected overridden greeting, got: %v", result2.Output["_"])
	}
}

func TestRenderNewTemplate(t *testing.T) {
	vars := map[string]any{
		"intent":  "capture",
		"content": "buy milk",
		"_":       "pipe-value",
		"_error":  "some error",
		"params": map[string]any{
			"key": "param-value",
		},
	}

	tests := []struct {
		tmpl    string
		want    string
		wantErr bool
	}{
		{"gtd-{{intent}}", "gtd-capture", false},
		{"{{content}}", "buy milk", false},
		{"{{_}}", "pipe-value", false},
		{"{{_error}}", "some error", false},
		{"{{params.key}}", "param-value", false},
		{"no template here", "no template here", false},
		{"{{undefined_var}}", "", true}, // fail-fast on undefined
	}

	for _, tc := range tests {
		got, err := renderNewTemplate(tc.tmpl, vars)
		if tc.wantErr {
			if err == nil {
				t.Errorf("template %q: expected error, got %q", tc.tmpl, got)
			}
		} else {
			if err != nil {
				t.Errorf("template %q: unexpected error: %v", tc.tmpl, err)
			}
			if got != tc.want {
				t.Errorf("template %q: want %q, got %q", tc.tmpl, tc.want, got)
			}
		}
	}
}

func TestDuplicateOutputNameRejected(t *testing.T) {
	r := &Reflex{
		Name: "dup-test",
		Pipeline: Pipeline{
			{Action: "template", Output: "result"},
			{Action: "template", Output: "result"}, // duplicate
		},
	}
	if err := validateOutputNames(r); err == nil {
		t.Error("Expected error for duplicate output name 'result'")
	}
}

func TestExtensionSemaphore(t *testing.T) {
	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	engine.SaveReflex(&Reflex{
		Name:      "sem-test",
		Extension: "test-ext",
		Trigger:   Trigger{Pattern: "sem test"},
		Pipeline: Pipeline{
			{Action: "template", Params: map[string]any{"template": "ok"}},
		},
	})
	engine.Load()

	ctx := context.Background()
	fired, results := engine.Process(ctx, "discord", "message", "sem test", map[string]any{})
	if !fired || !results[0].Success {
		t.Fatalf("Expected success: %v", results[0].Error)
	}
}
