# GTD Reflexes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add reflex-based handling for simple GTD operations with Ollama classification and immediate trace creation.

**Architecture:** Reflexes process raw Discord input before attention routing. Ollama classifies intent for natural language handling. Immediate traces preserve context for follow-up questions.

**Tech Stack:** Go, Ollama (qwen2.5:7b), YAML reflexes

---

### Task 1: Add Classifier Fields to Trigger Type

**Files:**
- Modify: `internal/reflex/types.go:22-28`
- Test: `internal/reflex/reflex_test.go`

**Step 1: Add new fields to Trigger struct**

Add after line 27 in `internal/reflex/types.go`:

```go
// Trigger defines when a reflex fires
type Trigger struct {
	Pattern string   `yaml:"pattern"` // regex pattern to match
	Extract []string `yaml:"extract"` // named capture groups to extract
	Source  string   `yaml:"source"`  // optional: only match specific sources (discord, github, etc)
	Type    string   `yaml:"type"`    // optional: only match specific types (message, notification, etc)

	// Classifier configuration
	Classifier string   `yaml:"classifier"` // "regex" (default), "ollama", or "none"
	Model      string   `yaml:"model"`      // Ollama model for classifier:ollama (default: qwen2.5:7b)
	Intents    []string `yaml:"intents"`    // valid intents for Ollama classification
	Prompt     string   `yaml:"prompt"`     // optional custom classification prompt
}
```

**Step 2: Add Priority field to Reflex struct**

Add after line 14 in `internal/reflex/types.go`:

```go
type Reflex struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Trigger     Trigger  `yaml:"trigger"`
	Pipeline    Pipeline `yaml:"pipeline"`
	Level       int      `yaml:"level"`    // 0=pattern, 1=heuristic, 2=ollama, 3=executive
	Priority    int      `yaml:"priority"` // higher = fires first when multiple match

	// Runtime state
	compiledPattern *regexp.Regexp
	LastFired       time.Time
	FireCount       int
}
```

**Step 3: Build to verify**

Run: `go build ./...`
Expected: Success

**Step 4: Commit**

```bash
git add internal/reflex/types.go
git commit -m "feat(reflex): add classifier fields to Trigger type"
```

---

### Task 2: Add Ollama Classification to Reflex Engine

**Files:**
- Modify: `internal/reflex/engine.go`
- Modify: `internal/reflex/types.go`
- Test: `internal/reflex/reflex_test.go`

**Step 1: Add ClassifyWithOllama method to engine.go**

Add after the `Match` function (around line 143):

```go
// ClassifyWithOllama uses Ollama to classify content into an intent
func (e *Engine) ClassifyWithOllama(ctx context.Context, trigger *Trigger, content string) (string, error) {
	model := trigger.Model
	if model == "" {
		model = "qwen2.5:7b"
	}

	// Build classification prompt
	prompt := trigger.Prompt
	if prompt == "" {
		prompt = fmt.Sprintf(`Classify this message into ONE of these intents: %s

If none match well, respond with "not_matched".

Message: %s

Respond with ONLY the intent name, nothing else.`,
			strings.Join(trigger.Intents, ", "), content)
	}

	// Call Ollama
	reqBody := map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	}

	jsonBody, _ := json.Marshal(reqBody)
	resp, err := http.Post("http://localhost:11434/api/generate", "application/json", bytes.NewReader(jsonBody))
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

	intent := strings.TrimSpace(result.Response)

	// Validate intent is in allowed list
	for _, valid := range trigger.Intents {
		if strings.EqualFold(intent, valid) {
			return valid, nil
		}
	}

	return "not_matched", nil
}
```

**Step 2: Add imports at top of engine.go**

```go
import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	// ... existing imports
)
```

**Step 3: Update Match method in types.go to return classifier type**

Update the `Match` function signature and add classifier handling:

```go
// MatchResult contains the result of matching a reflex
type MatchResult struct {
	Matched   bool
	Extracted map[string]string
	Intent    string // populated for ollama classifier
}

// Match checks if this reflex matches based on source/type filters and regex pattern
// For ollama classifier, this only checks filters - classification happens separately
func (r *Reflex) Match(source, typ, content string) MatchResult {
	result := MatchResult{Extracted: make(map[string]string)}

	// Check source filter
	if r.Trigger.Source != "" && r.Trigger.Source != source {
		return result
	}

	// Check type filter
	if r.Trigger.Type != "" && r.Trigger.Type != typ {
		return result
	}

	// Determine classifier type
	classifier := r.Trigger.Classifier
	if classifier == "" {
		classifier = "regex" // default
	}

	switch classifier {
	case "none":
		// Always match if filters pass
		result.Matched = true
		return result

	case "ollama":
		// Filters pass, but classification happens in engine
		result.Matched = true
		return result

	case "regex":
		fallthrough
	default:
		// Check pattern
		if r.Trigger.Pattern == "" {
			result.Matched = true
			return result
		}

		// Compile pattern if needed
		if r.compiledPattern == nil {
			compiled, err := regexp.Compile(r.Trigger.Pattern)
			if err != nil {
				return result
			}
			r.compiledPattern = compiled
		}

		// Match pattern
		matches := r.compiledPattern.FindStringSubmatch(content)
		if matches == nil {
			return result
		}

		result.Matched = true
		// Extract named groups
		for i, name := range r.Trigger.Extract {
			if i+1 < len(matches) {
				result.Extracted[name] = matches[i+1]
			}
		}
		return result
	}
}
```

**Step 4: Update engine.go Process to handle Ollama classification**

Update the `Process` function:

```go
// Process attempts to match and execute reflexes for raw input
// Returns: handled bool, results []*ReflexResult, outputPercept *ProcessedInput
func (e *Engine) Process(ctx context.Context, source, typ, content string, data map[string]any) (bool, []*ReflexResult) {
	// Find all potentially matching reflexes
	var candidates []*Reflex
	for _, reflex := range e.List() {
		result := reflex.Match(source, typ, content)
		if result.Matched {
			candidates = append(candidates, reflex)
		}
	}

	if len(candidates) == 0 {
		return false, nil
	}

	// Sort by priority (higher first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Priority > candidates[j].Priority
	})

	// Try each candidate until one succeeds
	for _, reflex := range candidates {
		result := reflex.Match(source, typ, content)

		// Handle Ollama classification
		if reflex.Trigger.Classifier == "ollama" {
			intent, err := e.ClassifyWithOllama(ctx, &reflex.Trigger, content)
			if err != nil {
				log.Printf("[reflex] Ollama classification failed for %s: %v", reflex.Name, err)
				continue
			}
			if intent == "not_matched" {
				continue
			}
			result.Intent = intent
			result.Extracted["intent"] = intent
		}

		// Execute pipeline
		execResult, err := e.Execute(ctx, reflex, result.Extracted, data)
		if err != nil {
			log.Printf("[reflex] Error executing %s: %v", reflex.Name, err)
			continue
		}

		if execResult.Success {
			log.Printf("[reflex] Fired: %s (%.2fms)", reflex.Name, execResult.Duration.Seconds()*1000)
			return true, []*ReflexResult{execResult}
		}
	}

	return false, nil
}
```

**Step 5: Build to verify**

Run: `go build ./...`
Expected: Success

**Step 6: Commit**

```bash
git add internal/reflex/types.go internal/reflex/engine.go
git commit -m "feat(reflex): add Ollama classification support"
```

---

### Task 3: Add Gate Action

**Files:**
- Modify: `internal/reflex/actions.go`
- Test: `internal/reflex/reflex_test.go`

**Step 1: Add gate action and StopPipeline error**

Add to `internal/reflex/actions.go`:

```go
// ErrStopPipeline signals the pipeline should stop (not an error, just early exit)
var ErrStopPipeline = fmt.Errorf("pipeline stopped")

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
```

**Step 2: Register the gate action in NewActionRegistry**

Add to the registration list:

```go
r.Register("gate", ActionFunc(actionGate))
```

**Step 3: Update Execute in engine.go to handle ErrStopPipeline**

In the `Execute` function, update the error handling:

```go
// Execute action
result, err := action.Execute(ctx, params, vars)
if err != nil {
	if err == ErrStopPipeline {
		// Pipeline stopped intentionally, this is success
		return &ReflexResult{
			ReflexName: reflex.Name,
			Success:    true,
			Output:     vars,
			Duration:   time.Since(start),
			Stopped:    true, // Add this field to ReflexResult
		}, nil
	}
	return &ReflexResult{
		ReflexName: reflex.Name,
		Success:    false,
		Error:      fmt.Errorf("step %d (%s) failed: %w", i, step.Action, err),
		Duration:   time.Since(start),
	}, nil
}
```

**Step 4: Add Stopped field to ReflexResult**

In `types.go`:

```go
type ReflexResult struct {
	ReflexName string
	Success    bool
	Output     map[string]any
	Error      error
	Duration   time.Duration
	Stopped    bool // true if pipeline stopped early via gate
}
```

**Step 5: Build to verify**

Run: `go build ./...`
Expected: Success

**Step 6: Commit**

```bash
git add internal/reflex/actions.go internal/reflex/engine.go internal/reflex/types.go
git commit -m "feat(reflex): add gate action for conditional pipeline stops"
```

---

### Task 4: Add GTD Actions

**Files:**
- Modify: `internal/reflex/actions.go`
- Modify: `internal/reflex/engine.go`

**Step 1: Add GTDStore field to Engine**

In `engine.go`, update the Engine struct:

```go
type Engine struct {
	reflexes  map[string]*Reflex
	actions   *ActionRegistry
	reflexDir string
	mu        sync.RWMutex

	// Callbacks for integration
	onReply func(channelID, message string) error
	onReact func(channelID, messageID, emoji string) error

	// GTD store for gtd_* actions
	gtdStore interface {
		GetTasks(when, projectID, areaID string) []gtd.Task
		AddTask(task *gtd.Task)
		CompleteTask(id string) error
		Save() error
		FindTaskByTitle(title string) *gtd.Task
	}
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
```

**Step 2: Add FindTaskByTitle to GTDStore**

In `internal/gtd/store.go`, add:

```go
// FindTaskByTitle finds a task by partial title match (case-insensitive)
func (s *GTDStore) FindTaskByTitle(title string) *Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	title = strings.ToLower(title)
	for i := range s.data.Tasks {
		if strings.Contains(strings.ToLower(s.data.Tasks[i].Title), title) {
			task := s.data.Tasks[i]
			return &task
		}
	}
	return nil
}
```

**Step 3: Create GTD actions in actions.go**

Add these functions:

```go
// GTD action creators - need engine reference for store access
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
			tasks := e.gtdStore.GetTasks("today", "", "")
			if len(tasks) == 0 {
				return "No tasks for today", nil
			}
			var lines []string
			for i, t := range tasks {
				lines = append(lines, fmt.Sprintf("%d. %s", i+1, t.Title))
			}
			return "Today's tasks:\n" + strings.Join(lines, "\n"), nil

		case "gtd_show_inbox":
			tasks := e.gtdStore.GetTasks("inbox", "", "")
			if len(tasks) == 0 {
				return "Inbox is empty", nil
			}
			var lines []string
			for i, t := range tasks {
				lines = append(lines, fmt.Sprintf("%d. %s", i+1, t.Title))
			}
			return "Inbox:\n" + strings.Join(lines, "\n"), nil

		case "gtd_add_inbox":
			// Extract what to add from content
			// Simple heuristic: look for "add X to inbox" pattern
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
			e.gtdStore.Save()
			return fmt.Sprintf("Added '%s' to inbox", item), nil

		case "gtd_complete":
			// Extract task reference from content
			// This is tricky - might need more sophisticated extraction
			return nil, fmt.Errorf("gtd_complete via dispatch not yet implemented - use direct action")

		default:
			return nil, fmt.Errorf("unknown GTD intent: %s", intent)
		}
	}))
}
```

**Step 4: Call createGTDActions in NewEngine**

Update `NewEngine`:

```go
func NewEngine(statePath string) *Engine {
	e := &Engine{
		reflexes:  make(map[string]*Reflex),
		actions:   NewActionRegistry(),
		reflexDir: filepath.Join(statePath, "reflexes"),
	}
	e.createGTDActions()
	return e
}
```

