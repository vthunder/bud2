package executive

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/budget"
	"github.com/vthunder/bud2/internal/buffer"
	"github.com/vthunder/bud2/internal/focus"
	"github.com/vthunder/bud2/internal/graph"
	"github.com/vthunder/bud2/internal/reflex"
)

// ExecutiveV2 is the simplified executive using focus-based attention
// Key simplifications:
// - Single Claude session (not per-thread sessions)
// - Focus-based context assembly (not thread-based)
// - Uses conversation buffer for reply chain context
// - Uses graph layer for memory retrieval
type ExecutiveV2 struct {
	tmux    *Tmux
	session *SimpleSession

	// Focus-based attention
	attention *focus.Attention
	queue     *focus.Queue

	// Memory systems
	graph   *graph.DB
	buffers *buffer.ConversationBuffer

	// Reflex log for context
	reflexLog *reflex.Log

	// Config
	config ExecutiveV2Config
}

// ExecutiveV2Config holds configuration for the v2 executive
type ExecutiveV2Config struct {
	Model          string
	WorkDir        string
	UseInteractive bool

	// BotAuthor is the name used for bot messages in the buffer (e.g., "Bud")
	// Used to filter out bot's own responses on incremental syncs
	// (Claude already knows what it said in the same session)
	BotAuthor string

	// Callbacks
	SessionTracker *budget.SessionTracker
	StartTyping    func(channelID string)
	StopTyping     func(channelID string)
	OnExecWake     func(focusID, context string)
	OnExecDone     func(focusID, summary string, durationSec float64)
}

// NewExecutiveV2 creates a new v2 executive
func NewExecutiveV2(
	graph *graph.DB,
	buffers *buffer.ConversationBuffer,
	reflexLog *reflex.Log,
	statePath string,
	cfg ExecutiveV2Config,
) *ExecutiveV2 {
	tmux := NewTmux()
	return &ExecutiveV2{
		tmux:      tmux,
		session:   NewSimpleSession(tmux),
		attention: focus.New(),
		queue:     focus.NewQueue(statePath, 100),
		graph:     graph,
		buffers:   buffers,
		reflexLog: reflexLog,
		config:    cfg,
	}
}

// SetTypingCallbacks sets the typing indicator callbacks
func (e *ExecutiveV2) SetTypingCallbacks(start, stop func(channelID string)) {
	e.config.StartTyping = start
	e.config.StopTyping = stop
}

// Start initializes the executive
func (e *ExecutiveV2) Start() error {
	if err := e.tmux.EnsureSession(); err != nil {
		return fmt.Errorf("failed to create tmux session: %w", err)
	}

	// Load queue state
	if err := e.queue.Load(); err != nil {
		log.Printf("[executive-v2] Warning: failed to load queue: %v", err)
	}

	log.Println("[executive-v2] Started, tmux session ready")
	return nil
}

// AddPending adds an item to the pending queue
func (e *ExecutiveV2) AddPending(item *focus.PendingItem) error {
	return e.queue.Add(item)
}

// ProcessNext processes the next item in the attention queue
// Returns true if an item was processed, false if queue was empty
func (e *ExecutiveV2) ProcessNext(ctx context.Context) (bool, error) {
	// Select next item from queue
	e.attention.AddPending(e.queue.PopHighest())
	item := e.attention.SelectNext()
	if item == nil {
		return false, nil
	}

	// Set as current focus
	e.attention.Focus(item)
	defer e.attention.Complete()

	// Process the item
	if err := e.processItem(ctx, item); err != nil {
		return true, err
	}

	return true, nil
}

// ProcessItem processes a specific pending item
func (e *ExecutiveV2) ProcessItem(ctx context.Context, item *focus.PendingItem) error {
	e.attention.Focus(item)
	defer e.attention.Complete()
	return e.processItem(ctx, item)
}

