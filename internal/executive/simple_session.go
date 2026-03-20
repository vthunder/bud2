package executive

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	claudecode "github.com/severity1/claude-agent-sdk-go"
	"github.com/vthunder/bud2/internal/logging"
)

// ErrInterrupted is returned by SendPrompt when the session is cancelled by a
// higher-priority item (e.g. a P1 user message arriving during a background wake).
var ErrInterrupted = errors.New("session interrupted by higher-priority item")

const (
	// MaxContextTokens is the threshold for context tokens before auto-reset.
	// Uses cache_read_input_tokens from the API which tells us how much session
	// history is being read from cache. With a 200K context window, we reset
	// at 150K to leave headroom for the current prompt + response.
	MaxContextTokens = 150000
)

// SimpleSession manages a single persistent Claude session via the SDK
type SimpleSession struct {
	mu sync.Mutex

	sessionID        string
	sessionStartTime time.Time // When this session started (for guardrails)
	statePath        string    // Path to state directory for reset coordination

	// Track what's been sent to this session
	seenItemIDs   map[string]bool
	seenMemoryIDs map[string]bool   // Track which memory traces have been sent
	memoryIDMap   map[string]string // Map trace_id -> short hash display ID (tr_xxxxx)

	// Track conversation buffer sync to avoid re-sending already-seen context
	// Only buffer entries after this time need to be sent
	lastBufferSync time.Time

	// Track number of user messages for reset threshold
	userMessageCount int

	// Usage from last completed prompt
	lastUsage *SessionUsage

	// Claude-assigned session ID (from result event) — use this for --resume
	claudeSessionID string

	// Track if we've received text output for current prompt (to avoid duplicates)
	currentPromptHasText bool

	// isResuming is set by PrepareForResume to tell buildPrompt to skip static context
	// (core identity, conversation buffer) that's already in the Claude session history.
	isResuming bool

	// Callbacks
	onToolCall func(name string, args map[string]any) (string, error)
	onOutput   func(text string)
}

// NewSimpleSession creates a new simple session manager
func NewSimpleSession(statePath string) *SimpleSession {
	return &SimpleSession{
		sessionID:        generateSessionUUID(),
		sessionStartTime: time.Now(),
		seenItemIDs:      make(map[string]bool),
		seenMemoryIDs:    make(map[string]bool),
		memoryIDMap:      make(map[string]string),
		statePath:        statePath,
	}
}

// generateSessionUUID creates a random UUID v4
func generateSessionUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%12x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// resetPendingPath returns the path to the reset pending flag file
func (s *SimpleSession) resetPendingPath() string {
	if s.statePath == "" {
		return ""
	}
	return s.statePath + "/reset.pending"
}

