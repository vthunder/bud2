package reflex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/gtd"
	"github.com/vthunder/bud2/internal/integrations/calendar"
	"gopkg.in/yaml.v3"
)

// AttentionChecker is the interface for checking attention state
type AttentionChecker interface {
	IsAttending(domain string) bool
}

// Engine manages and executes reflexes
type Engine struct {
	reflexes    map[string]*Reflex
	actions     *ActionRegistry
	reflexDir   string
	mu          sync.RWMutex
	fileModTime map[string]time.Time // Track file mod times for hot reload

	// Default channel for notifications (used when no channel_id in context)
	defaultChannel string

	// Attention system for proactive mode
	attention AttentionChecker

	// Callbacks for integration
	onReply            func(channelID, message string) error
	onInteractionReply func(token, appID, message string) error // for slash command followup responses
	onReact            func(channelID, messageID, emoji string) error

	// GTD store for gtd_* actions
	gtdStore interface {
		GetTasks(when, projectID, areaID string) []gtd.Task
		AddTask(task *gtd.Task)
		CompleteTask(id string) error
		Save() error
		FindTaskByTitle(title string) *gtd.Task
	}

	// Bud task store for complete_bud_task action
	budTaskStore interface {
		Complete(id string)
		Save() error
	}

	// Calendar client for calendar_* actions
	calendarClient *calendar.Client
}

// NewEngine creates a new reflex engine
func NewEngine(statePath string) *Engine {
	e := &Engine{
		reflexes:    make(map[string]*Reflex),
		actions:     NewActionRegistry(),
		reflexDir:   filepath.Join(statePath, "system", "reflexes"),
		fileModTime: make(map[string]time.Time),
	}
	e.createGTDActions()
	return e
}

// SetReplyCallback sets the callback for reply actions
func (e *Engine) SetReplyCallback(cb func(channelID, message string) error) {
	e.onReply = cb
}

// SetInteractionReplyCallback sets the callback for slash command interaction responses
// The callback receives token and appID strings (not a full Interaction object)
func (e *Engine) SetInteractionReplyCallback(cb func(token, appID, message string) error) {
	e.onInteractionReply = cb
}

// SetReactCallback sets the callback for react actions
func (e *Engine) SetReactCallback(cb func(channelID, messageID, emoji string) error) {
	e.onReact = cb
}

// SetDefaultChannel sets the default channel for notifications when no channel_id in context
func (e *Engine) SetDefaultChannel(channelID string) {
	e.defaultChannel = channelID
}

// SetGTDStore sets the GTD store for reflex actions
func (e *Engine) SetGTDStore(store interface {
	GetTasks(when, projectID, areaID string) []gtd.Task
	AddTask(task *gtd.Task)
	CompleteTask(id string) error
	Save() error
	FindTaskByTitle(title string) *gtd.Task
}) {
	e.gtdStore = store
}

// SetBudTaskStore sets the bud task store for complete_bud_task action
func (e *Engine) SetBudTaskStore(store interface {
	Complete(id string)
	Save() error
}) {
	e.budTaskStore = store
}

// SetCalendarClient sets the calendar client for calendar_* actions
func (e *Engine) SetCalendarClient(client *calendar.Client) {
	e.calendarClient = client
	if client != nil {
		e.createCalendarActions()
	}
}

// SetAttention sets the attention checker for proactive mode
// When attention is actively focusing on a domain, reflexes for that domain are bypassed
func (e *Engine) SetAttention(attention AttentionChecker) {
	e.attention = attention
}

// Load loads all reflexes from the reflexes directory
func (e *Engine) Load() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Check if directory exists
	dirExists := true
	if _, err := os.Stat(e.reflexDir); os.IsNotExist(err) {
		dirExists = false
	}

	// Ensure directory exists
	if err := os.MkdirAll(e.reflexDir, 0755); err != nil {
		return fmt.Errorf("failed to create reflexes dir: %w", err)
	}

	// If directory was just created, seed from seed/reflexes/
	if !dirExists {
		e.seedFromDefaults()
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
	e.fileModTime = make(map[string]time.Time)
	for _, file := range files {
		reflex, err := e.loadReflexFile(file)
		if err != nil {
			log.Printf("[reflex] Failed to load %s: %v", file, err)
			continue
		}
		e.reflexes[reflex.Name] = reflex
		// Track mod time for hot reload
		if info, err := os.Stat(file); err == nil {
			e.fileModTime[file] = info.ModTime()
		}
		log.Printf("[reflex] Loaded: %s", reflex.Name)
	}

	log.Printf("[reflex] Loaded %d reflexes", len(e.reflexes))

	// Merge stats from separate stats file (so stats don't overwrite config)
	e.loadStats()

	return nil
}

