package reflex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"
)

// ErrStopPipeline signals the pipeline should stop (not an error, just early exit)
var ErrStopPipeline = fmt.Errorf("pipeline stopped")

// Action is the interface for reflex actions
type Action interface {
	Execute(ctx context.Context, params map[string]any, vars map[string]any) (any, error)
}

// ActionFunc is a function that implements Action
type ActionFunc func(ctx context.Context, params map[string]any, vars map[string]any) (any, error)

func (f ActionFunc) Execute(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
	return f(ctx, params, vars)
}

// ActionRegistry holds all available actions
type ActionRegistry struct {
	actions map[string]Action
}

// NewActionRegistry creates a registry with built-in actions
func NewActionRegistry() *ActionRegistry {
	r := &ActionRegistry{
		actions: make(map[string]Action),
	}

	// Register built-in actions
	r.Register("fetch_url", ActionFunc(actionFetchURL))
	r.Register("read_file", ActionFunc(actionReadFile))
	r.Register("write_file", ActionFunc(actionWriteFile))
	r.Register("ollama_prompt", ActionFunc(actionOllamaPrompt))
	r.Register("extract_json", ActionFunc(actionExtractJSON))
	r.Register("template", ActionFunc(actionTemplate))
	r.Register("log", ActionFunc(actionLog))
	r.Register("shell", ActionFunc(actionShell))
	r.Register("gate", ActionFunc(actionGate))

	return r
}

// Register adds an action to the registry
func (r *ActionRegistry) Register(name string, action Action) {
	r.actions[name] = action
}

// Get retrieves an action by name
func (r *ActionRegistry) Get(name string) (Action, bool) {
	action, ok := r.actions[name]
	return action, ok
}

// List returns all registered action names
func (r *ActionRegistry) List() []string {
	names := make([]string, 0, len(r.actions))
	for name := range r.actions {
		names = append(names, name)
	}
	return names
}

// Built-in actions

func actionFetchURL(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
	url := resolveVar(params, vars, "url", "input")
	if url == "" {
		return nil, fmt.Errorf("url is required")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}

	return string(body), nil
}

func actionReadFile(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
	path := resolveVar(params, vars, "path", "input")
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}

	return string(data), nil
}

func actionWriteFile(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
	path := resolveVar(params, vars, "path", "")
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	content := resolveVar(params, vars, "content", "input")

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("write failed: %w", err)
	}

	return fmt.Sprintf("Wrote %d bytes to %s", len(content), path), nil
}

func actionOllamaPrompt(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
	model := "qwen2.5:14b" // default model
	if m, ok := params["model"].(string); ok {
		model = m
	}

	promptTemplate := ""
	if p, ok := params["prompt"].(string); ok {
		promptTemplate = p
	}

	// Resolve template with variables
	prompt, err := renderTemplate(promptTemplate, vars)
	if err != nil {
		return nil, fmt.Errorf("template failed: %w", err)
	}

	// Call Ollama API
	reqBody := map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	}

	jsonBody, _ := json.Marshal(reqBody)
	resp, err := http.Post("http://localhost:11434/api/generate", "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ollama decode failed: %w", err)
	}

	return result.Response, nil
}

func actionExtractJSON(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
	input := resolveVar(params, vars, "input", "")
	path := ""
	if p, ok := params["path"].(string); ok {
		path = p
	}

	var data any
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		return nil, fmt.Errorf("json parse failed: %w", err)
	}

	if path == "" {
		return data, nil
	}

	// Simple path extraction (e.g., "results.0.title")
	parts := strings.Split(path, ".")
	current := data
	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			current = v[part]
		case []any:
			// Try to parse as index
			var idx int
			if _, err := fmt.Sscanf(part, "%d", &idx); err == nil && idx < len(v) {
				current = v[idx]
			} else {
				return nil, fmt.Errorf("invalid array index: %s", part)
			}
		default:
			return nil, fmt.Errorf("cannot traverse path at: %s", part)
		}
	}

	return current, nil
}

func actionTemplate(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
	tmplStr := ""
	if t, ok := params["template"].(string); ok {
		tmplStr = t
	}
	if tmplStr == "" {
		return nil, fmt.Errorf("template is required")
	}

	return renderTemplate(tmplStr, vars)
}

func actionLog(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
	message := resolveVar(params, vars, "message", "input")
	fmt.Printf("[reflex] %s\n", message)
	return message, nil
}

func actionShell(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
	command := resolveVar(params, vars, "command", "input")
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("shell failed: %w", err)
	}

	return string(output), nil
}

func actionGate(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
	condition := ""
	if c, ok := params["condition"].(string); ok {
		condition = c
	}

	// Render the condition template
	rendered, err := renderTemplate(condition, vars)
	if err != nil {
		return nil, fmt.Errorf("gate condition template failed: %w", err)
	}

	// Evaluate condition (simple string equality check)
	// Format: "{{.intent}} == not_gtd" renders to "not_gtd == not_gtd"
	parts := strings.Split(rendered, "==")
	if len(parts) == 2 {
		left := strings.TrimSpace(parts[0])
		right := strings.TrimSpace(parts[1])
		if left == right {
			// Condition is true, check if we should stop
			if stop, ok := params["stop"].(bool); ok && stop {
				return nil, ErrStopPipeline
			}
		}
	}

	return "gate passed", nil
}

// Helper functions

func resolveVar(params map[string]any, vars map[string]any, paramNames ...string) string {
	for _, name := range paramNames {
		if v, ok := params[name].(string); ok {
			// Check if it's a variable reference
			if strings.HasPrefix(v, "$") {
				varName := v[1:]
				if val, ok := vars[varName]; ok {
					return fmt.Sprintf("%v", val)
				}
			}
			return v
		}
	}
	return ""
}

func renderTemplate(tmplStr string, vars map[string]any) (string, error) {
	tmpl, err := template.New("reflex").Parse(tmplStr)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", err
	}

	return buf.String(), nil
}
