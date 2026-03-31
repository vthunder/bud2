// Package executive provides the executive session manager for Bud.
// This file implements the subagent session infrastructure for Project 2:
// long-lived Claude subagents that work autonomously under executive supervision.
//
// Question routing uses AskUserQuestion interception via CanUseTool:
//  1. Subagent calls AskUserQuestion.
//  2. CanUseTool hook fires, blocks on answerReady channel.
//  3. Executive receives QuestionNotify, routes question to user.
//  4. On answer, executive calls Answer() which sends on answerReady.
//  5. Hook unblocks, returns deny message containing the answer.
//  6. Claude receives the answer inline and continues — no restart needed.
package executive

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	claudecode "github.com/severity1/claude-agent-sdk-go"
)

// SubagentEventKind categorizes a logged subagent event.
type SubagentEventKind string

const (
	SubagentEventToolCall  SubagentEventKind = "tool_call"
	SubagentEventText      SubagentEventKind = "text"
	SubagentEventThinking  SubagentEventKind = "thinking"
	SubagentEventError     SubagentEventKind = "error"
)

// SubagentEvent records a discrete activity from a subagent session.
type SubagentEvent struct {
	Kind     SubagentEventKind `json:"kind"`
	At       time.Time         `json:"at"`
	ToolName string            `json:"tool,omitempty"` // for tool_call
	Summary  string            `json:"summary"`
}

const maxSubagentEvents = 50

// SubagentStatus describes the current lifecycle state of a subagent session.
type SubagentStatus int

const (
	SubagentRunning         SubagentStatus = iota // Claude session is running
	SubagentWaitingForInput                       // Blocked on AskUserQuestion; waiting for executive answer
	SubagentCompleted                             // Finished successfully
	SubagentFailed                                // Exited with error
	SubagentStopped                               // Cancelled via stop_subagent
)

func (s SubagentStatus) String() string {
	switch s {
	case SubagentRunning:
		return "running"
	case SubagentWaitingForInput:
		return "waiting_for_input"
	case SubagentCompleted:
		return "completed"
	case SubagentFailed:
		return "failed"
	case SubagentStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// StagedMemory is a thought captured from a subagent's save_thought call,
// held pending executive approval before being written to Engram.
type StagedMemory struct {
	Content  string    `json:"content"`
	StagedAt time.Time `json:"staged_at"`
}

// SubagentSession manages a Claude SDK session for a specific task.
// The subagent runs autonomously with a restricted tool set and surfaces
// questions to the executive via the native AskUserQuestion tool.
type SubagentSession struct {
	// Identifiers
	// ID is the session identifier — always the Claude-assigned session ID once
	// the first StreamEvent is received (typically within milliseconds of spawn).
	// Until then it holds a temporary UUID used during the brief startup window.
	ID        string    // Session ID (temp UUID → Claude session ID on first StreamEvent)
	Task      string    // Short task description
	SpawnedAt time.Time // When the session was created

	// Workflow fields (optional) — set when spawned as part of a multi-step workflow.
	WorkflowInstanceID string // e.g. "wf_1711062766"
	WorkflowStep       string // e.g. "strategy"

	// State (protected by mu)
	mu              sync.Mutex
	status          SubagentStatus
	pendingQuestion string // Set when SubagentWaitingForInput
	result          string // Final output when Completed
	lastErr         error  // Error when Failed

	// answerReady receives the answer from the executive.
	// Buffered(1) so Answer() never blocks.
	answerReady chan string

	// claudeIDReady receives the Claude session ID from the first StreamEvent.
	// Buffered(1); Spawn() blocks on it (with timeout) to re-key the session.
	claudeIDReady chan string

	// cancel cancels the session's task context, terminating the Claude subprocess.
	cancel context.CancelFunc

	// stopped is set true by Stop() before cancelling, so runSession can
	// distinguish an explicit stop from an unexpected context cancellation.
	stopped bool

	// LogPath is the file path where this session's log is written.
	// Set once the log file is opened in runSession; empty if no log file.
	LogPath string

	// events is a capped log of recent subagent activity (tool calls, text).
	events      []SubagentEvent
	lastEventAt time.Time

	// stagedMemories holds save_thought calls intercepted during the session.
	// The executive reviews and approves these before they are flushed to Engram.
	stagedMemories []StagedMemory
}

// appendEvent records an event in the session's activity log (capped at maxSubagentEvents).
func (s *SubagentSession) appendEvent(e SubagentEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastEventAt = e.At
	s.events = append(s.events, e)
	if len(s.events) > maxSubagentEvents {
		s.events = s.events[len(s.events)-maxSubagentEvents:]
	}
}

// Events returns the last n events. If n <= 0 or n >= total, returns all events.
func (s *SubagentSession) Events(n int) []SubagentEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 || n >= len(s.events) {
		out := make([]SubagentEvent, len(s.events))
		copy(out, s.events)
		return out
	}
	out := make([]SubagentEvent, n)
	copy(out, s.events[len(s.events)-n:])
	return out
}

// LastEventAt returns the timestamp of the most recent recorded event.
func (s *SubagentSession) LastEventAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastEventAt
}

