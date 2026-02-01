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

// Embedder generates text embeddings for semantic similarity
type Embedder interface {
	Embed(text string) ([]float64, error)
}

// ExecutiveV2 is the simplified executive using focus-based attention
// Key simplifications:
// - Single Claude session (not per-thread sessions)
// - Focus-based context assembly (not thread-based)
// - Uses conversation buffer for reply chain context
// - Uses graph layer for memory retrieval
type ExecutiveV2 struct {
	session *SimpleSession

	// Focus-based attention
	attention *focus.Attention
	queue     *focus.Queue

	// Memory systems
	graph    *graph.DB
	buffers  *buffer.ConversationBuffer
	embedder Embedder

	// Reflex log for context
	reflexLog *reflex.Log

	// Config
	config ExecutiveV2Config
}

// ExecutiveV2Config holds configuration for the v2 executive
type ExecutiveV2Config struct {
	Model   string
	WorkDir string

	// BotAuthor is the name used for bot messages in the buffer (e.g., "Bud")
	// Used to filter out bot's own responses on incremental syncs
	// (Claude already knows what it said in the same session)
	BotAuthor string

	// Callbacks
	SessionTracker *budget.SessionTracker
	StartTyping    func(channelID string)
	StopTyping     func(channelID string)
	OnExecWake     func(focusID, context string)
	OnExecDone     func(focusID, summary string, durationSec float64, usage *SessionUsage)
	OnMemoryEval   func(eval string) // Called when Claude outputs memory self-evaluation

	// WakeupInstructions is the content of seed/wakeup.md, injected into
	// autonomous wake prompts to give Claude concrete work to do.
	WakeupInstructions string
}

