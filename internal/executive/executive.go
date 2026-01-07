package executive

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/budget"
	"github.com/vthunder/bud2/internal/memory"
	"github.com/vthunder/bud2/internal/types"
)

// Executive manages Claude sessions for threads
type Executive struct {
	tmux           *Tmux
	sessionManager *SessionManager

	// Dependencies
	percepts *memory.PerceptPool
	threads  *memory.ThreadPool
	outbox   *memory.Outbox

	// Config
	config ExecutiveConfig
}

// ExecutiveConfig holds executive configuration
type ExecutiveConfig struct {
	Model           string                                              // Claude model to use
	WorkDir         string                                              // Working directory
	UseInteractive  bool                                                // Use tmux interactive mode (for debugging)
	GetActiveTraces func(limit int, excludeSources []string) []*types.Trace // function to get activated memory traces
	GetCoreTraces   func() []*types.Trace                               // function to get core identity traces
	SessionTracker  *budget.SessionTracker                              // tracks thinking time
	StartTyping     func(channelID string)                              // start typing indicator
	StopTyping      func(channelID string)                              // stop typing indicator
}

// New creates a new Executive
func New(percepts *memory.PerceptPool, threads *memory.ThreadPool, outbox *memory.Outbox, cfg ExecutiveConfig) *Executive {
	tmux := NewTmux()
	return &Executive{
		tmux:           tmux,
		sessionManager: NewSessionManager(threads, tmux),
		percepts:       percepts,
		threads:        threads,
		outbox:         outbox,
		config:         cfg,
	}
}

// SetTypingCallbacks sets the typing indicator callbacks (called after Discord effector is initialized)
func (e *Executive) SetTypingCallbacks(start, stop func(channelID string)) {
	e.config.StartTyping = start
	e.config.StopTyping = stop
}

// Start initializes the executive (creates tmux session)
func (e *Executive) Start() error {
	if err := e.tmux.EnsureSession(); err != nil {
		return fmt.Errorf("failed to create tmux session: %w", err)
	}
	log.Println("[executive] Started, tmux session ready")
	return nil
}

// getChannelID extracts the Discord channel ID from thread percepts
func (e *Executive) getChannelID(thread *types.Thread) string {
	percepts := e.percepts.GetMany(thread.PerceptRefs)
	for _, p := range percepts {
		if p.Source == "discord" || p.Source == "inbox" {
			if ch, ok := p.Data["channel_id"].(string); ok && ch != "" {
				return ch
			}
		}
	}
	return ""
}