// processItem handles a single focus item
func (e *ExecutiveV2) processItem(ctx context.Context, item *focus.PendingItem) error {
	log.Printf("[executive-v2] Processing item: id=%s, type=%s, priority=%s",
		item.ID, item.Type, item.Priority)

	// Get channel ID for typing indicator
	channelID := item.ChannelID
	if channelID == "" {
		if ch, ok := item.Data["channel_id"].(string); ok {
			channelID = ch
		}
	}

	// Start typing indicator
	if channelID != "" && e.config.StartTyping != nil {
		e.config.StartTyping(channelID)
		defer func() {
			if e.config.StopTyping != nil {
				e.config.StopTyping(channelID)
			}
		}()
	}

	// Log executive wake
	if e.config.OnExecWake != nil {
		e.config.OnExecWake(item.ID, truncate(item.Content, 100))
	}

	// Build context bundle
	bundle := e.buildContext(item)

	// Build prompt from context
	prompt := e.buildPrompt(bundle)

	if strings.TrimSpace(prompt) == "" {
		log.Printf("[executive-v2] Empty prompt, skipping item %s", item.ID)
		return nil
	}

	// Set up callbacks
	var output strings.Builder
	e.session.OnOutput(func(text string) {
		output.WriteString(text)
	})

	e.session.OnToolCall(func(name string, args map[string]any) (string, error) {
		return e.handleToolCall(item, name, args)
	})

	// Send to Claude
	claudeCfg := ClaudeConfig{
		Model:   e.config.Model,
		WorkDir: e.config.WorkDir,
	}

	startTime := time.Now()

	if e.config.UseInteractive {
		// Track session start
		if e.config.SessionTracker != nil {
			e.config.SessionTracker.StartSession(e.session.SessionID(), item.ID)
		}

		if err := e.session.SendPrompt(prompt, claudeCfg); err != nil {
			return fmt.Errorf("interactive prompt failed: %w", err)
		}

		// Mark items as seen and update buffer sync time
		e.session.MarkItemsSeen([]string{item.ID})
		e.session.UpdateBufferSync(time.Now())

		log.Printf("[executive-v2] Sent prompt for item %s (interactive mode)", item.ID)
		return nil
	}

	// Programmatic mode
	if e.config.SessionTracker != nil {
		e.config.SessionTracker.StartSession(e.session.SessionID(), item.ID)
	}

	if err := e.session.SendPromptPrint(ctx, prompt, claudeCfg); err != nil {
		return fmt.Errorf("prompt failed: %w", err)
	}

	duration := time.Since(startTime).Seconds()

	if e.config.SessionTracker != nil {
		e.config.SessionTracker.CompleteSession(e.session.SessionID())
	}

	// Mark items as seen and update buffer sync time
	e.session.MarkItemsSeen([]string{item.ID})
	e.session.UpdateBufferSync(time.Now())

	// Log completion
	if e.config.OnExecDone != nil {
		e.config.OnExecDone(item.ID, truncate(output.String(), 100), duration)
	}

	if output.Len() > 0 {
		log.Printf("[executive-v2] Output: %s", truncate(output.String(), 200))
	}

	return nil
}

