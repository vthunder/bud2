package executive

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	// MaxContextTokens is the threshold for context tokens before auto-reset.
	// Uses cache_read_input_tokens from the API which tells us how much session
	// history is being read from cache. With a 200K context window, we reset
	// at 150K to leave headroom for the current prompt + response.
	MaxContextTokens = 150000
)

// SimpleSession manages a single persistent Claude session via `claude -p`
type SimpleSession struct {
	mu sync.Mutex

	sessionID string
	statePath string // Path to state directory for reset coordination

	// Track what's been sent to this session
	seenItemIDs   map[string]bool
	seenMemoryIDs map[string]bool // Track which memory traces have been sent
	memoryIDMap   map[string]int  // Map trace_id -> display ID (M1, M2, etc.)
	nextMemoryID  int             // Next display ID to assign

	// Track conversation buffer sync to avoid re-sending already-seen context
	// Only buffer entries after this time need to be sent
	lastBufferSync time.Time

	// Track number of user messages for reset threshold
	userMessageCount int

	// Usage from last completed prompt
	lastUsage *SessionUsage

	// Callbacks
	onToolCall func(name string, args map[string]any) (string, error)
	onOutput   func(text string)
}

// NewSimpleSession creates a new simple session manager
func NewSimpleSession(statePath string) *SimpleSession {
	return &SimpleSession{
		sessionID:     generateSessionUUID(),
		seenItemIDs:   make(map[string]bool),
		seenMemoryIDs: make(map[string]bool),
		memoryIDMap:   make(map[string]int),
		nextMemoryID:  1,
		statePath:     statePath,
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
// When true, new sessions should not be started until Reset() is called
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

// GetOrAssignMemoryID returns the display ID for a trace, assigning one if needed
// This ensures the same trace always gets the same ID within a session
func (s *SimpleSession) GetOrAssignMemoryID(traceID string) int {
	if id, exists := s.memoryIDMap[traceID]; exists {
		return id
	}
	id := s.nextMemoryID
	s.memoryIDMap[traceID] = id
	s.nextMemoryID++
	return id
}

// GetMemoryID returns the display ID for a trace, or 0 if not assigned
func (s *SimpleSession) GetMemoryID(traceID string) int {
	return s.memoryIDMap[traceID]
}

// ResolveMemoryEval takes a memory_eval map like {"M1": 5, "M2": 1} and returns
// a map of trace_id -> rating by reversing the memoryIDMap lookup.
// Unknown display IDs are skipped.
func (s *SimpleSession) ResolveMemoryEval(eval map[string]any) map[string]int {
	// Build reverse map: display_id -> trace_id
	reverseMap := make(map[int]string, len(s.memoryIDMap))
	for traceID, displayID := range s.memoryIDMap {
		reverseMap[displayID] = traceID
	}

	resolved := make(map[string]int)
	for key, val := range eval {
		// Parse "M1" -> 1
		var displayID int
		if _, err := fmt.Sscanf(key, "M%d", &displayID); err != nil {
			continue
		}
		// Parse rating value
		var rating int
		switch v := val.(type) {
		case float64:
			rating = int(v)
		case int:
			rating = v
		default:
			continue
		}
		// Resolve to trace_id
		if traceID, ok := reverseMap[displayID]; ok {
			resolved[traceID] = rating
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

// OnToolCall sets the callback for tool calls
func (s *SimpleSession) OnToolCall(fn func(name string, args map[string]any) (string, error)) {
	s.onToolCall = fn
}

// OnOutput sets the callback for Claude's text output
func (s *SimpleSession) OnOutput(fn func(text string)) {
	s.onOutput = fn
}

// SendPrompt sends a prompt to Claude using print mode (non-interactive)
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

	args := []string{
		"--print",
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"--verbose",
	}

	// First prompt creates the session; subsequent prompts resume it
	if s.IsFirstPrompt() {
		args = append(args, "--session-id", s.sessionID)
	} else {
		args = append(args, "--resume", s.sessionID)
	}

	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	log.Printf("[simple-session] Starting print mode with prompt: %s", truncatePrompt(prompt, 100))

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start claude: %w", err)
	}

	go func() {
		defer stdin.Close()
		io.WriteString(stdin, prompt)
	}()

	var wg sync.WaitGroup
	var stderrBuf strings.Builder
	wg.Add(2)

	go func() {
		defer wg.Done()
		s.processStreamJSON(stdout)
	}()

	go func() {
		defer wg.Done()
		s.processStderr(stderr, &stderrBuf)
	}()

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		if stderrBuf.Len() > 0 {
			return fmt.Errorf("claude exited with error: %w\nstderr: %s", err, stderrBuf.String())
		}
		return fmt.Errorf("claude exited with error: %w", err)
	}

	return nil
}

// processStreamJSON parses Claude's stream-json output
func (s *SimpleSession) processStreamJSON(r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event StreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			log.Printf("[simple-session] Failed to parse event: %v", err)
			continue
		}

		// For result events, parse usage from the full raw JSON
		if event.Type == "result" {
			s.parseResultUsage([]byte(line))
		}

		s.handleStreamEvent(event)
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[simple-session] Scanner error: %v", err)
	}
}