// CheckForUpdates checks if any reflex files have been modified and reloads them
// Returns the number of reflexes that were reloaded
func (e *Engine) CheckForUpdates() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	reloaded := 0

	// Find all YAML files
	files, _ := filepath.Glob(filepath.Join(e.reflexDir, "*.yaml"))
	yamlFiles, _ := filepath.Glob(filepath.Join(e.reflexDir, "*.yml"))
	files = append(files, yamlFiles...)

	// Check for new or modified files
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}

		lastMod, known := e.fileModTime[file]
		if !known || info.ModTime().After(lastMod) {
			// File is new or modified - reload it
			reflex, err := e.loadReflexFile(file)
			if err != nil {
				log.Printf("[reflex] Failed to reload %s: %v", file, err)
				continue
			}

			// Preserve stats from old reflex if it existed
			if oldReflex, exists := e.reflexes[reflex.Name]; exists {
				reflex.FireCount = oldReflex.FireCount
				reflex.LastFired = oldReflex.LastFired
			}

			e.reflexes[reflex.Name] = reflex
			e.fileModTime[file] = info.ModTime()
			log.Printf("[reflex] Hot-reloaded: %s", reflex.Name)
			reloaded++
		}
	}

	// Check for deleted files
	currentFiles := make(map[string]bool)
	for _, file := range files {
		currentFiles[file] = true
	}
	for file := range e.fileModTime {
		if !currentFiles[file] {
			// File was deleted - find and remove the reflex
			for name := range e.reflexes {
				reflexFile := filepath.Join(e.reflexDir, name+".yaml")
				if reflexFile == file || filepath.Join(e.reflexDir, name+".yml") == file {
					delete(e.reflexes, name)
					log.Printf("[reflex] Removed (file deleted): %s", name)
					break
				}
			}
			delete(e.fileModTime, file)
		}
	}

	return reloaded
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

// seedFromDefaults copies seed reflexes to the reflexes directory
func (e *Engine) seedFromDefaults() {
	seedDir := "seed/reflexes"
	files, err := filepath.Glob(filepath.Join(seedDir, "*.yaml"))
	if err != nil {
		log.Printf("[reflex] Failed to glob seed reflexes: %v", err)
		return
	}

	ymlFiles, err := filepath.Glob(filepath.Join(seedDir, "*.yml"))
	if err == nil {
		files = append(files, ymlFiles...)
	}

	for _, src := range files {
		data, err := os.ReadFile(src)
		if err != nil {
			log.Printf("[reflex] Failed to read seed %s: %v", src, err)
			continue
		}

		dst := filepath.Join(e.reflexDir, filepath.Base(src))
		if err := os.WriteFile(dst, data, 0644); err != nil {
			log.Printf("[reflex] Failed to write %s: %v", dst, err)
			continue
		}

		log.Printf("[reflex] Seeded: %s", filepath.Base(src))
	}
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
			// Check if this is a pipeline stop (not an error)
			if errors.Is(err, ErrStopPipeline) {
				reflex.LastFired = time.Now()
				reflex.FireCount++
				// Persist stats (unlock before save to avoid deadlock)
				e.saveReflexStats(reflex)
				return &ReflexResult{
					ReflexName: reflex.Name,
					Success:    true,
					Stopped:    true,
					Output:     vars,
					Duration:   time.Since(start),
				}, nil
			}
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
	// Persist stats
	e.saveReflexStats(reflex)

	return &ReflexResult{
		ReflexName: reflex.Name,
		Success:    true,
		Output:     vars,
		Duration:   time.Since(start),
	}, nil
}