// buildContext assembles the context bundle for the current focus
func (e *ExecutiveV2) buildContext(item *focus.PendingItem) *focus.ContextBundle {
	bundle := &focus.ContextBundle{
		CurrentFocus: item,
		Suspended:    e.attention.GetState().Suspended,
		Metadata:     make(map[string]string),
	}

	// Get core identity from graph
	if e.graph != nil {
		coreTraces, err := e.graph.GetCoreTraces()
		if err == nil {
			for _, t := range coreTraces {
				bundle.CoreIdentity = append(bundle.CoreIdentity, t.Summary)
			}
		}
	}

	// Get conversation buffer - only entries since last sync
	// This avoids re-sending context that's already in the Claude session
	if e.buffers != nil {
		var scope buffer.Scope
		if item.ChannelID != "" {
			scope = buffer.ScopeChannel(item.ChannelID)
		} else {
			scope = buffer.ScopeFocus(item.ID)
		}

		// Get only entries since last buffer sync, with filtering:
		// - ExcludeID: skip the current focus item (it's in Current Focus section)
		// - ExcludeBotAuthor: on incremental sync, skip bot's own responses
		//   (Claude already knows what it said in this session)
		since := e.session.LastBufferSync()
		filter := buffer.BufferFilter{
			ExcludeID:        item.ID, // Don't duplicate current focus in buffer
			ExcludeBotAuthor: e.config.BotAuthor,
		}
		entries, summary, hasNew := e.buffers.GetEntriesSinceFiltered(scope, since, filter)

		if hasNew {
			var parts []string

			// Include summary only on first sync (when since is zero)
			if since.IsZero() && summary != "" {
				parts = append(parts, fmt.Sprintf("[Earlier context]\n%s", summary))
			}

			// Format entries
			for _, entry := range entries {
				parts = append(parts, fmt.Sprintf("%s: %s", entry.Author, entry.Content))
			}

			bundle.BufferContent = strings.Join(parts, "\n")
		}
	}

	// Get recent reflex activity
	if e.reflexLog != nil {
		entries := e.reflexLog.GetUnsent()
		for _, entry := range entries {
			bundle.ReflexLog = append(bundle.ReflexLog, focus.ReflexActivity{
				Timestamp: entry.Timestamp,
				Query:     entry.Query,
				Response:  entry.Response,
				Reflex:    entry.Reflex,
			})
		}
	}

	// Retrieve relevant memories from graph
	if e.graph != nil {
		// TODO: Use embedding similarity for query
		// For now, just get activated traces
		traces, err := e.graph.GetActivatedTraces(0.1, 10)
		if err == nil {
			for _, t := range traces {
				bundle.Memories = append(bundle.Memories, focus.MemorySummary{
					ID:        t.ID,
					Summary:   t.Summary,
					Relevance: t.Activation,
				})
			}
		}
	}

	return bundle
}

// buildPrompt constructs the prompt from a context bundle
func (e *ExecutiveV2) buildPrompt(bundle *focus.ContextBundle) string {
	var prompt strings.Builder

	// Core identity - only include on first prompt to this session
	// (Claude session already has this context after first message)
	if len(bundle.CoreIdentity) > 0 && e.session.IsFirstPrompt() {
		prompt.WriteString("## Identity\n")
		for _, identity := range bundle.CoreIdentity {
			prompt.WriteString(fmt.Sprintf("- %s\n", identity))
		}
		prompt.WriteString("\n")
	}

	// Recent reflex activity
	if len(bundle.ReflexLog) > 0 {
		prompt.WriteString("## Recent Reflex Activity\n")
		prompt.WriteString("(Handled by reflexes without executive involvement)\n")
		for _, entry := range bundle.ReflexLog {
			prompt.WriteString(fmt.Sprintf("- User: %s\n  Bud: %s\n", entry.Query, entry.Response))
		}
		prompt.WriteString("\n")
	}

	// Relevant memories
	if len(bundle.Memories) > 0 {
		// Sort by relevance (highest first)
		sort.Slice(bundle.Memories, func(i, j int) bool {
			return bundle.Memories[i].Relevance > bundle.Memories[j].Relevance
		})
		prompt.WriteString("## Relevant Memories\n")
		for _, mem := range bundle.Memories {
			prompt.WriteString(fmt.Sprintf("- %s\n", mem.Summary))
		}
		prompt.WriteString("\n")
	}

	// Conversation buffer
	if bundle.BufferContent != "" {
		prompt.WriteString("## Recent Conversation\n")
		prompt.WriteString(bundle.BufferContent)
		prompt.WriteString("\n\n")
	}

	// Suspended items (if any)
	if len(bundle.Suspended) > 0 {
		prompt.WriteString("## Suspended Tasks\n")
		for _, item := range bundle.Suspended {
			prompt.WriteString(fmt.Sprintf("- [%s] %s\n", item.Type, truncate(item.Content, 50)))
		}
		prompt.WriteString("\n")
	}

	// Current focus item
	if bundle.CurrentFocus != nil {
		prompt.WriteString("## Current Focus\n")
		prompt.WriteString(fmt.Sprintf("Type: %s\n", bundle.CurrentFocus.Type))
		prompt.WriteString(fmt.Sprintf("Priority: %s\n", bundle.CurrentFocus.Priority))
		if bundle.CurrentFocus.Source != "" {
			prompt.WriteString(fmt.Sprintf("Source: %s\n", bundle.CurrentFocus.Source))
		}
		prompt.WriteString(fmt.Sprintf("Content: %s\n", bundle.CurrentFocus.Content))
		prompt.WriteString("\n")
	}

	return prompt.String()
}

