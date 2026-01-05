package executive

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/memory"
	"github.com/vthunder/bud2/internal/types"
)

// Executive manages Claude sessions for threads
type Executive struct {
	tmux     *Tmux
	sessions map[string]*ClaudeSession
	mu       sync.RWMutex

	// Dependencies
	percepts *memory.PerceptPool
	threads  *memory.ThreadPool
	outbox   *memory.Outbox

	// Config
	config ExecutiveConfig
}

// ExecutiveConfig holds executive configuration
type ExecutiveConfig struct {
	Model      string // Claude model to use
	WorkDir    string // Working directory
	UseInteractive bool // Use tmux interactive mode (for debugging)
}

// New creates a new Executive
func New(percepts *memory.PerceptPool, threads *memory.ThreadPool, outbox *memory.Outbox, cfg ExecutiveConfig) *Executive {
	return &Executive{
		tmux:     NewTmux(),
		sessions: make(map[string]*ClaudeSession),
		percepts: percepts,
		threads:  threads,
		outbox:   outbox,
		config:   cfg,
	}
}

// Start initializes the executive (creates tmux session)
func (e *Executive) Start() error {
	if err := e.tmux.EnsureSession(); err != nil {
		return fmt.Errorf("failed to create tmux session: %w", err)
	}
	log.Println("[executive] Started, tmux session ready")
	return nil
}

// ProcessThread processes an active thread
func (e *Executive) ProcessThread(ctx context.Context, thread *types.Thread) error {
	session := e.getOrCreateSession(thread.ID)

	// Build prompt from thread context
	prompt := e.buildPrompt(thread)

	// Set up tool handling
	session.OnToolCall(func(name string, args map[string]any) (string, error) {
		return e.handleToolCall(thread, name, args)
	})

	// Collect output
	var output strings.Builder
	session.OnOutput(func(text string) {
		output.WriteString(text)
	})

	// Send to Claude
	claudeCfg := ClaudeConfig{
		Model:   e.config.Model,
		WorkDir: e.config.WorkDir,
	}

	if e.config.UseInteractive {
		// Interactive mode - shows in tmux
		if err := session.SendPromptInteractive(prompt, claudeCfg); err != nil {
			return fmt.Errorf("interactive prompt failed: %w", err)
		}
		// In interactive mode, we don't wait for completion
		// User can monitor in tmux
		log.Printf("[executive] Sent prompt to thread %s (interactive mode)", thread.ID)
		return nil
	}

	// Programmatic mode
	if err := session.SendPrompt(ctx, prompt, claudeCfg); err != nil {
		return fmt.Errorf("prompt failed: %w", err)
	}

	// Log output
	if output.Len() > 0 {
		log.Printf("[executive] Thread %s output: %s", thread.ID, truncate(output.String(), 200))
	}

	return nil
}

// buildPrompt constructs the prompt for Claude
func (e *Executive) buildPrompt(thread *types.Thread) string {
	var prompt strings.Builder

	// Thread goal
	prompt.WriteString(fmt.Sprintf("## Current Task\n%s\n\n", thread.Goal))

	// Get referenced percepts
	percepts := e.percepts.GetMany(thread.PerceptRefs)
	if len(percepts) > 0 {
		prompt.WriteString("## Context\n")
		for _, p := range percepts {
			prompt.WriteString(fmt.Sprintf("- [%s] %s: ", p.Source, p.Type))
			if content, ok := p.Data["content"].(string); ok {
				prompt.WriteString(content)
			}
			prompt.WriteString("\n")
		}
		prompt.WriteString("\n")
	}

	// Thread state
	if thread.State.Phase != "" {
		prompt.WriteString(fmt.Sprintf("## Current Phase\n%s\n\n", thread.State.Phase))
	}
	if thread.State.NextStep != "" {
		prompt.WriteString(fmt.Sprintf("## Next Step\n%s\n\n", thread.State.NextStep))
	}

	// Instructions
	prompt.WriteString("## Instructions\n")
	prompt.WriteString("Respond to the task above. Use available tools as needed.\n")
	prompt.WriteString("When done, indicate completion clearly.\n")

	return prompt.String()
}

// handleToolCall processes a tool call from Claude
func (e *Executive) handleToolCall(thread *types.Thread, name string, args map[string]any) (string, error) {
	log.Printf("[executive] Tool call from thread %s: %s(%v)", thread.ID, name, args)

	switch name {
	case "respond_to_user", "send_message":
		return e.toolRespondToUser(thread, args)

	case "complete_thread":
		return e.toolCompleteThread(thread, args)

	case "update_thread_state":
		return e.toolUpdateThreadState(thread, args)

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// toolRespondToUser queues a message to send
func (e *Executive) toolRespondToUser(thread *types.Thread, args map[string]any) (string, error) {
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}

	// Get channel from percept data
	channelID := ""
	percepts := e.percepts.GetMany(thread.PerceptRefs)
	for _, p := range percepts {
		if p.Source == "discord" {
			if ch, ok := p.Data["channel_id"].(string); ok {
				channelID = ch
				break
			}
		}
	}

	if channelID == "" {
		return "", fmt.Errorf("no channel_id found in percepts")
	}

	// Queue the action
	action := &types.Action{
		ID:       fmt.Sprintf("action-%d", time.Now().UnixNano()),
		Effector: "discord",
		Type:     "send_message",
		Payload: map[string]any{
			"channel_id": channelID,
			"content":    content,
		},
	}

	e.outbox.Add(action)
	log.Printf("[executive] Queued response: %s", truncate(content, 100))

	return "Message queued for sending", nil
}

// toolCompleteThread marks the thread as complete
func (e *Executive) toolCompleteThread(thread *types.Thread, args map[string]any) (string, error) {
	thread.Status = types.StatusComplete
	log.Printf("[executive] Thread %s marked complete", thread.ID)
	return "Thread marked as complete", nil
}

// toolUpdateThreadState updates the thread state
func (e *Executive) toolUpdateThreadState(thread *types.Thread, args map[string]any) (string, error) {
	if phase, ok := args["phase"].(string); ok {
		thread.State.Phase = phase
	}
	if nextStep, ok := args["next_step"].(string); ok {
		thread.State.NextStep = nextStep
	}
	log.Printf("[executive] Thread %s state updated: phase=%s, next=%s",
		thread.ID, thread.State.Phase, thread.State.NextStep)
	return "Thread state updated", nil
}

// getOrCreateSession gets or creates a Claude session for a thread
func (e *Executive) getOrCreateSession(threadID string) *ClaudeSession {
	e.mu.Lock()
	defer e.mu.Unlock()

	if session, ok := e.sessions[threadID]; ok {
		return session
	}

	session := NewClaudeSession(threadID, e.tmux)
	e.sessions[threadID] = session
	return session
}

// CloseThread closes the session for a thread
func (e *Executive) CloseThread(threadID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if session, ok := e.sessions[threadID]; ok {
		if err := session.Close(); err != nil {
			return err
		}
		delete(e.sessions, threadID)
	}
	return nil
}

// ListSessions returns all active session thread IDs
func (e *Executive) ListSessions() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	ids := make([]string, 0, len(e.sessions))
	for id := range e.sessions {
		ids = append(ids, id)
	}
	return ids
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