// reflexStats holds stats for all reflexes, stored separately from config
type reflexStats struct {
	Stats map[string]struct {
		FireCount int       `json:"fire_count"`
		LastFired time.Time `json:"last_fired"`
	} `json:"stats"`
}

// statsFilePath returns the path to the stats file
func (e *Engine) statsFilePath() string {
	return filepath.Join(e.reflexDir, "reflex-stats.json")
}

// loadStats loads stats from the separate stats file and merges into loaded reflexes
func (e *Engine) loadStats() {
	data, err := os.ReadFile(e.statsFilePath())
	if err != nil {
		// No stats file yet, that's fine
		return
	}

	var stats reflexStats
	if err := json.Unmarshal(data, &stats); err != nil {
		log.Printf("[reflex] Failed to parse stats file: %v", err)
		return
	}

	// Merge stats into loaded reflexes
	for name, s := range stats.Stats {
		if reflex, ok := e.reflexes[name]; ok {
			reflex.FireCount = s.FireCount
			reflex.LastFired = s.LastFired
		}
	}
}

// saveReflexStats persists FireCount and LastFired to a separate stats file
// This avoids overwriting manual config edits
func (e *Engine) saveReflexStats(reflex *Reflex) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Load existing stats
	var stats reflexStats
	data, err := os.ReadFile(e.statsFilePath())
	if err == nil {
		json.Unmarshal(data, &stats)
	}
	if stats.Stats == nil {
		stats.Stats = make(map[string]struct {
			FireCount int       `json:"fire_count"`
			LastFired time.Time `json:"last_fired"`
		})
	}

	// Update stats for this reflex
	stats.Stats[reflex.Name] = struct {
		FireCount int       `json:"fire_count"`
		LastFired time.Time `json:"last_fired"`
	}{
		FireCount: reflex.FireCount,
		LastFired: reflex.LastFired,
	}

	// Write back
	data, err = json.MarshalIndent(stats, "", "  ")
	if err != nil {
		log.Printf("[reflex] Failed to marshal stats: %v", err)
		return
	}

	if err := os.WriteFile(e.statsFilePath(), data, 0644); err != nil {
		log.Printf("[reflex] Failed to write stats file: %v", err)
	}
}

