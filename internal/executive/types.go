package executive

import "encoding/json"

// ClaudeConfig holds configuration for Claude sessions
type ClaudeConfig struct {
	// Model to use (default: claude-sonnet-4-20250514)
	Model string
	// Working directory for Claude
	WorkDir string
}

// SessionUsage holds token usage metrics from a Claude session
type SessionUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	NumTurns                 int `json:"num_turns"`
	DurationMs               int `json:"duration_ms"`
	DurationApiMs            int `json:"duration_api_ms"`
	ContextWindow            int `json:"context_window,omitempty"`
	MaxOutputTokens          int `json:"max_output_tokens,omitempty"`
}

// TotalInputTokens returns the total input tokens including cache operations
func (u *SessionUsage) TotalInputTokens() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// CacheHitRate returns the proportion of input tokens served from cache (0.0-1.0)
func (u *SessionUsage) CacheHitRate() float64 {
	total := u.TotalInputTokens()
	if total == 0 {
		return 0
	}
	return float64(u.CacheReadInputTokens) / float64(total)
}

// StreamEvent represents a Claude stream-json event
type StreamEvent struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content,omitempty"`
	Tool    *ToolUse        `json:"tool,omitempty"`
	Text    string          `json:"text,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	SubType string          `json:"subtype,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// ToolUse represents a tool call
type ToolUse struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
	ID   string         `json:"id"`
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