**Step 5: Add import for gtd package**

At top of engine.go:

```go
import (
	"github.com/vthunder/bud2/internal/gtd"
	// ... other imports
)
```

**Step 6: Build to verify**

Run: `go build ./...`
Expected: Success

**Step 7: Commit**

```bash
git add internal/reflex/engine.go internal/reflex/actions.go internal/gtd/store.go
git commit -m "feat(reflex): add GTD actions (list, add, complete, dispatch)"
```

---

### Task 5: Add CreateImmediateTrace to Attention

**Files:**
- Modify: `internal/attention/attention.go`

**Step 1: Add CreateImmediateTrace method**

Add to `attention.go`:

```go
// CreateImmediateTrace creates a trace that's immediately available
// (bypasses consolidation delay) for reflex context continuity
func (a *Attention) CreateImmediateTrace(content string, source string) *types.Trace {
	// Generate embedding
	embedding := a.embed(content)

	trace := &types.Trace{
		ID:         fmt.Sprintf("trace-%d", time.Now().UnixNano()),
		Content:    content,
		Sources:    []string{source},
		Embedding:  embedding,
		Activation: 0.8, // High activation for immediate relevance
		Strength:   1,
		CreatedAt:  time.Now(),
		LastAccess: time.Now(),
	}

	a.traces.Add(trace)
	log.Printf("[attention] Created immediate trace: %s", truncate(content, 50))

	return trace
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
```

**Step 2: Build to verify**

Run: `go build ./...`
Expected: Success

**Step 3: Commit**

```bash
git add internal/attention/attention.go
git commit -m "feat(attention): add CreateImmediateTrace for reflex context"
```

---

### Task 6: Add RawInput and ProcessedBy to Percept

**Files:**
- Modify: `internal/types/types.go`

**Step 1: Add new fields to Percept struct**

Update the Percept struct in `types.go`:

```go
type Percept struct {
	ID        string            `json:"id"`
	Source    string            `json:"source"`    // discord, github, calendar
	Type      string            `json:"type"`      // message, notification, event
	Intensity float64           `json:"intensity"` // 0.0-1.0, automatic
	Timestamp time.Time         `json:"timestamp"`
	Tags      []string          `json:"tags"`      // [from:owner], [urgent], etc
	Data      map[string]any    `json:"data"`      // source-specific payload
	Features  map[string]any    `json:"features,omitempty"` // sense-defined clustering features
	Embedding []float64         `json:"embedding,omitempty"` // semantic embedding

	// Reflex processing metadata
	RawInput    string   `json:"raw_input,omitempty"`    // original input before reflex processing
	ProcessedBy []string `json:"processed_by,omitempty"` // reflex names that processed this
}
```

**Step 2: Build to verify**

Run: `go build ./...`
Expected: Success

**Step 3: Commit**

```bash
git add internal/types/types.go
git commit -m "feat(types): add RawInput and ProcessedBy to Percept"
```

---

### Task 7: Hook Reflex Engine into Main

**Files:**
- Modify: `cmd/bud/main.go`

**Step 1: Add reflex engine initialization**

After the attention initialization (around line 130), add:

```go
// Initialize reflex engine
reflexEngine := reflex.NewEngine(statePath)
if err := reflexEngine.Load(); err != nil {
	log.Printf("Warning: failed to load reflexes: %v", err)
}
reflexEngine.SetGTDStore(gtdStore) // Wire up GTD store

// Set up reflex callbacks
reflexEngine.SetReplyCallback(func(channelID, message string) error {
	if discordEffector != nil {
		// Queue the reply
		action := &types.Action{
			ID:        fmt.Sprintf("reflex-reply-%d", time.Now().UnixNano()),
			Type:      "send_message",
			ChannelID: channelID,
			Content:   message,
			Timestamp: time.Now(),
		}
		outbox.Add(action)
	}
	return nil
})
```

**Step 2: Add GTD store initialization (if not already present)**

Around line 107, add:

```go
// Initialize GTD store
gtdStore := gtd.NewGTDStore(statePath)
if err := gtdStore.Load(); err != nil {
	log.Printf("Warning: failed to load GTD store: %v", err)
}
```

**Step 3: Update processPercept to check reflexes first**

Replace the `processPercept` function:

```go
// Process percept helper - checks reflexes first, then routes to attention
processPercept := func(percept *types.Percept) {
	// Extract content for reflex matching
	content := ""
	if c, ok := percept.Data["content"].(string); ok {
		content = c
	}

	// Try reflexes first
	ctx := context.Background()
	handled, results := reflexEngine.Process(ctx, percept.Source, percept.Type, content, percept.Data)

	if handled && len(results) > 0 {
		result := results[0]
		if result.Success {
			// Create immediate traces for follow-up context
			attn.CreateImmediateTrace(
				fmt.Sprintf("User asked: %s", content),
				"reflex-query",
			)
			if response, ok := result.Output["response"].(string); ok {
				attn.CreateImmediateTrace(
					fmt.Sprintf("Bud responded: %s", response),
					"reflex-response",
				)
			}

			// Mark percept as processed by reflex
			percept.RawInput = content
			percept.ProcessedBy = []string{result.ReflexName}
			percept.Intensity *= 0.3 // Lower intensity since reflex handled it

			log.Printf("[main] Percept %s handled by reflex %s", percept.ID, result.ReflexName)
		}
	}

	// Always add to percept pool and route (even if reflex handled)
	perceptPool.Add(percept)
	threads := attn.RoutePercept(percept, func(content string) string {
		return "respond to: " + truncate(content, 50)
	})
	log.Printf("[main] Percept %s routed to %d thread(s)", percept.ID, len(threads))
}
```

**Step 4: Add imports**

Add to imports:

```go
import (
	"github.com/vthunder/bud2/internal/gtd"
	"github.com/vthunder/bud2/internal/reflex"
	// ... existing imports
)
```

**Step 5: Build to verify**

Run: `go build ./...`
Expected: Success

**Step 6: Commit**

```bash
git add cmd/bud/main.go
git commit -m "feat(main): hook reflex engine into percept processing"
```

---

### Task 8: Create GTD Handler Reflex

**Files:**
- Create: `state/reflexes/gtd-handler.yaml`

**Step 1: Create the reflex file**

```yaml
name: gtd-handler
description: Handle GTD queries using Ollama classification
trigger:
  source: discord
  classifier: ollama
  model: qwen2.5:7b
  intents:
    - gtd_show_today
    - gtd_show_inbox
    - gtd_add_inbox
    - gtd_complete
    - not_gtd
pipeline:
  - action: gate
    condition: "{{.intent}} == not_gtd"
    stop: true
  - action: gtd_dispatch
    output: response
  - action: reply
    message: "{{.response}}"
```

**Step 2: Commit**

```bash
git add state/reflexes/gtd-handler.yaml
git commit -m "feat(reflex): add GTD handler reflex with Ollama classification"
```

---

### Task 9: Update Reflexes Documentation

**Files:**
- Modify: `state/notes/reflexes.md`

**Step 1: Add Ollama classifier documentation**

Add section after "Reflex Definition Format":

```markdown
## Classifier Types

### Regex Classifier (default)

Fast pattern matching for specific phrases:

```yaml
name: quick-add
trigger:
  source: discord
  classifier: regex  # or omit - regex is default
  pattern: "^add (.+) to inbox$"
  extract: [item]
pipeline:
  - action: gtd_add
    title: "{{.item}}"
  - action: reply
    message: "Added '{{.item}}' to inbox"
```

### Ollama Classifier

Uses local LLM for natural language understanding:

```yaml
name: gtd-handler
trigger:
  source: discord
  classifier: ollama
  model: qwen2.5:7b  # optional, this is default
  intents:
    - gtd_show_today
    - gtd_show_inbox
    - gtd_add_inbox
    - not_gtd
  # prompt: "Custom classification prompt..."  # optional
pipeline:
  - action: gate
    condition: "{{.intent}} == not_gtd"
    stop: true
  - action: gtd_dispatch
    output: response
  - action: reply
    message: "{{.response}}"