// AddStagedMemory records a thought intercepted from a save_thought call.
func (s *SubagentSession) AddStagedMemory(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stagedMemories = append(s.stagedMemories, StagedMemory{
		Content:  content,
		StagedAt: time.Now(),
	})
}

// StagedMemories returns a copy of all staged memories without clearing them.
func (s *SubagentSession) StagedMemories() []StagedMemory {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]StagedMemory, len(s.stagedMemories))
	copy(out, s.stagedMemories)
	return out
}

// DrainStagedMemories returns all staged memories and clears the list.
func (s *SubagentSession) DrainStagedMemories() []StagedMemory {
	s.mu.Lock()
	defer s.mu.Unlock()
	memories := s.stagedMemories
	s.stagedMemories = nil
	return memories
}

// Status returns the current lifecycle status.
func (s *SubagentSession) Status() SubagentStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// PendingQuestion returns the question waiting for an answer, or "" if none.
func (s *SubagentSession) PendingQuestion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingQuestion
}

// Result returns the final output (only meaningful when Completed).
func (s *SubagentSession) Result() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

// Err returns the error (only meaningful when Failed).
func (s *SubagentSession) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// SubagentManager maintains a registry of active subagent sessions and
// provides spawn/answer/status operations for the executive.
type SubagentManager struct {
	mu       sync.RWMutex
	sessions map[string]*SubagentSession // keyed by session ID (Claude ID once known)

	// Notify executive when a subagent has a pending question
	QuestionNotify chan *SubagentSession

	// Notify executive when a subagent completes or fails
	DoneNotify chan *SubagentSession
}

// NewSubagentManager creates a new manager and starts a background goroutine
// that removes completed/failed/stopped sessions older than 1 hour every 10 minutes.
func NewSubagentManager(stateDir string) *SubagentManager {
	m := &SubagentManager{
		sessions:       make(map[string]*SubagentSession),
		QuestionNotify: make(chan *SubagentSession, 16),
		DoneNotify:     make(chan *SubagentSession, 16),
	}
	go m.cleanupLoop()
	return m
}

// cleanupLoop periodically removes finished sessions older than 1 hour.
func (m *SubagentManager) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		if n := m.Cleanup(time.Hour); n > 0 {
			log.Printf("[subagent-manager] Cleaned up %d finished session(s)", n)
		}
	}
}

// SubagentConfig controls how a subagent session is spawned.
type SubagentConfig struct {
	// Task is what the subagent should do.
	Task string

	// SystemPromptAppend is extra content appended to the subagent's system prompt.
	SystemPromptAppend string

	// Model overrides the default model (empty = use default).
	Model string

	// WorkDir overrides the working directory for the Claude subprocess.
	WorkDir string

	// AllowedTools restricts which built-in tools the subagent can use.
	// Empty = all tools. Example: "Bash,Read,Glob,Grep,Write"
	AllowedTools string

	// MCPServerURL is the HTTP URL for the bud2 MCP server.
	// Required for the subagent to call signal_done and other bud2 tools.
	MCPServerURL string

	// AgentDefs registers programmatic agent definitions so the built-in Agent tool
	// can resolve "namespace:name" style subagent references without file management.
	AgentDefs map[string]claudecode.AgentDefinition

	// Workflow fields — optional, set when this subagent is part of a multi-step workflow.
	WorkflowInstanceID string // e.g. "wf_1711062766"
	WorkflowStep       string // e.g. "strategy"
}

