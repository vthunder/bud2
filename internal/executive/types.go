package executive

import (
	"fmt"
	"time"

	claudecode "github.com/severity1/claude-agent-sdk-go"
	"github.com/vthunder/bud2/internal/executive/provider"
)

// ClaudeConfig holds configuration for Claude sessions
type ClaudeConfig struct {
	// Model to use (default: claude-sonnet-4-20250514)
	Model string
	// Working directory for Claude
	WorkDir string

	// MCPServerURL is the HTTP URL for the bud2 MCP server.
	// When set, passed explicitly via WithMcpServers so the SDK subprocess
	// doesn't have to auto-discover it.
	MCPServerURL string

	// PromptMode controls how the prompt is constructed
	// "bud" (default): Full Bud system prompt with identity, memories, tools
	// "custom": Use CustomSystemPrompt verbatim, skip Bud-specific additions
	PromptMode string

	// CustomSystemPrompt for custom mode (ignored in bud mode)
	// If set in custom mode, replaces the entire system prompt
	CustomSystemPrompt string

	// AgentDefs registers programmatic agent definitions with the SDK's built-in Agent tool.
	// When set, the SDK can resolve "namespace:name" style agent references (e.g.
	// "autopilot-vision:explorer") without ~/.claude/agents/ file management.
	AgentDefs map[string]claudecode.AgentDefinition
}

// SessionUsage is an alias for provider.SessionUsage so the executive package
// uses the canonical type from the provider package.
type SessionUsage = provider.SessionUsage

// DebugEventType identifies the kind of debug event
type DebugEventType string

const (
	DebugEventSessionStart DebugEventType = "session_start"
	DebugEventText         DebugEventType = "text"
	DebugEventToolCall     DebugEventType = "tool_call"
	DebugEventSessionEnd   DebugEventType = "session_end"
)

// DebugEvent carries real-time information about an active executive session.
// Emitted for each session_start, text output chunk, tool call, and session_end.
type DebugEvent struct {
	Type DebugEventType
	At   time.Time // when the event was emitted

	// session_start
	ItemID   string
	Focus    string // truncated item content
	Priority string

	// text
	Text string

	// tool_call
	Tool string
	Args map[string]any

	// session_end
	Duration float64
	Usage    *SessionUsage
	Err      error
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// formatMemoryTimestamp formats a memory timestamp for display in prompts
// Shows relative time if recent, otherwise shows date
func formatMemoryTimestamp(t time.Time) string {
	if t.IsZero() {
		return "Unknown"
	}

	now := time.Now()
	diff := now.Sub(t)

	// Less than 1 hour: show minutes
	if diff < time.Hour {
		mins := int(diff.Minutes())
		if mins == 0 {
			return "Just now"
		}
		return fmt.Sprintf("%dm ago", mins)
	}

	// Less than 24 hours: show hours
	if diff < 24*time.Hour {
		hours := int(diff.Hours())
		return fmt.Sprintf("%dh ago", hours)
	}

	// Less than 7 days: show days
	if diff < 7*24*time.Hour {
		days := int(diff.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	}

	// Otherwise show date
	return t.Format("2006-01-02")
}