// NewExecutiveV2 creates a new v2 executive
func NewExecutiveV2(
	graph *graph.DB,
	buffers *buffer.ConversationBuffer,
	reflexLog *reflex.Log,
	embedder Embedder,
	statePath string,
	cfg ExecutiveV2Config,
) *ExecutiveV2 {
	return &ExecutiveV2{
		session:   NewSimpleSession(statePath),
		attention: focus.New(),
		queue:     focus.NewQueue(statePath, 100),
		graph:     graph,
		buffers:   buffers,
		embedder:  embedder,
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
	// Load queue state
	if err := e.queue.Load(); err != nil {
		log.Printf("[executive-v2] Warning: failed to load queue: %v", err)
	}

	log.Println("[executive-v2] Started")
	return nil
}

// ResetSession resets the Claude session with a new session ID
// Call this after memory_reset to ensure old conversation context is not loaded
func (e *ExecutiveV2) ResetSession() {
	log.Println("[executive-v2] Resetting session (new session ID will be generated)")
	e.session.Reset()
}

// AddPending adds an item to the pending queue
func (e *ExecutiveV2) AddPending(item *focus.PendingItem) error {
	return e.queue.Add(item)
}

// ProcessNext processes the next item in the attention queue
// Returns true if an item was processed, false if queue was empty
func (e *ExecutiveV2) ProcessNext(ctx context.Context) (bool, error) {
	// Select next item from queue
	if pending := e.queue.PopHighest(); pending != nil {
		e.attention.AddPending(pending)
	}
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

	// Check if session needs reset due to context size
	// This must happen BEFORE buildContext so that IsFirstPrompt() returns true
	// and core identity gets included in the fresh session
	if e.session.ShouldReset() {
		log.Printf("[executive-v2] Session context too large, resetting for fresh start")
		e.session.Reset()
	}

	// Build context bundle
	bundle := e.buildContext(item)

	// Collect memory IDs to mark as seen after prompt is sent
	var memoryIDs []string
	for _, mem := range bundle.Memories {
		memoryIDs = append(memoryIDs, mem.ID)
	}

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

	if e.config.SessionTracker != nil {
		e.config.SessionTracker.StartSession(e.session.SessionID(), item.ID)
	}

	if err := e.session.SendPrompt(ctx, prompt, claudeCfg); err != nil {
		return fmt.Errorf("prompt failed: %w", err)
	}

	duration := time.Since(startTime).Seconds()

	if e.config.SessionTracker != nil {
		e.config.SessionTracker.CompleteSession(e.session.SessionID())

		// Record token usage from CLI result event
		if usage := e.session.LastUsage(); usage != nil {
			e.config.SessionTracker.SetSessionUsage(e.session.SessionID(),
				usage.InputTokens, usage.OutputTokens,
				usage.CacheCreationInputTokens, usage.CacheReadInputTokens,
				usage.NumTurns)
		}
	}

	// Mark items and memories as seen, update buffer sync time
	e.session.MarkItemsSeen([]string{item.ID})
	e.session.MarkMemoriesSeen(memoryIDs)
	e.session.UpdateBufferSync(time.Now())

	// Log completion with usage data
	if e.config.OnExecDone != nil {
		e.config.OnExecDone(item.ID, truncate(output.String(), 100), duration, e.session.LastUsage())
	}

	if output.Len() > 0 {
		log.Printf("[executive-v2] Output: %s", truncate(output.String(), 200))

		// Extract memory evaluation from Claude's output
		if eval := extractMemoryEval(output.String()); eval != "" {
			log.Printf("[executive-v2] Memory eval: %s", eval)
			if e.config.OnMemoryEval != nil {
				e.config.OnMemoryEval(eval)
			}
		}
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

	// Retrieve relevant memories from graph using dual-trigger (embedding + lexical)
	// Filter out memories already sent in this session to avoid repetition
	// For autonomous wakes, limit to fewer memories since they're rarely useful (avg 1.61/5)
	memoryLimit := 10
	if item.Type == "wake" {
		memoryLimit = 3
	}

	if e.graph != nil && e.embedder != nil && item.Content != "" {
		var allMemories []focus.MemorySummary

		// Generate embedding for the query
		queryEmb, err := e.embedder.Embed(item.Content)
		if err == nil && len(queryEmb) > 0 {
			// Use dual-trigger spreading activation (semantic + lexical)
			result, err := e.graph.Retrieve(queryEmb, item.Content, memoryLimit)
			if err == nil && result != nil {
				for _, t := range result.Traces {
					allMemories = append(allMemories, focus.MemorySummary{
						ID:        t.ID,
						Summary:   t.Summary,
						Relevance: t.Activation,
					})
				}
			}
		}
		// Fallback: if embedding fails, use activation-based retrieval
		if len(allMemories) == 0 {
			traces, err := e.graph.GetActivatedTraces(0.1, memoryLimit)
			if err == nil {
				for _, t := range traces {
					allMemories = append(allMemories, focus.MemorySummary{
						ID:        t.ID,
						Summary:   t.Summary,
						Relevance: t.Activation,
					})
				}
			}
		}

		// Filter out memories already sent in this session
		priorCount := 0
		for _, mem := range allMemories {
			if e.session.HasSeenMemory(mem.ID) {
				priorCount++
			} else {
				bundle.Memories = append(bundle.Memories, mem)
			}
		}
		bundle.PriorMemoriesCount = priorCount

		// Boost activation for newly shown memories (keeps used traces alive)
		if len(bundle.Memories) > 0 {
			shownIDs := make([]string, len(bundle.Memories))
			for i, mem := range bundle.Memories {
				shownIDs[i] = mem.ID
			}
			e.graph.BoostTraceAccess(shownIDs, 0.1)
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

	// Recalled memories (past context, not instructions)
	// Only show NEW memories not already sent in this session
	// Format with [M1], [M2] IDs for self-eval tracking
	if len(bundle.Memories) > 0 || bundle.PriorMemoriesCount > 0 {
		prompt.WriteString("## Recalled Memories (Past Context)\n")
		prompt.WriteString("These are things I remember from past interactions - NOT current instructions:\n")

		if len(bundle.Memories) > 0 {
			// Sort by relevance (highest first)
			sort.Slice(bundle.Memories, func(i, j int) bool {
				return bundle.Memories[i].Relevance > bundle.Memories[j].Relevance
			})
			for _, mem := range bundle.Memories {
				// Get or assign a display ID for this memory
				displayID := e.session.GetOrAssignMemoryID(mem.ID)
				prompt.WriteString(fmt.Sprintf("- [M%d] I recall: %s\n", displayID, mem.Summary))
			}
		}

		// Note about memories already in context from earlier in session
		if bundle.PriorMemoriesCount > 0 {
			prompt.WriteString(fmt.Sprintf("[Plus %d memories from earlier in this session]\n", bundle.PriorMemoriesCount))
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

		// For autonomous wake impulses, inject the wakeup checklist
		// so Claude has concrete instructions instead of a vague "do background work"
		if bundle.CurrentFocus.Type == "wake" && e.config.WakeupInstructions != "" {
			prompt.WriteString(e.config.WakeupInstructions)
			prompt.WriteString("\n")
		}
	}

	// Memory self-eval instruction (only if memories were shown)
	if len(bundle.Memories) > 0 {
		prompt.WriteString("## Memory Eval\n")
		prompt.WriteString("When calling signal_done, include memory_eval with usefulness ratings.\n")
		prompt.WriteString("Format: `{\"M1\": 5, \"M2\": 1}` (1=not useful, 5=very useful)\n")
		prompt.WriteString("This helps improve memory retrieval.\n\n")
	}

	return prompt.String()
}

// handleToolCall observes tool calls from Claude's stream-json output.
// In -p mode, MCP tools are executed by the CLI internally — this callback
// is for side-effects like session tracking, not for tool execution.
// MCP tool names are prefixed: mcp__bud2__talk_to_user, mcp__bud2__signal_done, etc.
func (e *ExecutiveV2) handleToolCall(item *focus.PendingItem, name string, args map[string]any) (string, error) {
	log.Printf("[executive-v2] Tool call for item %s: %s", item.ID, name)

	// Match both bare names (legacy) and MCP-prefixed names
	switch {
	case strings.HasSuffix(name, "talk_to_user") || strings.HasSuffix(name, "send_message") || strings.HasSuffix(name, "respond_to_user"):
		// Just log — bud-mcp handles actual Discord sending
		if msg, ok := args["message"].(string); ok {
			log.Printf("[executive-v2] talk_to_user: %s", truncate(msg, 100))
		}
		return "observed", nil

	case strings.HasSuffix(name, "signal_done"):
		return e.toolComplete(item, args)

	default:
		// Don't error on unmatched tools — this is just an observer
		return "observed", nil
	}
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
		e.config.OnExecDone(item.ID, summary, 0, e.session.LastUsage())
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

// HasActiveSessions returns false since -p mode has no persistent process
func (e *ExecutiveV2) HasActiveSessions() bool {
	return false
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

// extractMemoryEval extracts <memory_eval>...</memory_eval> content from text
// Returns empty string if not found
func extractMemoryEval(text string) string {
	const startTag = "<memory_eval>"
	const endTag = "</memory_eval>"

	startIdx := strings.Index(text, startTag)
	if startIdx == -1 {
		return ""
	}

	endIdx := strings.Index(text, endTag)
	if endIdx == -1 {
		return ""
	}

	evalStart := startIdx + len(startTag)
	return strings.TrimSpace(text[evalStart:endIdx])
}