// isResetPending checks if a memory reset is pending
func (s *SimpleSession) isResetPending() bool {
	path := s.resetPendingPath()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// clearResetPending removes the reset pending flag
func (s *SimpleSession) clearResetPending() {
	path := s.resetPendingPath()
	if path != "" {
		os.Remove(path)
	}
}

// SessionID returns the current session ID
func (s *SimpleSession) SessionID() string {
	return s.sessionID
}

// SessionStartTime returns when this session was created (for guardrails)
func (s *SimpleSession) SessionStartTime() time.Time {
	return s.sessionStartTime
}

// HasSeenItem returns true if an item has been sent to this session
func (s *SimpleSession) HasSeenItem(id string) bool {
	return s.seenItemIDs[id]
}

// MarkItemsSeen marks items as having been sent to Claude
func (s *SimpleSession) MarkItemsSeen(ids []string) {
	for _, id := range ids {
		s.seenItemIDs[id] = true
	}
}

// HasSeenMemory returns true if a memory trace has been sent to this session
func (s *SimpleSession) HasSeenMemory(id string) bool {
	return s.seenMemoryIDs[id]
}

// MarkMemoriesSeen marks memory traces as having been sent to Claude
func (s *SimpleSession) MarkMemoriesSeen(ids []string) {
	for _, id := range ids {
		s.seenMemoryIDs[id] = true
	}
}

// SeenMemoryCount returns how many memories have been sent in this session
func (s *SimpleSession) SeenMemoryCount() int {
	return len(s.seenMemoryIDs)
}

// GetOrAssignMemoryID returns the display ID for a trace, assigning one if needed.
// Uses the first 5 chars of the real engram ID so it can be queried directly via
// GET /v1/engrams/<id>.
func (s *SimpleSession) GetOrAssignMemoryID(traceID string) string {
	if id, exists := s.memoryIDMap[traceID]; exists {
		return id
	}
	id := traceID
	if len(id) > 5 {
		id = id[:5]
	}
	s.memoryIDMap[traceID] = id
	return id
}

// GetMemoryID returns the display ID for a trace, or empty string if not assigned
func (s *SimpleSession) GetMemoryID(traceID string) string {
	return s.memoryIDMap[traceID]
}

// ResolveMemoryEval takes a memory_eval map like {"a3f9c": 5, "b2e1d": 1} (5-char engram prefix)
// and returns a map of trace_id -> rating by reversing the memoryIDMap lookup.
// Also skips legacy "M1", "M2" format keys which can no longer be resolved.
// Unknown display IDs are skipped.
func (s *SimpleSession) ResolveMemoryEval(eval map[string]any) map[string]int {
	// Build reverse map: display_id -> trace_id
	reverseMap := make(map[string]string, len(s.memoryIDMap))
	for traceID, displayID := range s.memoryIDMap {
		reverseMap[displayID] = traceID
	}

	resolved := make(map[string]int)
	for key, val := range eval {
		// Parse rating value first
		var rating int
		switch v := val.(type) {
		case float64:
			rating = int(v)
		case int:
			rating = v
		default:
			continue
		}

		// Try new format (tr_xxxxx) first
		if traceID, ok := reverseMap[key]; ok {
			resolved[traceID] = rating
			continue
		}

		// Legacy format: Parse "M1" -> look up in old sequential map
		// This won't work for new sessions but provides graceful degradation
		var displayID int
		if _, err := fmt.Sscanf(key, "M%d", &displayID); err == nil {
			// Can't resolve legacy IDs in new system, skip
			continue
		}
	}
	return resolved
}

// LastBufferSync returns when the buffer was last synced to this session
func (s *SimpleSession) LastBufferSync() time.Time {
	return s.lastBufferSync
}

// UpdateBufferSync updates the buffer sync timestamp
// Call this after sending a prompt that included buffer content
func (s *SimpleSession) UpdateBufferSync(t time.Time) {
	s.lastBufferSync = t
}

// IncrementUserMessages increments the user message counter
// Call this when processing a user message (not autonomous wakes)
func (s *SimpleSession) IncrementUserMessages() {
	s.userMessageCount++
}

// UserMessageCount returns the number of user messages in this session
func (s *SimpleSession) UserMessageCount() int {
	return s.userMessageCount
}

// IsFirstPrompt returns true if no prompts have been sent to this session
// Used to determine if core identity and full buffer history should be included
func (s *SimpleSession) IsFirstPrompt() bool {
	return len(s.seenItemIDs) == 0 && s.lastBufferSync.IsZero()
}

// PrepareNewSession rotates the session ID and clears per-prompt state so the
// caller can record the correct ID with the session tracker before sending.
// Must be called before StartSession + SendPrompt.
func (s *SimpleSession) PrepareNewSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = generateSessionUUID()
	s.sessionStartTime = time.Now()
	s.memoryIDMap = make(map[string]string)
	s.seenMemoryIDs = make(map[string]bool)
	s.claudeSessionID = ""
	s.isResuming = false
}

// PrepareForResume prepares for a new turn in an ongoing Claude session.
// Unlike PrepareNewSession, it preserves claudeSessionID (for --resume),
// seenMemoryIDs (to avoid re-injecting seen memories), and lastBufferSync
// (to only send new episodes). It clears memoryIDMap so memory self-eval
// display IDs are fresh for this turn, and sets isResuming so buildPrompt
// skips static context already present in the session history.
//
// Call this instead of PrepareNewSession when ClaudeSessionID() is non-empty
// and ShouldReset() is false.
func (s *SimpleSession) PrepareForResume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = generateSessionUUID() // Fresh tracking ID per turn
	s.sessionStartTime = time.Now()
	s.memoryIDMap = make(map[string]string) // Fresh display IDs for memory eval
	// claudeSessionID preserved — used for --resume flag
	// seenMemoryIDs preserved — avoids re-injecting already-sent memories
	// lastBufferSync preserved — only sends new conversation episodes
	s.isResuming = true
}