func (e *Engine) executeReply(step PipelineStep, vars map[string]any) error {
	message := ""
	if m, ok := step.Params["message"].(string); ok {
		rendered, err := renderTemplate(m, vars)
		if err != nil {
			return err
		}
		message = rendered
	}

	if message == "" {
		return fmt.Errorf("message required for reply")
	}

	// Check if this is a slash command - use interaction followup response
	// (We already sent a deferred response, so need to edit it)
	if token, ok := vars["interaction_token"].(string); ok && token != "" {
		appID, _ := vars["app_id"].(string)
		if e.onInteractionReply != nil && appID != "" {
			return e.onInteractionReply(token, appID, message)
		}
		// Fall through to regular reply if no interaction callback
	}

	// Regular message reply
	if e.onReply == nil {
		return fmt.Errorf("reply callback not configured")
	}

	// Try to get channel_id from vars (e.g., Discord message context)
	channelID := ""
	if ch, ok := vars["channel_id"].(string); ok {
		channelID = ch
	}

	// Fall back to default channel for notifications (e.g., calendar reminders)
	if channelID == "" {
		channelID = e.defaultChannel
	}

	if channelID == "" {
		return fmt.Errorf("channel_id required (none in context and no default configured)")
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
	// Proactive mode check: if attention is actively focusing on this source/domain,
	// bypass reflexes and route directly to executive
	if e.attention != nil {
		// Check by source (e.g., "discord", "calendar")
		if e.attention.IsAttending(source) {
			log.Printf("[reflex] Bypassing reflexes: attention is attending to %q", source)
			return false, nil
		}
		// Check by type (e.g., "gtd", "message")
		if e.attention.IsAttending(typ) {
			log.Printf("[reflex] Bypassing reflexes: attention is attending to %q", typ)
			return false, nil
		}
		// Check for "all" domain (executive wants everything)
		if e.attention.IsAttending("all") {
			log.Printf("[reflex] Bypassing reflexes: attention is attending to all")
			return false, nil
		}
	}

	matches := e.Match(source, typ, content)
	if len(matches) == 0 {
		return false, nil
	}

	// Filter by slash command routing
	// If message has slash_command: only match reflexes for that command
	// If message has no slash_command: only match reflexes without slash command requirement
	slashCmd, hasSlash := data["slash_command"].(string)
	filtered := matches[:0]
	for _, reflex := range matches {
		if hasSlash {
			// Message is from a slash command - only match slash-specific reflexes
			if reflex.Trigger.SlashCommand == slashCmd {
				filtered = append(filtered, reflex)
			}
		} else {
			// Normal message - only match reflexes without slash command requirement
			if reflex.Trigger.SlashCommand == "" {
				filtered = append(filtered, reflex)
			}
		}
	}
	matches = filtered

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

// createGTDActions registers GTD-related actions that need engine access
func (e *Engine) createGTDActions() {
	e.actions.Register("gtd_list", ActionFunc(func(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
		if e.gtdStore == nil {
			return nil, fmt.Errorf("GTD store not configured")
		}

		when := resolveVar(params, vars, "when")
		if when == "" {
			when = "today" // default
		}

		tasks := e.gtdStore.GetTasks(when, "", "")
		if len(tasks) == 0 {
			return fmt.Sprintf("No tasks for %s", when), nil
		}

		var lines []string
		for i, t := range tasks {
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, t.Title))
		}
		return strings.Join(lines, "\n"), nil
	}))

	e.actions.Register("gtd_add", ActionFunc(func(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
		if e.gtdStore == nil {
			return nil, fmt.Errorf("GTD store not configured")
		}

		title := resolveVar(params, vars, "title")
		if title == "" {
			return nil, fmt.Errorf("title is required")
		}

		task := &gtd.Task{
			Title: title,
			When:  "inbox",
		}
		if notes := resolveVar(params, vars, "notes"); notes != "" {
			task.Notes = notes
		}

		e.gtdStore.AddTask(task)
		if err := e.gtdStore.Save(); err != nil {
			return nil, fmt.Errorf("failed to save: %w", err)
		}

		return fmt.Sprintf("Added '%s' to inbox", title), nil
	}))

	e.actions.Register("gtd_complete", ActionFunc(func(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
		if e.gtdStore == nil {
			return nil, fmt.Errorf("GTD store not configured")
		}

		identifier := resolveVar(params, vars, "id", "title")
		if identifier == "" {
			return nil, fmt.Errorf("id or title is required")
		}

		// Try to find task
		task := e.gtdStore.FindTaskByTitle(identifier)
		if task == nil {
			return nil, fmt.Errorf("task not found: %s", identifier)
		}

		if err := e.gtdStore.CompleteTask(task.ID); err != nil {
			return nil, err
		}
		if err := e.gtdStore.Save(); err != nil {
			return nil, fmt.Errorf("failed to save: %w", err)
		}

		return fmt.Sprintf("Completed '%s'", task.Title), nil
	}))

	e.actions.Register("gtd_dispatch", ActionFunc(func(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
		if e.gtdStore == nil {
			return nil, fmt.Errorf("GTD store not configured")
		}

		intent, _ := vars["intent"].(string)
		content, _ := vars["content"].(string)

		switch intent {
		case "gtd_show_today":
			allTasks := e.gtdStore.GetTasks("today", "", "")
			tasks := filterOpenTasks(allTasks)
			if len(tasks) == 0 {
				return "No tasks for today", nil
			}
			lines := formatTaskList(tasks, 1800)
			header := "Today's tasks:\n"
			if len(lines) < len(tasks) {
				header = fmt.Sprintf("Today (%d of %d):\n", len(lines), len(tasks))
			}
			return header + strings.Join(lines, "\n"), nil

		case "gtd_show_inbox":
			allTasks := e.gtdStore.GetTasks("inbox", "", "")
			tasks := filterOpenTasks(allTasks)
			if len(tasks) == 0 {
				return "Inbox is empty", nil
			}
			lines := formatTaskList(tasks, 1800)
			header := "Inbox:\n"
			if len(lines) < len(tasks) {
				header = fmt.Sprintf("Inbox (%d of %d):\n", len(lines), len(tasks))
			}
			return header + strings.Join(lines, "\n"), nil

		case "gtd_show_logbook":
			allTasks := e.gtdStore.GetTasks("", "", "")
			var tasks []gtd.Task
			for _, t := range allTasks {
				if t.Status == "completed" || t.Status == "canceled" {
					tasks = append(tasks, t)
				}
			}
			if len(tasks) == 0 {
				return "Logbook is empty", nil
			}
			lines := formatTaskList(tasks, 1800)
			header := "Logbook:\n"
			if len(lines) < len(tasks) {
				header = fmt.Sprintf("Logbook (%d of %d):\n", len(lines), len(tasks))
			}
			return header + strings.Join(lines, "\n"), nil

		case "gtd_add_inbox":
			// Extract what to add from content
			item := content
			if idx := strings.Index(strings.ToLower(content), "add "); idx >= 0 {
				item = content[idx+4:]
			}
			if idx := strings.Index(strings.ToLower(item), " to inbox"); idx >= 0 {
				item = item[:idx]
			}
			item = strings.TrimSpace(item)

			if item == "" {
				return nil, fmt.Errorf("couldn't extract item to add")
			}

			task := &gtd.Task{Title: item, When: "inbox"}
			e.gtdStore.AddTask(task)
			if err := e.gtdStore.Save(); err != nil {
				return nil, fmt.Errorf("failed to save: %w", err)
			}
			return fmt.Sprintf("Added '%s' to inbox", item), nil

		case "gtd_complete":
			return nil, fmt.Errorf("gtd_complete via dispatch not yet implemented - use direct action")

		default:
			return nil, fmt.Errorf("unknown GTD intent: %s", intent)
		}
	}))

	// Bud task actions
	e.actions.Register("complete_bud_task", ActionFunc(func(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
		if e.budTaskStore == nil {
			return nil, fmt.Errorf("bud task store not configured")
		}

		// Get task_id from params or vars (impulse data has task_id)
		taskID := resolveVar(params, vars, "task_id")
		if taskID == "" {
			// Try to get from impulse data
			if impulse, ok := vars["impulse"].(map[string]any); ok {
				if id, ok := impulse["task_id"].(string); ok {
					taskID = id
				}
			}
		}

		if taskID == "" {
			return nil, fmt.Errorf("task_id is required")
		}

		e.budTaskStore.Complete(taskID)
		if err := e.budTaskStore.Save(); err != nil {
			return nil, fmt.Errorf("failed to save: %w", err)
		}

		return fmt.Sprintf("Completed bud task '%s'", taskID), nil
	}))
}

