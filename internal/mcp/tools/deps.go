// Package tools provides MCP tool registration with dependency injection.
package tools

import (
	"github.com/vthunder/bud2/internal/activity"
	"github.com/vthunder/bud2/internal/engram"
	"github.com/vthunder/bud2/internal/eval"
	"github.com/vthunder/bud2/internal/extensions"
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
	MemoryJudge    *eval.Judge
	CalendarClient *calendar.Client
	GitHubClient   *github.Client

	// Callbacks for direct effector access (instead of file-based)
	// If set, talk_to_user will use this instead of writing to outbox
	SendMessage func(channelID, message string) error
	// If set, discord_react will use this to add reactions
	AddReaction func(channelID, messageID, emoji string) error
	// If set, send_image will use this to send files
	SendFile func(channelID, filePath, message string) error
	// If set, save_thought will use this instead of writing to file
	AddThought func(content string) error
	// If set, signal_done will use this to send completion signals
	SendSignal func(signalType, content string, extra map[string]any) error
	// If set, MCP tools will call this to notify that they've been executed
	// Used to detect user responses (talk_to_user, discord_react) for validation
	OnMCPToolCall func(toolName string)

	// GK knowledge graph tools (optional — injected when GK_PATH is configured).
	// GKCallTool proxies a tool call to the GK process for the given domain.
	// domain is a path like "/" or "/projects/foo"; toolName is the raw GK tool name
	// (without the gk_ prefix); args are forwarded as-is to the GK process.
	GKCallTool func(domain, toolName string, args map[string]any) (string, error)

	// ReadResource reads an MCP resource by URI, routing to the appropriate provider for the given domain.
	// uri is a resource URI like "gk://guides/extraction".
	ReadResource func(domain, uri string) (string, error)

	// RegisterSession registers a session token with an agent ID and default domain
	// so the MCP HTTP handler can inject the domain into gk_* tool calls.
	RegisterSession func(token, agentID, domain string)

	// MCPBaseURL is the base URL of the bud2 MCP HTTP server (e.g. "http://127.0.0.1:8066").
	// Used to construct per-subagent tokenized URLs.
	MCPBaseURL string

	// Subagent management callbacks (optional — injected by executive)
	// SpawnSubagent starts a new subagent session and returns its ID.
	// profile is optional — if non-empty, loads the named agent from state/system/plugins/.
	// workflowInstanceID and workflowStep are optional workflow tracking fields.
	// mcpURL overrides the MCP server URL for this subagent (used for domain routing).
	SpawnSubagent func(task, systemPromptAppend, profile, workflowInstanceID, workflowStep, mcpURL string) (id string, logPath string, err error)
	// ListSubagents returns a snapshot of active subagent sessions.
	ListSubagents func() []map[string]any
	// AnswerSubagent routes an answer to a waiting subagent.
	AnswerSubagent func(sessionID, answer string) error
	// GetSubagentStatus returns (status, result, claudeSessionID, pendingQuestion, error) for a session.
	GetSubagentStatus func(sessionID string) (status, result, claudeSessionID, pendingQuestion string, err error)
	// StopSubagent cancels a running subagent session.
	StopSubagent func(sessionID string) error
	// GetSubagentLog returns the last N activity events for a session.
	// Events include tool calls and text snippets with timestamps.
	GetSubagentLog func(sessionID string, lastN int) ([]map[string]any, error)
	// DrainSubagentMemories returns the staged save_thought memories for a session
	// and clears the staging area. The caller should flush these to Engram.
	DrainSubagentMemories func(sessionID string) ([]string, error)
	// PeekSubagentMemories returns the count of staged memories for a session without draining.
	PeekSubagentMemories func(sessionID string) int
	// ListSubagentMemories returns the content of all staged memories for a session without draining.
	ListSubagentMemories func(sessionID string) []string
	// ListJobs returns available job templates. If project is empty, returns only global jobs.
	ListJobs func(project string) ([]any, error)

	// VMControlURL is the base URL for the vm-control-server REST API.
	// Defaults to http://127.0.0.1:3099 if empty.
	VMControlURL string

	// ExtensionRegistry provides access to loaded extensions for skill/workflow/agent
	// discovery via the invoke_workflow and Skill MCP tools. Optional — when nil,
	// those tools are not registered.
	ExtensionRegistry *extensions.Registry
}
