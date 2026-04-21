package reflex

import (
	"context"
	"regexp"
	"time"
)

// WorkflowParam describes a declared parameter for a workflow.
// Parameters are validated and defaults applied at invocation time.
type WorkflowParam struct {
	Type        string `yaml:"type,omitempty"`        // "string", "integer", "boolean"
	Description string `yaml:"description,omitempty"` // human-readable description
	Required    bool   `yaml:"required,omitempty"`    // fail if not provided
	Default     any    `yaml:"default,omitempty"`     // value applied when param is absent
	Options     []any  `yaml:"options,omitempty"`     // allowed values (enum)
}

// SubagentSpawner synchronously spawns a subagent and returns its output.
// The engine calls SpawnSync for type:subagent steps; the caller blocks
// until the session completes or the context is cancelled.
type SubagentSpawner interface {
	SpawnSync(ctx context.Context, systemPrompt, task string) (string, error)
}

// CapabilityResolver resolves a capability name to its prompt body.
// Used by type:subagent steps to look up capability prompts from the extension registry.
type CapabilityResolver interface {
	ResolveCapability(name string) (body string, ok bool)
}

// Reflex is a pattern-action rule defined in YAML
type Reflex struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Callable    bool     `yaml:"callable,omitempty"`  // if true, only invokable via invoke_reflex (not auto-matched)
	Extension   string   `yaml:"extension,omitempty"` // extension name for per-extension serialization
	Trigger     Trigger  `yaml:"trigger"`
	Pipeline    Pipeline `yaml:"pipeline"`
	Level       int      `yaml:"level"`    // 0=pattern, 1=heuristic, 2=ollama, 3=executive
	Priority    int      `yaml:"priority"` // higher = fires first when multiple match

	// Workflow declaration fields (WS4 additions)
	Params  map[string]WorkflowParam `yaml:"params,omitempty"`  // declared typed parameters
	Returns string                   `yaml:"returns,omitempty"` // output var name to use as workflow result; defaults to last step's $_

	// Runtime state (persisted)
	LastFired time.Time `yaml:"last_fired,omitempty"`
	FireCount int       `yaml:"fire_count,omitempty"`

	// Runtime only (not persisted)
	compiledPattern *regexp.Regexp `yaml:"-"`
}

// Trigger defines when a reflex fires
type Trigger struct {
	Pattern      string   `yaml:"pattern"`       // regex pattern to match
	Extract      []string `yaml:"extract"`       // named capture groups to extract
	Source       string   `yaml:"source"`        // optional: only match specific sources (discord, github, etc)
	Type         string   `yaml:"type"`          // optional: only match specific types (message, notification, etc)
	SlashCommand string   `yaml:"slash_command"` // optional: only match this Discord slash command

	// Classifier configuration
	Classifier string   `yaml:"classifier"` // "regex" (default), "ollama", or "none"
	Model      string   `yaml:"model"`      // Ollama model for classifier:ollama (default: qwen2.5:7b)
	Intents    []string `yaml:"intents"`    // valid intents for Ollama classification
	Prompt     string   `yaml:"prompt"`     // optional custom classification prompt
}

// MatchResult contains the result of matching a reflex
type MatchResult struct {
	Matched   bool
	Extracted map[string]string
	Intent    string // populated for ollama classifier
}

// Pipeline is a sequence of steps to execute
type Pipeline []PipelineStep

// PipelineStep is a single step in a pipeline.
// Steps are either action-based (backward-compatible) or type-based (new WS4 step kinds).
type PipelineStep struct {
	// Type selects the step kind: "subagent" or "invoke".
	// If empty, the step is an action step using the Action field (backward-compatible).
	Type   string `yaml:"type,omitempty"`
	Action string `yaml:"action,omitempty"` // action name (fetch_url, ollama_prompt, reply, etc)
	Input  string `yaml:"input,omitempty"`  // input variable reference (e.g., $url)
	Output string `yaml:"output,omitempty"` // output variable name

	// type:subagent fields — spawns a session with a named capability's prompt
	Agent string `yaml:"agent,omitempty"` // capability name, e.g. "researcher" or "ext:cap"

	// type:invoke fields — calls another workflow/reflex by name
	Workflow   string         `yaml:"workflow,omitempty"` // target workflow name; {{var}} template expressions allowed
	StepParams map[string]any `yaml:"params,omitempty"`   // params to pass to the sub-workflow
	// on_missing is read from the inline Params map ("on_missing": "escalate") to avoid
	// conflicts with existing invoke_reflex action usage. Not a named field.

	// type:direct fields — calls an extension action through the ActionProxy
	Tool string `yaml:"tool,omitempty"` // action name in <ext>:<cap> format; {{var}} template expressions allowed

	// Per-step error handling
	OnError        string `yaml:"on_error,omitempty"`         // "stop" (default), "skip", or "retry"
	MaxRetries     int    `yaml:"max_retries,omitempty"`       // for on_error:retry; default 3
	RetryDelaySecs int    `yaml:"retry_delay_seconds,omitempty"` // seconds between retries; default 1

	// Params holds inline action-specific parameters for action steps.
	// MUST be last; yaml:",inline" captures all YAML keys not matched by named fields above.
	Params map[string]any `yaml:",inline"`
}

// Match checks if this reflex matches a percept
func (r *Reflex) Match(source, typ, content string) MatchResult {
	// Callable reflexes are only invokable via invoke_reflex, not auto-matched
	if r.Callable {
		return MatchResult{Matched: false}
	}

	// Check source filter
	if r.Trigger.Source != "" && r.Trigger.Source != source {
		return MatchResult{Matched: false}
	}

	// Check type filter
	if r.Trigger.Type != "" && r.Trigger.Type != typ {
		return MatchResult{Matched: false}
	}

	// Determine classifier type (default to "regex")
	classifier := r.Trigger.Classifier
	if classifier == "" {
		classifier = "regex"
	}

	switch classifier {
	case "none":
		// Always match if filters pass
		return MatchResult{Matched: true, Extracted: make(map[string]string)}

	case "ollama":
		// Return matched=true if filters pass; actual classification happens in engine
		return MatchResult{Matched: true, Extracted: make(map[string]string)}

	default: // "regex"
		// Check pattern
		if r.Trigger.Pattern == "" {
			return MatchResult{Matched: true, Extracted: make(map[string]string)}
		}

		// Compile pattern if needed
		if r.compiledPattern == nil {
			compiled, err := regexp.Compile(r.Trigger.Pattern)
			if err != nil {
				return MatchResult{Matched: false}
			}
			r.compiledPattern = compiled
		}

		// Match pattern
		matches := r.compiledPattern.FindStringSubmatch(content)
		if matches == nil {
			return MatchResult{Matched: false}
		}

		// Extract named groups
		extracted := make(map[string]string)
		for i, name := range r.Trigger.Extract {
			if i+1 < len(matches) {
				extracted[name] = matches[i+1]
			}
		}

		return MatchResult{Matched: true, Extracted: extracted}
	}
}

// ReflexResult is the result of executing a reflex
type ReflexResult struct {
	ReflexName      string
	Success         bool
	Stopped         bool           // true if pipeline stopped early via gate
	Escalate        bool           // true if pipeline wants to escalate to executive
	EscalateMessage string         // human-readable reason (from explicit escalate action)
	EscalateStep    int            // which pipeline step triggered escalation
	EscalateVars    map[string]any // snapshot of accumulated vars at escalation point
	Output          map[string]any
	Error           error
	Duration        time.Duration
}
