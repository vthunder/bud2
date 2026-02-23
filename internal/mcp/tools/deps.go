// Package tools provides MCP tool registration with dependency injection.
package tools

import (
	"github.com/vthunder/bud2/internal/activity"
	"github.com/vthunder/bud2/internal/engram"
	"github.com/vthunder/bud2/internal/eval"
	"github.com/vthunder/bud2/internal/gtd"
	"github.com/vthunder/bud2/internal/integrations/calendar"
	"github.com/vthunder/bud2/internal/integrations/github"
	"github.com/vthunder/bud2/internal/reflex"
	"github.com/vthunder/bud2/internal/state"
)

// Dependencies holds all services that MCP tools may need.
// Optional fields may be nil.
type Dependencies struct {
	// Core services (required)
	EngramClient   *engram.Client
	ActivityLog    *activity.Log
	StateInspector *state.Inspector

	// Paths
	StatePath      string
	SystemPath     string
	QueuesPath     string
	DefaultChannel string // Discord channel ID

	// Optional services
	ReflexEngine   *reflex.Engine
	GTDStore       gtd.Store
	MemoryJudge    *eval.Judge
	CalendarClient *calendar.Client
	GitHubClient   *github.Client

	// Callbacks for direct effector access (instead of file-based)
	// If set, talk_to_user will use this instead of writing to outbox
	SendMessage func(channelID, message string) error
	// If set, discord_react will use this to add reactions
	AddReaction func(channelID, messageID, emoji string) error
	// If set, save_thought will use this instead of writing to file
	AddThought func(content string) error
	// If set, signal_done will use this to send completion signals
	SendSignal func(signalType, content string, extra map[string]any) error
	// If set, MCP tools will call this to notify that they've been executed
	// Used to detect user responses (talk_to_user, discord_react) for validation
	OnMCPToolCall func(toolName string)
}