// Spawn creates a new SubagentSession and starts it in a background goroutine.
// It blocks briefly (up to 10s) waiting for the first StreamEvent from Claude so
// that the returned session's ID is the Claude-assigned session ID rather than a
// temporary UUID.
func (m *SubagentManager) Spawn(ctx context.Context, cfg SubagentConfig) (*SubagentSession, error) {
	tempID := generateSessionUUID()

	taskCtx, taskCancel := context.WithCancel(ctx)

	session := &SubagentSession{
		ID:                 tempID,
		Task:               cfg.Task,
		SpawnedAt:          time.Now(),
		status:             SubagentRunning,
		answerReady:        make(chan string, 1),
		claudeIDReady:      make(chan string, 1),
		cancel:             taskCancel,
		WorkflowInstanceID: cfg.WorkflowInstanceID,
		WorkflowStep:       cfg.WorkflowStep,
	}

	m.mu.Lock()
	m.sessions[tempID] = session
	m.mu.Unlock()

	go func() {
		defer taskCancel()
		m.runSession(taskCtx, session, cfg)
	}()

	// Wait for Claude session ID from first StreamEvent so we can use it as the
	// primary key. Fall back to temp UUID if it doesn't arrive in time.
	select {
	case claudeID := <-session.claudeIDReady:
		m.mu.Lock()
		delete(m.sessions, tempID)
		session.ID = claudeID
		m.sessions[claudeID] = session
		m.mu.Unlock()
		log.Printf("[subagent-manager] Spawned session %s: %s", claudeID, truncate(cfg.Task, 60))
	case <-time.After(10 * time.Second):
		log.Printf("[subagent-manager] Spawned session %s (Claude ID not yet available): %s", tempID, truncate(cfg.Task, 60))
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return session, nil
}

// Answer provides a reply to a subagent's pending question.
func (m *SubagentManager) Answer(sessionID, answer string) error {
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("subagent session not found: %s", sessionID)
	}

	session.mu.Lock()
	if session.status != SubagentWaitingForInput {
		session.mu.Unlock()
		return fmt.Errorf("subagent %s is not waiting for input (status: %s)", sessionID, session.status)
	}
	session.status = SubagentRunning
	session.pendingQuestion = ""
	session.mu.Unlock()

	select {
	case session.answerReady <- answer:
	default:
		return fmt.Errorf("answer channel full for session %s", sessionID)
	}

	log.Printf("[subagent-manager] Answer provided to session %s", sessionID)
	return nil
}

// List returns a snapshot of all active sessions.
func (m *SubagentManager) List() []*SubagentSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sessions := make([]*SubagentSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

// Get returns a session by its ID (Claude session ID once known, temp UUID during startup).
func (m *SubagentManager) Get(id string) *SubagentSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// Stop cancels a running subagent session by its ID. Returns an error if the
// session is not found or is already finished.
func (m *SubagentManager) Stop(sessionID string) error {
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("subagent session not found: %s", sessionID)
	}
	session.mu.Lock()
	done := session.status == SubagentCompleted || session.status == SubagentFailed || session.status == SubagentStopped
	session.mu.Unlock()
	if done {
		return fmt.Errorf("subagent %s is already finished (status: %s)", sessionID, session.status)
	}
	log.Printf("[subagent-manager] Stopping session %s on request", sessionID)
	session.mu.Lock()
	session.stopped = true
	session.mu.Unlock()
	session.cancel()
	return nil
}

// Cleanup removes completed/failed sessions older than the given duration.
func (m *SubagentManager) Cleanup(olderThan time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-olderThan)
	var removed int
	for id, s := range m.sessions {
		s.mu.Lock()
		done := s.status == SubagentCompleted || s.status == SubagentFailed || s.status == SubagentStopped
		old := s.SpawnedAt.Before(cutoff)
		s.mu.Unlock()
		if done && old {
			delete(m.sessions, id)
			removed++
		}
	}
	return removed
}