// handleToolCall processes a tool call from Claude
func (e *ExecutiveV2) handleToolCall(item *focus.PendingItem, name string, args map[string]any) (string, error) {
	log.Printf("[executive-v2] Tool call for item %s: %s(%v)", item.ID, name, args)

	switch name {
	case "respond_to_user", "send_message", "talk_to_user":
		return e.toolRespondToUser(item, args)

	case "save_thought", "remember":
		return e.toolSaveThought(args)

	case "complete", "signal_done":
		return e.toolComplete(item, args)

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// toolRespondToUser queues a message to send
func (e *ExecutiveV2) toolRespondToUser(item *focus.PendingItem, args map[string]any) (string, error) {
	content, ok := args["content"].(string)
	if !ok {
		if msg, ok := args["message"].(string); ok {
			content = msg
		} else {
			return "", fmt.Errorf("content/message is required")
		}
	}

	channelID := item.ChannelID
	if channelID == "" {
		if ch, ok := args["channel_id"].(string); ok {
			channelID = ch
		}
	}

	if channelID == "" {
		return "", fmt.Errorf("no channel_id available")
	}

	// TODO: Queue to outbox for effector
	log.Printf("[executive-v2] Would send to %s: %s", channelID, truncate(content, 100))

	return "Message queued for sending", nil
}

// toolSaveThought saves a thought to memory
func (e *ExecutiveV2) toolSaveThought(args map[string]any) (string, error) {
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}

	if e.graph != nil {
		trace := &graph.Trace{
			ID:        fmt.Sprintf("trace-%d", time.Now().UnixNano()),
			Summary:   content,
			Topic:     "thought",
			IsCore:    false,
			CreatedAt: time.Now(),
		}
		if err := e.graph.AddTrace(trace); err != nil {
			return "", fmt.Errorf("failed to save thought: %w", err)
		}
	}

	return "Thought saved", nil
}

// toolComplete marks the current focus as complete
func (e *ExecutiveV2) toolComplete(item *focus.PendingItem, args map[string]any) (string, error) {
	summary := ""
	if s, ok := args["summary"].(string); ok {
		summary = s
	}

	log.Printf("[executive-v2] Item %s marked complete: %s", item.ID, summary)

	// Complete session tracking
	if e.config.SessionTracker != nil {
		e.config.SessionTracker.CompleteSession(e.session.SessionID())
	}

	if e.config.OnExecDone != nil {
		e.config.OnExecDone(item.ID, summary, 0)
	}

	return "Focus marked complete", nil
}

// GetSession returns the underlying session
func (e *ExecutiveV2) GetSession() *SimpleSession {
	return e.session
}

// GetAttention returns the attention system
func (e *ExecutiveV2) GetAttention() *focus.Attention {
	return e.attention
}

// GetQueue returns the pending queue
func (e *ExecutiveV2) GetQueue() *focus.Queue {
	return e.queue
}

// HasActiveSessions returns true if Claude is running
func (e *ExecutiveV2) HasActiveSessions() bool {
	return e.session.IsReady()
}

// TodayThinkingMinutes returns total thinking time today
func (e *ExecutiveV2) TodayThinkingMinutes() float64 {
	if e.config.SessionTracker == nil {
		return 0
	}
	return e.config.SessionTracker.TodayThinkingMinutes()
}

// Stop shuts down the executive
func (e *ExecutiveV2) Stop() error {
	// Save queue state
	if err := e.queue.Save(); err != nil {
		log.Printf("[executive-v2] Warning: failed to save queue: %v", err)
	}

	// Close session
	return e.session.Close()
}
