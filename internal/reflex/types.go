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
	Level       int      `yaml:"level"` // 0=pattern, 1=heuristic, 2=ollama, 3=executive

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
func (r *Reflex) Match(source, typ, content string) (bool, map[string]string) {
	// Check source filter
	if r.Trigger.Source != "" && r.Trigger.Source != source {
		return false, nil
	}

	// Check type filter
	if r.Trigger.Type != "" && r.Trigger.Type != typ {
		return false, nil
	}

	// Check pattern
	if r.Trigger.Pattern == "" {
		return true, nil // no pattern means always match (if source/type match)
	}

	// Compile pattern if needed
	if r.compiledPattern == nil {
		compiled, err := regexp.Compile(r.Trigger.Pattern)
		if err != nil {
			return false, nil
		}
		r.compiledPattern = compiled
	}

	// Match pattern
	matches := r.compiledPattern.FindStringSubmatch(content)
	if matches == nil {
		return false, nil
	}

	// Extract named groups
	extracted := make(map[string]string)
	for i, name := range r.Trigger.Extract {
		if i+1 < len(matches) {
			extracted[name] = matches[i+1]
		}
	}

	return true, extracted
}

// ReflexResult is the result of executing a reflex
type ReflexResult struct {
	ReflexName string
	Success    bool
	Output     map[string]any
	Error      error
	Duration   time.Duration
}