// runSession runs the subagent Claude session to completion using the SDK.
// Uses CanUseTool to intercept AskUserQuestion and block until the executive
// provides an answer — no subprocess restart needed.
func (m *SubagentManager) runSession(ctx context.Context, session *SubagentSession, cfg SubagentConfig) {
	questionCallback := claudecode.WithCanUseTool(func(
		ctx context.Context,
		toolName string,
		input map[string]any,
		permCtx claudecode.ToolPermissionContext,
	) (claudecode.PermissionResult, error) {
		if toolName == "mcp__bud2__save_thought" {
			content, _ := input["content"].(string)
			if content != "" {
				session.AddStagedMemory(content)
				log.Printf("[subagent-%s] staged memory: %s", session.ID[:8], truncate(content, 60))
			}
			return claudecode.NewPermissionResultDeny("Thought staged for executive review. It will be saved to memory after the executive approves it."), nil
		}

		if toolName != "AskUserQuestion" {
			log.Printf("[subagent-%s] tool: %s", session.ID[:8], toolName)
			return claudecode.NewPermissionResultAllow(), nil
		}

		question := extractAskUserQuestionText(input)

		session.mu.Lock()
		session.status = SubagentWaitingForInput
		session.pendingQuestion = question
		session.mu.Unlock()

		log.Printf("[subagent] Session %s has question: %s", session.ID, truncate(question, 80))

		select {
		case m.QuestionNotify <- session:
		case <-ctx.Done():
			return claudecode.NewPermissionResultDeny("cancelled"), ctx.Err()
		}

		select {
		case answer := <-session.answerReady:
			return claudecode.NewPermissionResultDeny(answer), nil
		case <-ctx.Done():
			return claudecode.NewPermissionResultDeny("cancelled"), ctx.Err()
		}
	})

	opts := []claudecode.Option{
		claudecode.WithAppendSystemPrompt(buildSubagentSystemPrompt(cfg)),
		claudecode.WithPermissionMode(claudecode.PermissionModeAcceptEdits),
		claudecode.WithPartialStreaming(),
		claudecode.WithStderrCallback(func(line string) {
			log.Printf("[subagent-%s stderr] %s", session.ID[:8], line)
		}),
		questionCallback,
	}
	if cfg.MCPServerURL != "" {
		opts = append(opts, claudecode.WithMcpServers(map[string]claudecode.McpServerConfig{
			"bud2": &claudecode.McpHTTPServerConfig{
				Type: claudecode.McpServerTypeHTTP,
				URL:  cfg.MCPServerURL,
			},
		}))
	}
	if cfg.Model != "" {
		opts = append(opts, claudecode.WithModel(cfg.Model))
	}
	if cfg.WorkDir != "" {
		opts = append(opts, claudecode.WithCwd(cfg.WorkDir))
	}
	if cfg.AllowedTools != "" {
		tools := strings.Split(cfg.AllowedTools, ",")
		opts = append(opts, claudecode.WithAllowedTools(tools...))
	}
	if len(cfg.AgentDefs) > 0 {
		opts = append(opts, claudecode.WithAgents(cfg.AgentDefs))
	}

	prompt := fmt.Sprintf("## Task\n%s\n\nBegin work on this task. When you are done, finish your response.", cfg.Task)

	// Open a session log file (same format as executive session logs).
	var logFile *os.File
	if cfg.WorkDir != "" {
		logDir := filepath.Join(cfg.WorkDir, "logs", "agents")
		if err := os.MkdirAll(logDir, 0755); err == nil {
			logPath := filepath.Join(logDir, "subagent-"+session.ID[:8]+".log")
			if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
				logFile = f
				session.LogPath = logPath
				defer logFile.Close()
			} else {
				log.Printf("[subagent-%s] cannot open log file: %v", session.ID[:8], err)
			}
		}
	}

	log.Printf("[subagent-%s] Starting session (acceptEdits mode)", session.ID[:8])
	writeLog(logFile, "=== PROMPT (%d chars) ===", len(prompt))
	if logFile != nil {
		fmt.Fprintf(logFile, "%s\n=== END PROMPT ===\n\n", prompt)
	}
	var result strings.Builder
	err := claudecode.WithClient(ctx, func(client claudecode.Client) error {
		if err := client.Query(ctx, prompt); err != nil {
			log.Printf("[subagent-%s] Query error: %v", session.ID[:8], err)
			return err
		}
		cb := receiveLoopCallbacks{
			LogPrefix: fmt.Sprintf("subagent-%s", session.ID[:8]),
			OnStreamEvent: func(sessionID string) {
				// Capture Claude session ID from the first StreamEvent and
				// signal Spawn() so it can re-key the session.
				// The channel is buffered(1); subsequent StreamEvents hit default.
				select {
				case session.claudeIDReady <- sessionID:
				default:
				}
			},
			OnThinking: func(text string) {
				log.Printf("[subagent-%s] thinking: %s", session.ID[:8], truncate(text, 300))
				session.appendEvent(SubagentEvent{
					Kind:    SubagentEventThinking,
					At:      time.Now(),
					Summary: truncate(text, 120),
				})
			},
			OnText: func(text string) {
				result.WriteString(text)
				log.Printf("[subagent-%s] text: %s", session.ID[:8], truncate(text, 300))
				session.appendEvent(SubagentEvent{
					Kind:    SubagentEventText,
					At:      time.Now(),
					Summary: truncate(text, 120),
				})
			},
			OnTool: func(name string, input map[string]any) {
				summary := summarizeToolInput(name, input)
				log.Printf("[subagent-%s] calling tool: %s %s", session.ID[:8], name, summary)
				session.appendEvent(SubagentEvent{
					Kind:     SubagentEventToolCall,
					At:       time.Now(),
					ToolName: name,
					Summary:  summary,
				})
			},
			OnResult: func(m *claudecode.ResultMessage) {
				log.Printf("[subagent-%s] Claude session complete (turns=%d duration=%dms)",
					session.ID[:8], m.NumTurns, m.DurationMs)
			},
		}
		receiveLoop(ctx, client, logFile, cb) //nolint:errcheck
		return nil
	}, opts...)

	session.mu.Lock()
	session.result = result.String()
	if session.stopped {
		session.status = SubagentStopped
		log.Printf("[subagent] Session %s stopped", session.ID)
	} else if err != nil {
		session.status = SubagentFailed
		session.lastErr = err
		log.Printf("[subagent] Session %s failed: %v", session.ID, err)
	} else {
		session.status = SubagentCompleted
		log.Printf("[subagent] Session %s completed", session.ID)
	}
	session.mu.Unlock()

	select {
	case m.DoneNotify <- session:
	default:
	}
}