// ProcessThread processes an active thread
func (e *Executive) ProcessThread(ctx context.Context, thread *types.Thread) error {
	// Check if already processed (prevent re-processing on restart)
	if thread.ProcessedAt != nil {
		// Check if any percepts are newer than ProcessedAt
		hasNewPercepts := false
		percepts := e.percepts.GetMany(thread.PerceptRefs)
		for _, p := range percepts {
			if p.Timestamp.After(*thread.ProcessedAt) {
				hasNewPercepts = true
				break
			}
		}
		if !hasNewPercepts {
			log.Printf("[executive] Thread %s already processed at %s, skipping",
				thread.ID, thread.ProcessedAt.Format(time.RFC3339))
			// Pause this thread and reset salience so attention can select a different one
			thread.Status = types.StatusPaused
			thread.Salience = 0
			thread.Activation = 0.1 // low but not zero, can be boosted by new percepts
			return nil
		}
		log.Printf("[executive] Thread %s has new percepts since last processing", thread.ID)
	}

	// Focus this thread (handles session limits, freezing old sessions if needed)
	session, err := e.sessionManager.Focus(thread)
	if err != nil {
		return fmt.Errorf("failed to focus thread: %w", err)
	}

	// Check if this is the first message in this session
	isFirstMessage := session.IsFirstMessage()

	// Build prompt from thread context
	prompt, perceptIDs := e.buildPrompt(thread, session, isFirstMessage)

	// Skip if prompt is empty (e.g., only filtered percepts)
	if strings.TrimSpace(prompt) == "" {
		log.Printf("[executive] Thread %s has no new content, skipping", thread.ID)
		return nil
	}

	// Get channel ID for typing indicator (only after we know we'll send to Claude)
	channelID := e.getChannelID(thread)

	// Start typing indicator if we have a channel and callback
	if channelID != "" && e.config.StartTyping != nil {
		e.config.StartTyping(channelID)
		defer func() {
			if e.config.StopTyping != nil {
				e.config.StopTyping(channelID)
			}
		}()
	}

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
		// Track session start for thinking time budget
		if e.config.SessionTracker != nil {
			e.config.SessionTracker.StartSession(session.sessionID, thread.ID)
		}

		// Interactive mode - shows in tmux
		if err := session.SendPromptInteractive(prompt, claudeCfg); err != nil {
			return fmt.Errorf("interactive prompt failed: %w", err)
		}
		// Mark first message sent (so subsequent prompts skip boilerplate)
		session.MarkFirstMessageSent()
		// Mark percepts as seen (so they're not repeated)
		session.MarkPerceptsSeen(perceptIDs)
		// Mark as processed (prevents re-processing on restart)
		now := time.Now()
		thread.ProcessedAt = &now
		// In interactive mode, we don't wait for completion
		// Claude will call signal_done when finished
		log.Printf("[executive] Sent prompt to thread %s (interactive mode, session %s)", thread.ID, session.sessionID)
		return nil
	}

	// Programmatic mode - track session
	if e.config.SessionTracker != nil {
		e.config.SessionTracker.StartSession(session.sessionID, thread.ID)
	}

	if err := session.SendPrompt(ctx, prompt, claudeCfg); err != nil {
		return fmt.Errorf("prompt failed: %w", err)
	}

	// Programmatic mode completes synchronously, so mark done
	if e.config.SessionTracker != nil {
		e.config.SessionTracker.CompleteSession(session.sessionID)
	}

	// Mark first message sent
	session.MarkFirstMessageSent()
	// Mark percepts as seen
	session.MarkPerceptsSeen(perceptIDs)

	// Mark as processed
	now := time.Now()
	thread.ProcessedAt = &now

	// Log output
	if output.Len() > 0 {
		log.Printf("[executive] Thread %s output: %s", thread.ID, truncate(output.String(), 200))
	}

	return nil
}