func (s *SimpleSession) handleStreamEvent(event StreamEvent) {
	switch event.Type {
	case "assistant":
		if event.Message != nil {
			var msg struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(event.Message, &msg); err == nil {
				for _, block := range msg.Content {
					if block.Type == "text" && block.Text != "" && s.onOutput != nil {
						s.onOutput(block.Text)
					}
				}
			}
		}

	case "tool_use":
		if event.Tool != nil && s.onToolCall != nil {
			log.Printf("[simple-session] Tool call: %s", event.Tool.Name)
			result, err := s.onToolCall(event.Tool.Name, event.Tool.Args)
			if err != nil {
				log.Printf("[simple-session] Tool error: %v", err)
			} else {
				log.Printf("[simple-session] Tool result: %s", truncatePrompt(result, 100))
			}
		}

	case "result":
		if event.Result != nil {
			var result string
			if err := json.Unmarshal(event.Result, &result); err == nil && result != "" && s.onOutput != nil {
				s.onOutput(result)
			}
		}

	case "content_block_delta":
		var delta struct {
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(event.Content, &delta); err == nil {
			if delta.Delta.Text != "" && s.onOutput != nil {
				s.onOutput(delta.Delta.Text)
			}
		}
	}
}

func (s *SimpleSession) processStderr(r io.Reader, buf *strings.Builder) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			log.Printf("[simple-session stderr] %s", line)
			if buf != nil {
				buf.WriteString(line + "\n")
			}
		}
	}
}

// LastUsage returns the usage metrics from the last completed prompt, or nil
func (s *SimpleSession) LastUsage() *SessionUsage {
	return s.lastUsage
}

// parseResultUsage extracts token usage from a raw result event JSON line
func (s *SimpleSession) parseResultUsage(raw []byte) {
	var result struct {
		NumTurns      int `json:"num_turns"`
		DurationMs    int `json:"duration_ms"`
		DurationApiMs int `json:"duration_api_ms"`
		Usage         struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
		ModelUsage map[string]struct {
			ContextWindow   int `json:"contextWindow"`
			MaxOutputTokens int `json:"maxOutputTokens"`
		} `json:"modelUsage"`
	}

	if err := json.Unmarshal(raw, &result); err != nil {
		log.Printf("[simple-session] Failed to parse result usage: %v", err)
		return
	}

	usage := &SessionUsage{
		InputTokens:              result.Usage.InputTokens,
		OutputTokens:             result.Usage.OutputTokens,
		CacheCreationInputTokens: result.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     result.Usage.CacheReadInputTokens,
		NumTurns:                 result.NumTurns,
		DurationMs:               result.DurationMs,
		DurationApiMs:            result.DurationApiMs,
	}

	// Extract context window from first model in modelUsage
	for _, m := range result.ModelUsage {
		usage.ContextWindow = m.ContextWindow
		usage.MaxOutputTokens = m.MaxOutputTokens
		break
	}

	s.lastUsage = usage
	log.Printf("[simple-session] Usage: input=%d output=%d cache_read=%d cache_create=%d turns=%d duration=%dms",
		usage.InputTokens, usage.OutputTokens, usage.CacheReadInputTokens,
		usage.CacheCreationInputTokens, usage.NumTurns, usage.DurationMs)
}

// Close is a no-op since there's no persistent process to clean up in -p mode
func (s *SimpleSession) Close() error {
	return nil
}

// Reset clears the session state for a fresh start
func (s *SimpleSession) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = generateSessionUUID()
	s.seenItemIDs = make(map[string]bool)
	s.seenMemoryIDs = make(map[string]bool)
	s.memoryIDMap = make(map[string]int)
	s.nextMemoryID = 1
	s.lastBufferSync = time.Time{} // Reset buffer sync so full buffer is sent on first prompt
	s.userMessageCount = 0         // Reset message counter
	s.lastUsage = nil              // Clear usage data
	s.clearResetPending()          // Clear the pending flag so new sessions can start
	log.Printf("[simple-session] Session reset complete, new session ID: %s", s.sessionID)
}

// findSessionFile looks for the Claude session file
func (s *SimpleSession) findSessionFile() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	projectsDir := homeDir + "/.claude/projects"
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}

	sessionFile := s.sessionID + ".jsonl"
	for _, entry := range entries {
		if entry.IsDir() {
			path := projectsDir + "/" + entry.Name() + "/" + sessionFile
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}

	return ""
}

// SessionContentSize calculates the actual content size of the session since
// the last compaction boundary. This counts only the message/content fields
// from user and assistant entries, excluding thinking blocks and metadata.
// Returns 0 if the file doesn't exist or can't be parsed.
func (s *SimpleSession) SessionContentSize() int64 {
	path := s.findSessionFile()
	if path == "" {
		return 0
	}

	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()

	// First pass: find last compaction boundary line number
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024) // 10MB max line size

	var lastCompactLine int
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		// Look for compact_boundary subtype
		if strings.Contains(line, `"subtype":"compact_boundary"`) ||
			strings.Contains(line, `"subtype": "compact_boundary"`) {
			lastCompactLine = lineNum
		}
	}

	// Second pass: sum content from entries after last compaction
	file.Seek(0, 0)
	scanner = bufio.NewScanner(file)
	scanner.Buffer(buf, 10*1024*1024)

	var totalSize int64
	lineNum = 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= lastCompactLine {
			continue
		}

		line := scanner.Text()
		totalSize += extractContentSize(line)
	}

	return totalSize
}

// extractContentSize extracts the size of actual content from a session entry
func extractContentSize(line string) int64 {
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}

	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return 0
	}

	switch entry.Type {
	case "user":
		// User content is a string
		var content string
		if err := json.Unmarshal(entry.Message.Content, &content); err == nil {
			return int64(len(content))
		}

	case "assistant":
		// Assistant content is an array of content blocks
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(entry.Message.Content, &blocks); err == nil {
			var size int64
			for _, block := range blocks {
				// Only count text blocks, skip thinking
				if block.Type == "text" {
					size += int64(len(block.Text))
				}
			}
			return size
		}
	}

	return 0
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