// extractAskUserQuestionText extracts the question from AskUserQuestion tool input.
// Input schema: {"questions": [{"question": "..."}]}
func extractAskUserQuestionText(input map[string]any) string {
	questions, ok := input["questions"]
	if !ok {
		return ""
	}
	list, ok := questions.([]any)
	if !ok || len(list) == 0 {
		return ""
	}
	first, ok := list[0].(map[string]any)
	if !ok {
		return ""
	}
	q, _ := first["question"].(string)
	return q
}

// buildSubagentSystemPrompt constructs the system prompt for a subagent session.
func buildSubagentSystemPrompt(cfg SubagentConfig) string {
	var sb strings.Builder
	sb.WriteString(`You are a focused task assistant working autonomously on behalf of Bud.

CONSTRAINTS:
- Do NOT use talk_to_user or discord_react — you cannot communicate directly with the user.
- If you need information from the user, call AskUserQuestion with your question.
  You will receive the answer inline and can continue your work.
- When your task is complete, finish your response with a clear summary of what you did.
- Keep reasoning internal. Output decisions and outcomes, not your full thought process.

MEMORY:
- Use save_thought to record observations worth persisting (e.g., discoveries, decisions, patterns).
- Thoughts are staged for executive review rather than written directly to memory.
  The executive will approve and flush them after your task completes.
- Be selective: only save things that are genuinely useful to recall later.
`)

	if cfg.SystemPromptAppend != "" {
		sb.WriteString("\n")
		sb.WriteString(cfg.SystemPromptAppend)
	}

	return sb.String()
}
