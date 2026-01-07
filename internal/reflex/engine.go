package reflex

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
		if matched, _ := reflex.Match(source, typ, content); matched {
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

// Process attempts to match and execute reflexes for a percept
// Returns true if any reflex fired (and executive should be skipped)
func (e *Engine) Process(ctx context.Context, source, typ, content string, data map[string]any) (bool, []*ReflexResult) {
	matches := e.Match(source, typ, content)
	if len(matches) == 0 {
		return false, nil
	}

	var results []*ReflexResult
	for _, reflex := range matches {
		_, extracted := reflex.Match(source, typ, content)
		result, err := e.Execute(ctx, reflex, extracted, data)
		if err != nil {
			log.Printf("[reflex] Error executing %s: %v", reflex.Name, err)
			continue
		}
		results = append(results, result)

		if result.Success {
			log.Printf("[reflex] Fired: %s (%.2fms)", reflex.Name, result.Duration.Seconds()*1000)
		} else {
			log.Printf("[reflex] Failed: %s: %v", reflex.Name, result.Error)
		}
	}

	// Return true if any reflex fired successfully
	for _, r := range results {
		if r.Success {
			return true, results
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