// buildPrompt constructs the prompt for Claude
// Returns the prompt string and IDs of percepts that were included
func (e *Executive) buildPrompt(thread *types.Thread, session *ClaudeSession, isFirstMessage bool) (string, []string) {
	var prompt strings.Builder
	var includedPerceptIDs []string

	// Include core identity traces on first message only (defines who Bud is)
	if isFirstMessage && e.config.GetCoreTraces != nil {
		coreTraces := e.config.GetCoreTraces()
		if len(coreTraces) > 0 {
			prompt.WriteString("## Identity\n")
			for _, t := range coreTraces {
				prompt.WriteString(fmt.Sprintf("- %s\n", t.Content))
			}
			prompt.WriteString("\n")
		}
	}

	// Include activated memory traces (long-term memory)
	// Exclude: core traces (already in Identity), traces from recent percepts (in Recent Context)
	if e.config.GetActiveTraces != nil {
		// Get IDs of recent percepts to exclude (avoid duplication with Recent Context)
		var recentPerceptIDs []string
		allPercepts := e.percepts.GetMany(thread.PerceptRefs)
		now := time.Now()
		for _, p := range allPercepts {
			if now.Sub(p.Timestamp) < 60*time.Second {
				recentPerceptIDs = append(recentPerceptIDs, p.ID)
			}
		}
		traces := e.config.GetActiveTraces(10, recentPerceptIDs) // top 10, excluding only recent
		// Filter out core traces (they're in Identity section)
		var nonCoreTraces []*types.Trace
		for _, t := range traces {
			if !t.IsCore {
				nonCoreTraces = append(nonCoreTraces, t)
			}
		}
		if len(nonCoreTraces) > 0 {
			prompt.WriteString("## Relevant Memories\n")
			for _, t := range nonCoreTraces {
				prompt.WriteString(fmt.Sprintf("- %s\n", t.Content))
			}
			prompt.WriteString("\n")
		}
	}

	// Get referenced percepts - only include recent ones (not yet consolidated)
	// Consolidated percepts live in traces, recent ones in context
	const recentThreshold = 60 * time.Second // match consolidation threshold
	allPercepts := e.percepts.GetMany(thread.PerceptRefs)
	var newPercepts []*types.Percept
	now := time.Now()
	for _, p := range allPercepts {
		// Skip Bud's own outputs (thoughts) - they shouldn't come back as input
		if p.Source == "bud" {
			continue
		}
		// Only include recent percepts
		if now.Sub(p.Timestamp) < recentThreshold {
			// Skip if already sent to Claude in this session
			if !session.HasSeenPercept(p.ID) {
				newPercepts = append(newPercepts, p)
				includedPerceptIDs = append(includedPerceptIDs, p.ID)
			}
		}
	}

	// Format new percepts (these are messages Claude hasn't seen yet)
	if len(newPercepts) > 0 {
		// Use "Context" on first message, "New" on subsequent
		if isFirstMessage {
			prompt.WriteString("## Context\n")
		} else {
			prompt.WriteString("## New\n")
		}
		for _, p := range newPercepts {
			// Format: "author@channel: content" for messages
			if p.Source == "discord" || p.Source == "inbox" {
				author := "user"
				if a, ok := p.Data["author"].(string); ok && a != "" {
					author = a
				}
				channelID := ""
				if ch, ok := p.Data["channel_id"].(string); ok {
					channelID = ch
				}
				content := ""
				if c, ok := p.Data["content"].(string); ok {
					content = c
				}
				prompt.WriteString(fmt.Sprintf("- %s@%s: %s\n", author, channelID, content))
			} else {
				// Generic format for other percept types
				prompt.WriteString(fmt.Sprintf("- [%s] %s: ", p.Source, p.Type))
				if content, ok := p.Data["content"].(string); ok {
					prompt.WriteString(content)
				}
				prompt.WriteString("\n")
			}
		}
		prompt.WriteString("\n")
	}

	return prompt.String(), includedPerceptIDs
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

	// Get channel from percept data (Discord or inbox)
	channelID := ""
	percepts := e.percepts.GetMany(thread.PerceptRefs)
	for _, p := range percepts {
		if p.Source == "discord" || p.Source == "inbox" {
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

// GetSessionManager returns the session manager for external access
func (e *Executive) GetSessionManager() *SessionManager {
	return e.sessionManager
}

// SessionStats returns current session statistics
func (e *Executive) SessionStats() SessionStats {
	return e.sessionManager.Stats()
}

// CloseThread freezes the session for a thread (preserves session ID for resume)
func (e *Executive) CloseThread(threadID string) error {
	thread := e.threads.Get(threadID)
	if thread == nil {
		return nil // thread doesn't exist
	}
	return e.sessionManager.Freeze(thread)
}

// ListSessions returns thread IDs with active sessions (focused or active)
func (e *Executive) ListSessions() []string {
	var ids []string
	for _, t := range e.threads.All() {
		if t.SessionState == types.SessionFocused || t.SessionState == types.SessionActive {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// GetSessionTracker returns the session tracker (for external signal processing)
func (e *Executive) GetSessionTracker() *budget.SessionTracker {
	return e.config.SessionTracker
}

// TodayThinkingMinutes returns total thinking time today
func (e *Executive) TodayThinkingMinutes() float64 {
	if e.config.SessionTracker == nil {
		return 0
	}
	return e.config.SessionTracker.TodayThinkingMinutes()
}

// HasActiveSessions returns true if any Claude sessions are still running
func (e *Executive) HasActiveSessions() bool {
	if e.config.SessionTracker == nil {
		return false
	}
	return e.config.SessionTracker.HasActiveSessions()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
