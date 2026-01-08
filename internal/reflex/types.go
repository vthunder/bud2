package reflex

import (
	"regexp"
	"time"
)

// Reflex is a pattern-action rule defined in YAML
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

// MatchResult contains the result of matching a reflex
type MatchResult struct {
	Matched   bool
	Extracted map[string]string
	Intent    string // populated for ollama classifier
}

// Pipeline is a sequence of actions to execute
type Pipeline []PipelineStep

// PipelineStep is a single action in a pipeline
type PipelineStep struct {
	Action string         `yaml:"action"` // action name (fetch_url, ollama_prompt, reply, etc)
	Input  string         `yaml:"input"`  // input variable (e.g., $url)
	Output string         `yaml:"output"` // output variable name
	Params map[string]any `yaml:",inline"` // action-specific parameters
}

// Match checks if this reflex matches a percept
func (r *Reflex) Match(source, typ, content string) MatchResult {
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
	ReflexName string
	Success    bool
	Output     map[string]any
	Error      error
	Duration   time.Duration
}
