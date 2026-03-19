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
	"strings"
	"sync"
	"time"

	claudecode "github.com/severity1/claude-agent-sdk-go"
)

// SubagentStatus describes the current lifecycle state of a subagent session.
type SubagentStatus int

const (
	SubagentRunning         SubagentStatus = iota // Claude session is running
	SubagentWaitingForInput                       // Blocked on AskUserQuestion; waiting for executive answer
	SubagentCompleted                             // Finished successfully
	SubagentFailed                                // Exited with error
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
	default:
		return "unknown"
	}
}

// SubagentSession manages a Claude SDK session for a specific task.
// The subagent runs autonomously with a restricted tool set and surfaces
// questions to the executive via the native AskUserQuestion tool.
type SubagentSession struct {
	// Identifiers
	ID            string    // Bud-internal UUID (used for spawning/logging prefix)
	ClaudeID      string    // Claude-assigned session ID (from ResultMessage; available after completion)
	Task          string    // Short task description
	SpawnedAt     time.Time // When the session was created

	// State (protected by mu)
	mu              sync.Mutex
	status          SubagentStatus
	pendingQuestion string // Set when SubagentWaitingForInput
	result          string // Final output when Completed
	lastErr         error  // Error when Failed

	// answerReady receives the answer from the executive.
	// Buffered(1) so Answer() never blocks.
	answerReady chan string
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
	mu           sync.RWMutex
	sessions     map[string]*SubagentSession // keyed by Bud internal UUID
	claudeIDIdx  map[string]*SubagentSession // keyed by Claude session ID (populated after completion)

	// Notify executive when a subagent has a pending question
	QuestionNotify chan *SubagentSession

	// Notify executive when a subagent completes or fails
	DoneNotify chan *SubagentSession
}

// NewSubagentManager creates a new manager.
func NewSubagentManager(stateDir string) *SubagentManager {
	return &SubagentManager{
		sessions:       make(map[string]*SubagentSession),
		claudeIDIdx:    make(map[string]*SubagentSession),
		QuestionNotify: make(chan *SubagentSession, 16),
		DoneNotify:     make(chan *SubagentSession, 16),
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
}

// Spawn creates a new SubagentSession and starts it in a background goroutine.
func (m *SubagentManager) Spawn(ctx context.Context, cfg SubagentConfig) (*SubagentSession, error) {
	id := generateSessionUUID()

	session := &SubagentSession{
		ID:          id,
		Task:        cfg.Task,
		SpawnedAt:   time.Now(),
		status:      SubagentRunning,
		answerReady: make(chan string, 1),
	}

	m.mu.Lock()
	m.sessions[id] = session
	m.mu.Unlock()

	taskCtx, taskCancel := context.WithTimeout(ctx, 3*time.Minute)
	go func() {
		defer taskCancel()
		m.runSession(taskCtx, session, cfg)
	}()

	log.Printf("[subagent-manager] Spawned session %s: %s", id, truncate(cfg.Task, 60))
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

// Get returns a session by Bud internal UUID or Claude session ID.
func (m *SubagentManager) Get(id string) *SubagentSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.sessions[id]; ok {
		return s
	}
	return m.claudeIDIdx[id]
}

// Cleanup removes completed/failed sessions older than the given duration.
func (m *SubagentManager) Cleanup(olderThan time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-olderThan)
	var removed int
	for id, s := range m.sessions {
		s.mu.Lock()
		done := s.status == SubagentCompleted || s.status == SubagentFailed
		old := s.SpawnedAt.Before(cutoff)
		claudeID := s.ClaudeID
		s.mu.Unlock()
		if done && old {
			delete(m.sessions, id)
			if claudeID != "" {
				delete(m.claudeIDIdx, claudeID)
			}
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

	prompt := fmt.Sprintf("## Task\n%s\n\nBegin work on this task. When you are done, finish your response.", cfg.Task)

	log.Printf("[subagent-%s] Starting session (acceptEdits mode)", session.ID[:8])
	var result strings.Builder
	err := claudecode.WithClient(ctx, func(client claudecode.Client) error {
		log.Printf("[subagent-%s] Client connected, sending query", session.ID[:8])
		if err := client.Query(ctx, prompt); err != nil {
			log.Printf("[subagent-%s] Query error: %v", session.ID[:8], err)
			return err
		}
		msgCount := 0
		for msg := range client.ReceiveMessages(ctx) {
			msgCount++
			switch m := msg.(type) {
			case *claudecode.AssistantMessage:
				for _, b := range m.Content {
					switch block := b.(type) {
					case *claudecode.TextBlock:
						result.WriteString(block.Text)
						log.Printf("[subagent-%s] text output (%d chars)", session.ID[:8], len(block.Text))
					case *claudecode.ToolUseBlock:
						log.Printf("[subagent-%s] calling tool: %s", session.ID[:8], block.Name)
					}
				}
			case *claudecode.ResultMessage:
				session.mu.Lock()
				session.ClaudeID = m.SessionID
				session.mu.Unlock()
				log.Printf("[subagent-%s] Claude session ID: %s (turns=%d duration=%dms)",
					session.ID[:8], m.SessionID, m.NumTurns, m.DurationMs)
			}
		}
		log.Printf("[subagent-%s] ReceiveMessages loop exited (received %d messages)", session.ID[:8], msgCount)
		return nil
	}, opts...)

	// Register Claude session ID alias now that WithClient has returned
	session.mu.Lock()
	claudeID := session.ClaudeID
	session.mu.Unlock()
	if claudeID != "" {
		m.mu.Lock()
		m.claudeIDIdx[claudeID] = session
		m.mu.Unlock()
	}

	session.mu.Lock()
	session.result = result.String()
	if err != nil {
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
`)

	if cfg.SystemPromptAppend != "" {
		sb.WriteString("\n")
		sb.WriteString(cfg.SystemPromptAppend)
	}

	return sb.String()
}