// createCalendarActions registers calendar-related actions
func (e *Engine) createCalendarActions() {
	e.actions.Register("calendar_today", ActionFunc(func(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
		if e.calendarClient == nil {
			return nil, fmt.Errorf("calendar not configured")
		}

		events, err := e.calendarClient.GetTodayEvents(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get today's events: %w", err)
		}

		if len(events) == 0 {
			return "No events scheduled for today.", nil
		}

		var lines []string
		for _, event := range events {
			lines = append(lines, event.FormatEventSummary())
		}

		return fmt.Sprintf("Today's schedule (%d events):\n%s", len(events), strings.Join(lines, "\n")), nil
	}))

	e.actions.Register("calendar_upcoming", ActionFunc(func(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
		if e.calendarClient == nil {
			return nil, fmt.Errorf("calendar not configured")
		}

		durationStr := resolveVar(params, vars, "duration")
		duration := 24 * time.Hour // default
		if durationStr != "" {
			if d, err := time.ParseDuration(durationStr); err == nil {
				duration = d
			}
		}

		events, err := e.calendarClient.GetUpcomingEvents(ctx, duration, 10)
		if err != nil {
			return nil, fmt.Errorf("failed to get upcoming events: %w", err)
		}

		if len(events) == 0 {
			return fmt.Sprintf("No events in the next %s.", duration), nil
		}

		var lines []string
		for _, event := range events {
			lines = append(lines, event.FormatEventSummary())
		}

		return fmt.Sprintf("Upcoming events (%d):\n%s", len(events), strings.Join(lines, "\n")), nil
	}))

	e.actions.Register("calendar_dispatch", ActionFunc(func(ctx context.Context, params map[string]any, vars map[string]any) (any, error) {
		if e.calendarClient == nil {
			return nil, fmt.Errorf("calendar not configured")
		}

		intent, _ := vars["intent"].(string)

		switch intent {
		case "calendar_today":
			events, err := e.calendarClient.GetTodayEvents(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get today's events: %w", err)
			}

			if len(events) == 0 {
				return "No events scheduled for today.", nil
			}

			var lines []string
			for _, event := range events {
				lines = append(lines, event.FormatEventSummary())
			}

			return fmt.Sprintf("Today's schedule (%d events):\n%s", len(events), strings.Join(lines, "\n")), nil

		case "calendar_upcoming":
			events, err := e.calendarClient.GetUpcomingEvents(ctx, 24*time.Hour, 10)
			if err != nil {
				return nil, fmt.Errorf("failed to get upcoming events: %w", err)
			}

			if len(events) == 0 {
				return "No events in the next 24 hours.", nil
			}

			var lines []string
			for _, event := range events {
				lines = append(lines, event.FormatEventSummary())
			}

			return fmt.Sprintf("Upcoming events (%d):\n%s", len(events), strings.Join(lines, "\n")), nil

		case "calendar_query":
			// Query events in a date range
			events, err := e.calendarClient.GetUpcomingEvents(ctx, 7*24*time.Hour, 20)
			if err != nil {
				return nil, fmt.Errorf("failed to query events: %w", err)
			}

			if len(events) == 0 {
				return "No events found in the next week.", nil
			}

			var lines []string
			var currentDay string
			for _, event := range events {
				day := event.Start.Format("Monday, Jan 2")
				if day != currentDay {
					if currentDay != "" {
						lines = append(lines, "")
					}
					lines = append(lines, day+":")
					currentDay = day
				}
				lines = append(lines, "  "+event.FormatEventSummary())
			}

			return fmt.Sprintf("Events this week (%d):\n%s", len(events), strings.Join(lines, "\n")), nil

		case "calendar_free":
			// Check availability for today
			now := time.Now()
			endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())

			busy, err := e.calendarClient.FreeBusy(ctx, calendar.FreeBusyParams{
				TimeMin: now,
				TimeMax: endOfDay,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to check availability: %w", err)
			}

			if len(busy) == 0 {
				return "You're free for the rest of today!", nil
			}

			var totalBusyMins float64
			var busyTimes []string
			for _, b := range busy {
				totalBusyMins += b.End.Sub(b.Start).Minutes()
				busyTimes = append(busyTimes, fmt.Sprintf("%s - %s",
					b.Start.Format("3:04 PM"),
					b.End.Format("3:04 PM")))
			}

			remainingMins := endOfDay.Sub(now).Minutes()
			freeMins := remainingMins - totalBusyMins

			return fmt.Sprintf("Today's availability:\nBusy times: %s\nFree time remaining: %.0f minutes",
				strings.Join(busyTimes, ", "), freeMins), nil

		default:
			return nil, fmt.Errorf("unknown calendar intent: %s", intent)
		}
	}))
}

// filterOpenTasks returns only tasks with status "open"
func filterOpenTasks(tasks []gtd.Task) []gtd.Task {
	var result []gtd.Task
	for _, t := range tasks {
		if t.Status == "open" {
			result = append(result, t)
		}
	}
	return result
}

// formatTaskList formats tasks into numbered lines, truncating to fit within maxLen
func formatTaskList(tasks []gtd.Task, maxLen int) []string {
	var lines []string
	totalLen := 0
	for i, t := range tasks {
		line := fmt.Sprintf("%d. %s", i+1, t.Title)
		// Truncate long titles
		if len(line) > 80 {
			line = line[:77] + "..."
		}
		// Check if adding this line would exceed limit
		newLen := totalLen + len(line)
		if len(lines) > 0 {
			newLen++ // account for newline
		}
		if newLen > maxLen {
			break
		}
		lines = append(lines, line)
		totalLen = newLen
	}
	return lines
}
