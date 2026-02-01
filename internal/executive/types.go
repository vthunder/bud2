package executive

import "encoding/json"

// ClaudeConfig holds configuration for Claude sessions
type ClaudeConfig struct {
	// Model to use (default: claude-sonnet-4-20250514)
	Model string
	// Working directory for Claude
	WorkDir string
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
	CostUSD   float64       `json:"costUSD,omitempty"`
	TotalCost float64       `json:"totalCost,omitempty"`
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