// IsResuming returns true if this turn is resuming an existing Claude session.
// Used by buildPrompt to skip static context already in the session history.
func (s *SimpleSession) IsResuming() bool {
	return s.isResuming
}

// OnToolCall sets the callback for tool calls (informational — actual execution
// happens inside the Claude subprocess via MCP).
func (s *SimpleSession) OnToolCall(fn func(name string, args map[string]any) (string, error)) {
	s.onToolCall = fn
}

// OnOutput sets the callback for Claude's text output
func (s *SimpleSession) OnOutput(fn func(text string)) {
	s.onOutput = fn
}

// SendPrompt sends a prompt to Claude and blocks until the response is complete.
func (s *SimpleSession) SendPrompt(ctx context.Context, prompt string, cfg ClaudeConfig) error {
	// Check for reset pending before sending
	if s.isResetPending() {
		log.Printf("[simple-session] Reset pending, waiting for reset_session signal...")
		deadline := time.Now().Add(10 * time.Second)
		for s.isResetPending() && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if s.isResetPending() {
			log.Printf("[simple-session] Warning: reset still pending after timeout, clearing flag")
			s.clearResetPending()
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentPromptHasText = false

	opts := []claudecode.Option{
		claudecode.WithPermissionMode(claudecode.PermissionModeBypassPermissions),
		claudecode.WithPartialStreaming(), // captures SessionID from first StreamEvent
	}

	// Resume existing Claude session when available, otherwise let the SDK
	// create a new session (no --session-id equivalent in SDK).
	if s.claudeSessionID != "" && s.isResuming {
		log.Printf("[simple-session] Resuming Claude session %s (bud turn %s)", s.claudeSessionID, s.sessionID)
		opts = append(opts, claudecode.WithResume(s.claudeSessionID))
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

	const sessionTimeout = 30 * time.Minute
	timeoutCtx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()

	logging.Debug("simple-session", "SendPrompt: prompt_len=%d resuming=%v", len(prompt), s.isResuming)

	err := claudecode.WithClient(timeoutCtx, func(client claudecode.Client) error {
		if err := client.Query(timeoutCtx, prompt); err != nil {
			return err
		}
		msgCount := 0
		heartbeatDone := make(chan struct{})
		go func() {
			t := time.NewTicker(60 * time.Second)
			defer t.Stop()
			lastCount := 0
			for {
				select {
				case <-t.C:
					if msgCount == lastCount {
						log.Printf("[simple-session] WARNING: no new messages for 60s (msgs_so_far=%d)", msgCount)
					}
					lastCount = msgCount
				case <-heartbeatDone:
					return
				case <-timeoutCtx.Done():
					return
				}
			}
		}()
		msgCh := client.ReceiveMessages(timeoutCtx)
	receiveLoop:
		for {
			select {
			case msg, ok := <-msgCh:
				if !ok {
					break receiveLoop
				}
				msgCount++
				switch m := msg.(type) {
				case *claudecode.StreamEvent:
					// Capture session ID from the first streaming event — this arrives
					// within milliseconds and ensures claudeSessionID is set before
					// signal_done can cancel the context (which skips ResultMessage).
					if m.SessionID != "" && s.claudeSessionID == "" {
						s.claudeSessionID = m.SessionID
						logging.Debug("simple-session", "Captured Claude session ID early: %s", m.SessionID)
					}
				case *claudecode.AssistantMessage:
					for _, block := range m.Content {
						switch b := block.(type) {
						case *claudecode.TextBlock:
							if !s.currentPromptHasText {
								s.currentPromptHasText = true
							}
							logging.Debug("simple-session", "Text block (%d chars)", len(b.Text))
							if s.onOutput != nil {
								s.onOutput(b.Text)
							}
						case *claudecode.ToolUseBlock:
							logging.Debug("simple-session", "Tool call: %s", b.Name)
							if s.onToolCall != nil {
								s.onToolCall(b.Name, b.Input)
							}
						}
					}
				case *claudecode.ResultMessage:
					s.claudeSessionID = m.SessionID
					s.lastUsage = parseUsageFromResult(m)
					log.Printf("[simple-session] Claude session ID: %s (turns=%d duration=%dms)",
						m.SessionID, m.NumTurns, m.DurationMs)
					break receiveLoop
				}
			case <-timeoutCtx.Done():
				log.Printf("[simple-session] Context cancelled, exiting receive loop (msgs_so_far=%d)", msgCount)
				break receiveLoop
			}
		}
		close(heartbeatDone)
		return nil
	}, opts...)

	if err != nil {
		if ctx.Err() != nil {
			log.Printf("[simple-session] Session %s interrupted (context cancelled)", s.sessionID)
			return ErrInterrupted
		}
		if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			log.Printf("[simple-session] Session %s timed out after %v", s.sessionID, sessionTimeout)
			return fmt.Errorf("claude session timed out after %v", sessionTimeout)
		}
		// Ignore unknown message type errors (e.g. rate_limit_event) — non-fatal
		if strings.Contains(err.Error(), "unknown message type") {
			return nil
		}
		return err
	}

	return nil
}

// parseUsageFromResult extracts SessionUsage from a ResultMessage.
func parseUsageFromResult(m *claudecode.ResultMessage) *SessionUsage {
	usage := &SessionUsage{
		NumTurns:      m.NumTurns,
		DurationMs:    m.DurationMs,
		DurationApiMs: m.DurationAPIMs,
	}
	if m.Usage != nil {
		u := *m.Usage
		usage.InputTokens = intFromUsage(u, "input_tokens")
		usage.OutputTokens = intFromUsage(u, "output_tokens")
		usage.CacheCreationInputTokens = intFromUsage(u, "cache_creation_input_tokens")
		usage.CacheReadInputTokens = intFromUsage(u, "cache_read_input_tokens")
	}
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		logging.Debug("simple-session", "Usage: input=%d output=%d cache_read=%d turns=%d duration=%dms",
			usage.InputTokens, usage.OutputTokens, usage.CacheReadInputTokens, usage.NumTurns, usage.DurationMs)
	}
	return usage
}

// intFromUsage extracts an int value from the Usage map by key.
func intFromUsage(u map[string]any, key string) int {
	v, ok := u[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// Close is a no-op since there's no persistent process to clean up
func (s *SimpleSession) Close() error {
	return nil
}

// Reset clears the session state for a fresh start
func (s *SimpleSession) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = generateSessionUUID()
	s.sessionStartTime = time.Now()
	s.seenItemIDs = make(map[string]bool)
	s.seenMemoryIDs = make(map[string]bool)
	s.memoryIDMap = make(map[string]string)
	s.lastBufferSync = time.Time{} // Reset buffer sync so full buffer is sent on first prompt
	s.userMessageCount = 0         // Reset message counter
	s.lastUsage = nil              // Clear usage data
	s.claudeSessionID = ""         // Force new session (no resume)
	s.isResuming = false
	s.clearResetPending() // Clear the pending flag so new sessions can start
	log.Printf("[simple-session] Session reset complete, new session ID: %s", s.sessionID)
}

// LastUsage returns the usage metrics from the last completed prompt, or nil
func (s *SimpleSession) LastUsage() *SessionUsage {
	return s.lastUsage
}

// ClaudeSessionID returns the Claude-assigned session ID from the last completed
// prompt. Use this value with `claude --resume` to reload the session.
func (s *SimpleSession) ClaudeSessionID() string {
	return s.claudeSessionID
}

// ShouldReset returns true if the session should be reset before sending
// the next prompt. Uses context token count from the API response.
func (s *SimpleSession) ShouldReset() bool {
	if s.lastUsage == nil {
		return false
	}

	// Total context = cached history + new input tokens
	totalContext := s.lastUsage.CacheReadInputTokens + s.lastUsage.InputTokens
	if totalContext > MaxContextTokens {
		log.Printf("[simple-session] Context tokens %d exceeds threshold %d, should reset",
			totalContext, MaxContextTokens)
		return true
	}

	return false
}

func truncatePrompt(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
