package reflex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Engine manages and executes reflexes
type Engine struct {
	reflexes  map[string]*Reflex
	actions   *ActionRegistry
	reflexDir string
	mu        sync.RWMutex

	// Callbacks for integration
	onReply func(channelID, message string) error
	onReact func(channelID, messageID, emoji string) error
}

// NewEngine creates a new reflex engine
func NewEngine(statePath string) *Engine {
	return &Engine{
		reflexes:  make(map[string]*Reflex),
		actions:   NewActionRegistry(),
		reflexDir: filepath.Join(statePath, "reflexes"),
	}
}

// SetReplyCallback sets the callback for reply actions
func (e *Engine) SetReplyCallback(cb func(channelID, message string) error) {
	e.onReply = cb
}

// SetReactCallback sets the callback for react actions
func (e *Engine) SetReactCallback(cb func(channelID, messageID, emoji string) error) {
	e.onReact = cb
}

// Load loads all reflexes from the reflexes directory
func (e *Engine) Load() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Ensure directory exists
	if err := os.MkdirAll(e.reflexDir, 0755); err != nil {
		return fmt.Errorf("failed to create reflexes dir: %w", err)
	}

	// Find all YAML files
	files, err := filepath.Glob(filepath.Join(e.reflexDir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("failed to glob reflexes: %w", err)
	}

	yamlFiles, err := filepath.Glob(filepath.Join(e.reflexDir, "*.yml"))
	if err != nil {
		return fmt.Errorf("failed to glob reflexes: %w", err)
	}
	files = append(files, yamlFiles...)

	// Load each file
	e.reflexes = make(map[string]*Reflex)
	for _, file := range files {
		reflex, err := e.loadReflexFile(file)
		if err != nil {
			log.Printf("[reflex] Failed to load %s: %v", file, err)
			continue
		}
		e.reflexes[reflex.Name] = reflex
		log.Printf("[reflex] Loaded: %s", reflex.Name)
	}

	log.Printf("[reflex] Loaded %d reflexes", len(e.reflexes))
	return nil
}

func (e *Engine) loadReflexFile(path string) (*Reflex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var reflex Reflex
	if err := yaml.Unmarshal(data, &reflex); err != nil {
		return nil, err
	}

	if reflex.Name == "" {
		reflex.Name = filepath.Base(path)
	}

	return &reflex, nil
}

// SaveReflex saves a reflex to a YAML file
func (e *Engine) SaveReflex(reflex *Reflex) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Ensure directory exists
	if err := os.MkdirAll(e.reflexDir, 0755); err != nil {
		return fmt.Errorf("failed to create reflexes dir: %w", err)
	}

	// Marshal to YAML
	data, err := yaml.Marshal(reflex)
	if err != nil {
		return fmt.Errorf("failed to marshal reflex: %w", err)
	}

	// Save to file
	filename := filepath.Join(e.reflexDir, reflex.Name+".yaml")
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write reflex: %w", err)
	}

	// Add to registry
	e.reflexes[reflex.Name] = reflex
	log.Printf("[reflex] Saved: %s", reflex.Name)

	return nil
}

// Match finds all reflexes that match a percept
func (e *Engine) Match(source, typ, content string) []*Reflex {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var matches []*Reflex
	for _, reflex := range e.reflexes {
		result := reflex.Match(source, typ, content)
		if result.Matched {
			matches = append(matches, reflex)
		}
	}
	return matches
}

// Execute runs a reflex pipeline with extracted variables
func (e *Engine) Execute(ctx context.Context, reflex *Reflex, extracted map[string]string, perceptData map[string]any) (*ReflexResult, error) {
	start := time.Now()

	// Initialize variables with extracted values and percept data
	vars := make(map[string]any)
	for k, v := range extracted {
		vars[k] = v
	}
	for k, v := range perceptData {
		vars[k] = v
	}

	// Execute pipeline steps
	for i, step := range reflex.Pipeline {
		action, ok := e.actions.Get(step.Action)
		if !ok {
			// Check for special actions (reply, react)
			switch step.Action {
			case "reply":
				if err := e.executeReply(step, vars); err != nil {
					return &ReflexResult{
						ReflexName: reflex.Name,
						Success:    false,
						Error:      fmt.Errorf("step %d (reply) failed: %w", i, err),
						Duration:   time.Since(start),
					}, nil
				}
				continue
			case "react":
				if err := e.executeReact(step, vars); err != nil {
					return &ReflexResult{
						ReflexName: reflex.Name,
						Success:    false,
						Error:      fmt.Errorf("step %d (react) failed: %w", i, err),
						Duration:   time.Since(start),
					}, nil
				}
				continue
			case "add_task", "add_idea":
				// These would integrate with motivation package
				log.Printf("[reflex] Action %s not yet integrated", step.Action)
				continue
			default:
				return &ReflexResult{
					ReflexName: reflex.Name,
					Success:    false,
					Error:      fmt.Errorf("unknown action: %s", step.Action),
					Duration:   time.Since(start),
				}, nil
			}
		}

		// Build params from step
		params := make(map[string]any)
		for k, v := range step.Params {
			params[k] = v
		}
		if step.Input != "" {
			params["input"] = step.Input
		}

		// Execute action
		result, err := action.Execute(ctx, params, vars)
		if err != nil {
			return &ReflexResult{
				ReflexName: reflex.Name,
				Success:    false,
				Error:      fmt.Errorf("step %d (%s) failed: %w", i, step.Action, err),
				Duration:   time.Since(start),
			}, nil
		}

		// Store output in variables
		if step.Output != "" {
			vars[step.Output] = result
		}
	}

	// Update stats
	reflex.LastFired = time.Now()
	reflex.FireCount++

	return &ReflexResult{
		ReflexName: reflex.Name,
		Success:    true,
		Output:     vars,
		Duration:   time.Since(start),
	}, nil
}

