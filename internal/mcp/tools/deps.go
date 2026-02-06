// Package tools provides MCP tool registration with dependency injection.
package tools

import (
	"github.com/vthunder/bud2/internal/activity"
	"github.com/vthunder/bud2/internal/embedding"
	"github.com/vthunder/bud2/internal/eval"
	"github.com/vthunder/bud2/internal/graph"
	"github.com/vthunder/bud2/internal/gtd"
	"github.com/vthunder/bud2/internal/integrations/calendar"
	"github.com/vthunder/bud2/internal/integrations/github"
	"github.com/vthunder/bud2/internal/motivation"
	"github.com/vthunder/bud2/internal/reflex"
	"github.com/vthunder/bud2/internal/state"
)

// Dependencies holds all services that MCP tools may need.
// Optional fields may be nil.
type Dependencies struct {
	// Core services (required)
	GraphDB        *graph.DB
	ActivityLog    *activity.Log
	StateInspector *state.Inspector
	Embedder       *embedding.Client

	// Paths
	StatePath      string
	SystemPath     string
	QueuesPath     string
	DefaultChannel string // Discord channel ID

	// Motivation stores
	TaskStore *motivation.TaskStore
	IdeaStore *motivation.IdeaStore

	// Optional services
	ReflexEngine   *reflex.Engine
	GTDStore       *gtd.GTDStore
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
}