```

The `{{.intent}}` variable is populated with the classified intent.

### None Classifier

Always matches if source/type filters pass:

```yaml
name: catch-all
trigger:
  source: discord
  classifier: none
pipeline:
  # ...
```

## GTD Actions

| Action | Description | Parameters |
|--------|-------------|------------|
| `gtd_list` | List tasks | `when` (inbox, today, anytime, someday) |
| `gtd_add` | Add task to inbox | `title`, `notes` (optional) |
| `gtd_complete` | Complete a task | `id` or `title` (fuzzy match) |
| `gtd_dispatch` | Route by intent | uses `{{.intent}}` from classifier |

## Gate Action

Conditionally stop pipeline execution:

```yaml
- action: gate
  condition: "{{.intent}} == not_gtd"
  stop: true
```
```

**Step 2: Commit**

```bash
git add state/notes/reflexes.md
git commit -m "docs: add Ollama classifier and GTD actions to reflexes.md"
```

---

### Task 10: Write Integration Tests

**Files:**
- Create: `internal/reflex/gtd_test.go`

**Step 1: Create test file**

```go
package reflex

import (
	"context"
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

		if result != "Added 'Test task' to inbox" {
			t.Errorf("Unexpected result: %v", result)
		}

		// Verify task was added
		tasks := gtdStore.GetTasks("inbox", "", "")
		if len(tasks) != 1 {
			t.Errorf("Expected 1 task, got %d", len(tasks))
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
			t.Fatalf("Expected string result, got %T", result)
		}

		if !contains(resultStr, "Test task") {
			t.Errorf("Expected result to contain 'Test task', got: %s", resultStr)
		}
	})
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
	_, err := action.Execute(ctx,
		map[string]any{"condition": "{{.intent}} == not_gtd", "stop": true},
		map[string]any{"intent": "not_gtd"},
	)
	if err != ErrStopPipeline {
		t.Errorf("Expected ErrStopPipeline, got: %v", err)
	}

	// Test condition that should pass
	result, err := action.Execute(ctx,
		map[string]any{"condition": "{{.intent}} == not_gtd", "stop": true},
		map[string]any{"intent": "gtd_show_today"},
	)
	if err != nil {
		t.Errorf("Expected pass, got error: %v", err)
	}
	if result != "gate passed" {
		t.Errorf("Unexpected result: %v", result)
	}
}

func TestOllamaClassifier(t *testing.T) {
	// Skip if Ollama not available
	t.Skip("Requires running Ollama server")

	tmpDir := t.TempDir()
	engine := NewEngine(tmpDir)

	trigger := &Trigger{
		Classifier: "ollama",
		Model:      "qwen2.5:7b",
		Intents:    []string{"gtd_show_today", "gtd_add_inbox", "not_gtd"},
	}

	ctx := context.Background()

	// Test classification
	intent, err := engine.ClassifyWithOllama(ctx, trigger, "show me today's tasks")
	if err != nil {
		t.Fatalf("Classification failed: %v", err)
	}

	if intent != "gtd_show_today" {
		t.Errorf("Expected gtd_show_today, got: %s", intent)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && contains(s[1:], substr) || s[:len(substr)] == substr)
}
```

**Step 2: Run tests**

Run: `go test ./internal/reflex/... -v`
Expected: Tests pass (Ollama test skipped)

**Step 3: Commit**

```bash
git add internal/reflex/gtd_test.go
git commit -m "test(reflex): add GTD action and gate tests"
```

---

### Task 11: Final Verification

**Step 1: Run all tests**

Run: `go test ./...`
Expected: All tests pass

**Step 2: Build and verify**

Run: `go build ./cmd/bud`
Expected: Success

**Step 3: Manual test (if Ollama available)**

1. Start Ollama: `ollama serve`
2. Run bud in synthetic mode
3. Send message: `{"source": "discord", "type": "message", "data": {"content": "show today's tasks", "channel_id": "test"}}`
4. Verify reflex fires and responds

**Step 4: Final commit**

```bash
git add -A
git commit -m "feat(reflex): complete GTD reflexes implementation"
```