func (e *Engine) executeReply(step PipelineStep, vars map[string]any) error {
	if e.onReply == nil {
		return fmt.Errorf("reply callback not configured")
	}

	message := ""
	if m, ok := step.Params["message"].(string); ok {
		rendered, err := renderTemplate(m, vars)
		if err != nil {
			return err
		}
		message = rendered
	}

	channelID := ""
	if ch, ok := vars["channel_id"].(string); ok {
		channelID = ch
	}

	if channelID == "" || message == "" {
		return fmt.Errorf("channel_id and message required for reply")
	}

	return e.onReply(channelID, message)
}

func (e *Engine) executeReact(step PipelineStep, vars map[string]any) error {
	if e.onReact == nil {
		return fmt.Errorf("react callback not configured")
	}

	emoji := ""
	if em, ok := step.Params["emoji"].(string); ok {
		emoji = em
	}

	channelID := ""
	if ch, ok := vars["channel_id"].(string); ok {
		channelID = ch
	}

	messageID := ""
	if m, ok := vars["message_id"].(string); ok {
		messageID = m
	}

	if channelID == "" || messageID == "" || emoji == "" {
		return fmt.Errorf("channel_id, message_id, and emoji required for react")
	}

	return e.onReact(channelID, messageID, emoji)
}

// ClassifyWithOllama uses Ollama to classify content against a list of intents
func (e *Engine) ClassifyWithOllama(ctx context.Context, content string, intents []string, model string, customPrompt string) (string, error) {
	if model == "" {
		model = "qwen2.5:7b"
	}

	// Build classification prompt
	var prompt string
	if customPrompt != "" {
		prompt = fmt.Sprintf("%s\n\nMessage: %s", customPrompt, content)
	} else {
		intentList := strings.Join(intents, ", ")
		prompt = fmt.Sprintf(`Classify the following message into one of these intents: %s

If the message doesn't match any intent, respond with "not_matched".
Respond with ONLY the intent name, nothing else.

Message: %s`, intentList, content)
	}

	// Call Ollama API
	reqBody := map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	}

	jsonBody, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", "http://localhost:11434/api/generate", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("ollama decode failed: %w", err)
	}

	// Clean up the response
	intent := strings.TrimSpace(result.Response)
	intent = strings.ToLower(intent)

	// Validate intent is in the list (or not_matched)
	if intent == "not_matched" {
		return "not_matched", nil
	}

	for _, valid := range intents {
		if strings.ToLower(valid) == intent {
			return valid, nil
		}
	}

	// If response doesn't match any valid intent, treat as not matched
	log.Printf("[reflex] Ollama returned unknown intent: %q, treating as not_matched", intent)
	return "not_matched", nil
}

// Process attempts to match and execute reflexes for a percept
// Returns true if any reflex fired (and executive should be skipped)
func (e *Engine) Process(ctx context.Context, source, typ, content string, data map[string]any) (bool, []*ReflexResult) {
	matches := e.Match(source, typ, content)
	if len(matches) == 0 {
		return false, nil
	}

	// Sort candidates by priority (higher first)
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Priority > matches[j].Priority
	})

	var results []*ReflexResult

	// Try each candidate until one succeeds
	for _, reflex := range matches {
		matchResult := reflex.Match(source, typ, content)
		extracted := matchResult.Extracted

		// Handle Ollama classification
		if reflex.Trigger.Classifier == "ollama" {
			if len(reflex.Trigger.Intents) == 0 {
				log.Printf("[reflex] Skipping %s: ollama classifier requires intents", reflex.Name)
				continue
			}

			intent, err := e.ClassifyWithOllama(ctx, content, reflex.Trigger.Intents, reflex.Trigger.Model, reflex.Trigger.Prompt)
			if err != nil {
				log.Printf("[reflex] Ollama classification failed for %s: %v", reflex.Name, err)
				continue
			}

			if intent == "not_matched" {
				log.Printf("[reflex] Ollama did not match %s", reflex.Name)
				continue
			}

			// Store classified intent in extracted vars
			extracted["intent"] = intent
			log.Printf("[reflex] Ollama classified as %q for %s", intent, reflex.Name)
		}

		result, err := e.Execute(ctx, reflex, extracted, data)
		if err != nil {
			log.Printf("[reflex] Error executing %s: %v", reflex.Name, err)
			continue
		}
		results = append(results, result)

		if result.Success {
			log.Printf("[reflex] Fired: %s (%.2fms)", reflex.Name, result.Duration.Seconds()*1000)
			// Return immediately on first success
			return true, results
		} else {
			log.Printf("[reflex] Failed: %s: %v", reflex.Name, result.Error)
		}
	}

	return false, results
}

// List returns all loaded reflexes
func (e *Engine) List() []*Reflex {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*Reflex, 0, len(e.reflexes))
	for _, r := range e.reflexes {
		result = append(result, r)
	}
	return result
}

// Get returns a reflex by name
func (e *Engine) Get(name string) *Reflex {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.reflexes[name]
}

// Delete removes a reflex
func (e *Engine) Delete(name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.reflexes[name]; !ok {
		return fmt.Errorf("reflex not found: %s", name)
	}

	// Remove from registry
	delete(e.reflexes, name)

	// Remove file
	filename := filepath.Join(e.reflexDir, name+".yaml")
	if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	log.Printf("[reflex] Deleted: %s", name)
	return nil
}
