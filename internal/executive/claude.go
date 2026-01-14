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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

// ProcessState represents the state of the Claude process
type ProcessState string

const (
	ProcessNone     ProcessState = ""         // no process
	ProcessStarting ProcessState = "starting" // process launched, waiting for ready
	ProcessReady    ProcessState = "ready"    // accepting input
	ProcessBusy     ProcessState = "busy"     // processing a prompt
	ProcessDead     ProcessState = "dead"     // process exited (need cleanup)
)

// ClaudeSession manages a Claude Code session for a thread
type ClaudeSession struct {
	threadID  string
	sessionID string
	tmux      *Tmux
	mu        sync.Mutex

	// Process tracking (NEW)
	pid          int          // Claude process PID
	windowName   string       // human-readable tmux window name
	processState ProcessState // current process state
	startedAt    time.Time    // when process was started

	// State
	sessionInitialized bool            // true if Claude has been started with this session ID (exists on disk)
	firstMessageSent   bool            // track if we've sent the first message (for boilerplate)
	seenPerceptIDs     map[string]bool // track which percepts have been sent to Claude
	seenTraceIDs       map[string]bool // track which traces have been sent to Claude

	// Callbacks
	onToolCall func(name string, args map[string]any) (string, error)
	onOutput   func(text string)
}

// ClaudeConfig holds configuration for Claude sessions
type ClaudeConfig struct {
	// Model to use (default: claude-sonnet-4-20250514)
	Model string
	// Working directory for Claude
	WorkDir string
	// Whether to show in tmux (for debugging)
	ShowInTmux bool
}

// NewClaudeSession creates a session for a thread
func NewClaudeSession(threadID string, tmux *Tmux) *ClaudeSession {
	return &ClaudeSession{
		threadID:       threadID,
		sessionID:      generateUUID(),
		tmux:           tmux,
		seenPerceptIDs: make(map[string]bool),
		seenTraceIDs:   make(map[string]bool),
	}
}

// generateUUID creates a random UUID v4
func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	// Set version (4) and variant (2) bits
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%12x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// IsFirstMessage returns true if no messages have been sent yet
func (c *ClaudeSession) IsFirstMessage() bool {
	return !c.firstMessageSent
}

// MarkFirstMessageSent marks that the first message has been sent
func (c *ClaudeSession) MarkFirstMessageSent() {
	c.firstMessageSent = true
}

// HasSeenPercept returns true if this percept has been sent to Claude before
func (c *ClaudeSession) HasSeenPercept(perceptID string) bool {
	return c.seenPerceptIDs[perceptID]
}

// MarkPerceptsSeen marks percepts as having been sent to Claude
func (c *ClaudeSession) MarkPerceptsSeen(perceptIDs []string) {
	for _, id := range perceptIDs {
		c.seenPerceptIDs[id] = true
	}
}

// HasSeenTrace returns true if this trace has been sent to Claude before
func (c *ClaudeSession) HasSeenTrace(traceID string) bool {
	return c.seenTraceIDs[traceID]
}

// MarkTracesSeen marks traces as having been sent to Claude
func (c *ClaudeSession) MarkTracesSeen(traceIDs []string) {
	for _, id := range traceIDs {
		c.seenTraceIDs[id] = true
	}
}

// OnToolCall sets the callback for tool calls
func (c *ClaudeSession) OnToolCall(fn func(name string, args map[string]any) (string, error)) {
	c.onToolCall = fn
}

// OnOutput sets the callback for Claude's text output
func (c *ClaudeSession) OnOutput(fn func(text string)) {
	c.onOutput = fn
}

// SendPrompt sends a prompt to Claude and waits for response
// This uses claude -p (print mode) for programmatic interaction
func (c *ClaudeSession) SendPrompt(ctx context.Context, prompt string, cfg ClaudeConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	args := []string{
		"--print",
		"--session-id", c.sessionID,
		"--output-format", "stream-json",
		"--verbose",
	}

	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}

	// Use stdin for prompt (handles newlines better)
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

	log.Printf("[claude] Starting session %s with prompt: %s", c.sessionID, truncatePrompt(prompt, 100))
	log.Printf("[claude] Running: claude %v (prompt via stdin)", args)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start claude: %w", err)
	}

	// Write prompt to stdin and close
	go func() {
		defer stdin.Close()
		io.WriteString(stdin, prompt)
	}()

	// Process output in background
	var wg sync.WaitGroup
	var stderrBuf strings.Builder
	wg.Add(2)

	go func() {
		defer wg.Done()
		c.processStreamJSON(stdout)
	}()

	go func() {
		defer wg.Done()
		c.processStderr(stderr, &stderrBuf)
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
func (c *ClaudeSession) processStreamJSON(r io.Reader) {
	scanner := bufio.NewScanner(r)
	// Increase buffer size for large outputs
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event StreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			log.Printf("[claude] Failed to parse event: %v", err)
			continue
		}

		c.handleStreamEvent(event)
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[claude] Scanner error: %v", err)
	}
}

// StreamEvent represents a Claude stream-json event
type StreamEvent struct {
	Type       string          `json:"type"`
	Content    json.RawMessage `json:"content,omitempty"`
	Tool       *ToolUse        `json:"tool,omitempty"`
	Text       string          `json:"text,omitempty"`
	Message    json.RawMessage `json:"message,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	SubType    string          `json:"subtype,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
	Error      string          `json:"error,omitempty"`
	CostUSD    float64         `json:"costUSD,omitempty"`
	TotalCost  float64         `json:"totalCost,omitempty"`
}

// ToolUse represents a tool call
type ToolUse struct {
	Name  string         `json:"name"`
	Args  map[string]any `json:"args"`
	ID    string         `json:"id"`
}

func (c *ClaudeSession) handleStreamEvent(event StreamEvent) {
	switch event.Type {
	case "assistant":
		// Check for error first
		if event.Error != "" {
			log.Printf("[claude] Assistant error: %s", event.Error)
		}
		// Assistant message with text content
		if event.Message != nil {
			// Try to extract text from message content
			var msg struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(event.Message, &msg); err == nil {
				for _, block := range msg.Content {
					if block.Type == "text" && block.Text != "" {
						// Check if it's an error message
						if event.Error != "" || strings.Contains(block.Text, "balance is too low") {
							log.Printf("[claude] Error message: %s", block.Text)
						}
						if c.onOutput != nil {
							c.onOutput(block.Text)
						}
					}
				}
			}
		}

	case "tool_use":
		// Claude wants to use a tool
		if event.Tool != nil && c.onToolCall != nil {
			log.Printf("[claude] Tool call: %s", event.Tool.Name)
			result, err := c.onToolCall(event.Tool.Name, event.Tool.Args)
			if err != nil {
				log.Printf("[claude] Tool error: %v", err)
			} else {
				log.Printf("[claude] Tool result: %s", truncatePrompt(result, 100))
			}
			// Note: In stream mode, tool results are handled by Claude CLI internally
		}

	case "result":
		// Final result with text
		if event.Result != nil {
			var result string
			if err := json.Unmarshal(event.Result, &result); err == nil && result != "" {
				if c.onOutput != nil {
					c.onOutput(result)
				}
				// Check for error results
				if event.SubType == "error" || strings.Contains(result, "balance is too low") {
					log.Printf("[claude] Error result: %s", result)
				}
			}
		}

	case "content_block_delta":
		// Streaming text delta
		var delta struct {
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(event.Content, &delta); err == nil {
			if delta.Delta.Text != "" && c.onOutput != nil {
				c.onOutput(delta.Delta.Text)
			}
		}

	case "message_start", "message_stop", "content_block_start", "content_block_stop", "system", "user":
		// Lifecycle events, ignore

	default:
		// Log unknown events for debugging
		log.Printf("[claude] Unknown event type: %s", event.Type)
	}
}

func (c *ClaudeSession) processStderr(r io.Reader, buf *strings.Builder) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			log.Printf("[claude stderr] %s", line)
			if buf != nil {
				buf.WriteString(line)
				buf.WriteString("\n")
			}
		}
	}
}

// SendPromptInteractive runs Claude interactively in a tmux window
// This is the primary mode for agent operation (not just debugging)
func (c *ClaudeSession) SendPromptInteractive(prompt string, cfg ClaudeConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Printf("[claude] SendPromptInteractive called: windowName=%s, sessionID=%s, pid=%d",
		c.windowName, c.sessionID, c.pid)

	// Ensure Claude is running and ready
	if err := c.ensureClaudeRunning(cfg); err != nil {
		return fmt.Errorf("failed to ensure Claude running: %w", err)
	}

	// Mark busy while sending
	c.processState = ProcessBusy
	defer func() {
		if c.processState == ProcessBusy {
			c.processState = ProcessReady
		}
	}()

	// Send the prompt
	log.Printf("[claude] Sending interactive prompt to window %s (pid=%d): %s",
		c.windowName, c.pid, truncatePrompt(prompt, 100))

	target := fmt.Sprintf("%s:%s", c.tmux.session, c.windowName)

	// Send the prompt text literally (the -l flag preserves newlines and special chars)
	cmdText := exec.Command("tmux", "send-keys", "-t", target, "-l", prompt)
	if err := cmdText.Run(); err != nil {
		return fmt.Errorf("failed to send prompt text: %w", err)
	}

	// Wait 500ms for paste to complete (gastown pattern - tested, required)
	time.Sleep(500 * time.Millisecond)

	// Send Enter with retry logic (gastown pattern - 3 attempts, 200ms between)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		cmdEnter := exec.Command("tmux", "send-keys", "-t", target, "Enter")
		if err := cmdEnter.Run(); err != nil {
			lastErr = err
			continue
		}
		return nil
	}

	return fmt.Errorf("failed to send Enter after 3 attempts: %w", lastErr)
}

// ensureClaudeRunning verifies Claude is running or starts it
func (c *ClaudeSession) ensureClaudeRunning(cfg ClaudeConfig) error {
	// If we have a PID, verify it's still good
	if c.pid != 0 {
		if err := c.verifyReady(); err == nil {
			return nil // All good
		}
		// Process died or invalid - clean up
		log.Printf("[claude] Process verification failed, cleaning up...")
		c.cleanup()
	}

	// Need to start fresh
	if err := c.tmux.EnsureSession(); err != nil {
		return fmt.Errorf("failed to ensure tmux session: %w", err)
	}

	// Generate window name if needed
	if c.windowName == "" {
		c.windowName = c.generateWindowName()
	}

	// Kill any existing window with this name (stale state)
	if c.tmux.WindowExists(c.windowName) {
		log.Printf("[claude] Killing stale window %s", c.windowName)
		c.tmux.KillWindow(c.windowName)
		time.Sleep(200 * time.Millisecond)
	}

	// Create window
	if err := c.tmux.CreateWindow(c.windowName); err != nil {
		return fmt.Errorf("failed to create window: %w", err)
	}

	// Start Claude and capture PID
	if err := c.startClaude(cfg); err != nil {
		c.tmux.KillWindow(c.windowName)
		return fmt.Errorf("failed to start Claude: %w", err)
	}

	// Mark session as initialized immediately after startClaude succeeds
	// Claude Code creates the session file on startup, before it's "ready"
	// So even if initialization fails later, we need to use --resume next time
	c.sessionInitialized = true

	// Wait for ready via /status command detection
	log.Printf("[claude] Waiting for Claude to initialize (pid=%d)...", c.pid)
	if err := c.waitForReadyByStatus(90 * time.Second); err != nil {
		c.cleanup()
		return fmt.Errorf("Claude failed to initialize: %w", err)
	}

	log.Printf("[claude] Claude ready in window %s (pid=%d)", c.windowName, c.pid)
	return nil
}

// startClaude starts Claude and captures its PID
func (c *ClaudeSession) startClaude(cfg ClaudeConfig) error {
	log.Printf("[claude] startClaude called: sessionID=%s, windowName=%s", c.sessionID, c.windowName)

	// Validate session ID
	if c.sessionID == "" {
		return fmt.Errorf("cannot start Claude: empty session ID")
	}

	// Strategy: use --session-id for new sessions, --resume for existing ones.
	// --resume takes a session ID and resumes that specific session.
	// (--continue just resumes the most recent session in the directory, which
	// could be wrong if multiple sessions are active in the same directory.)

	log.Printf("[claude] Starting session: %s", c.sessionID)
	c.processState = ProcessStarting
	c.startedAt = time.Now()

	pidFile := fmt.Sprintf("/tmp/bud-claude-%s.pid", c.windowName)

	// Determine which flag to use based on session file state
	// --session-id creates a new session, --resume continues an existing one by ID
	// (Note: --continue continues the most recent session in the directory without
	// accepting a session ID, so we use --resume for explicit session resumption)
	sessionFlag := "--session-id"
	sessionFile, sessionFileSize := c.findSessionFile()

	if sessionFile != "" {
		if sessionFileSize < 100 {
			// Session file exists but is empty/tiny - invalid session from interrupted startup
			// Delete it so we can create a fresh one
			log.Printf("[claude] Found invalid session file (%d bytes), deleting: %s", sessionFileSize, sessionFile)
			os.Remove(sessionFile)
			sessionFlag = "--session-id"
		} else {
			// Session file exists and has content - use --resume to continue by ID
			log.Printf("[claude] Found valid session file (%d bytes), using --resume", sessionFileSize)
			sessionFlag = "--resume"
		}
	} else {
		log.Printf("[claude] No session file found, using --session-id")
	}

	// Now start Claude properly with PID tracking
	os.Remove(pidFile)
	claudeCmd := fmt.Sprintf("claude --dangerously-skip-permissions %s %s", sessionFlag, c.sessionID)
	if cfg.Model != "" {
		claudeCmd += fmt.Sprintf(" --model %s", cfg.Model)
	}

	wrapper := fmt.Sprintf("echo $$ > %s && exec %s", pidFile, claudeCmd)
	log.Printf("[claude] Starting with PID tracking: %s", claudeCmd)

	if err := c.tmux.SendKeys(c.windowName, wrapper); err != nil {
		c.processState = ProcessDead
		return fmt.Errorf("failed to send claude command: %w", err)
	}

	// Poll for PID file
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			pidStr := strings.TrimSpace(string(data))
			if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
				c.pid = pid
				os.Remove(pidFile)
				log.Printf("[claude] Captured PID %d", c.pid)
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	c.processState = ProcessDead
	return fmt.Errorf("timeout waiting for Claude PID file")
}

// waitForReadyByStatus waits for Claude to be ready using the /status command.
// Repeatedly sends /status until Claude responds with the dialog (Claude doesn't
// queue input during startup, only during active work). Once we see "escape to cancel"
// (or "Esc to cancel"), sends ESC to dismiss and returns.
func (c *ClaudeSession) waitForReadyByStatus(timeout time.Duration) error {
	target := fmt.Sprintf("%s:%s", c.tmux.session, c.windowName)

	const (
		pollInterval        = 200 * time.Millisecond
		statusRetryInterval = 2 * time.Second // Re-send /status this often
	)

	deadline := time.Now().Add(timeout)
	startTime := time.Now()
	var lastStatusSent time.Time

	// Initial delay - Claude needs time to start up before it can respond to /status
	time.Sleep(1 * time.Second)

	for time.Now().Before(deadline) {
		// Send /status periodically - Claude doesn't queue during startup
		if lastStatusSent.IsZero() || time.Since(lastStatusSent) >= statusRetryInterval {
			log.Printf("[claude] Sending /status to detect readiness...")
			// First send Escape to clear any autocomplete menu or partial state
			exec.Command("tmux", "send-keys", "-t", target, "Escape").Run()
			time.Sleep(50 * time.Millisecond)
			// Send /status as literal text
			exec.Command("tmux", "send-keys", "-t", target, "-l", "/status").Run()
			time.Sleep(50 * time.Millisecond)
			// Send Enter separately
			exec.Command("tmux", "send-keys", "-t", target, "Enter").Run()
			lastStatusSent = time.Now()
		}

		// Check for "escape to cancel" or "Esc to cancel" in output (case-insensitive)
		output, err := c.tmux.CapturePane(c.windowName, 30)
		if err == nil && strings.Contains(strings.ToLower(output), "to cancel") {
			// Found the status dialog - send ESC to dismiss
			log.Printf("[claude] Detected /status dialog after %.1fs, sending ESC",
				time.Since(startTime).Seconds())

			cmdEsc := exec.Command("tmux", "send-keys", "-t", target, "Escape")
			if err := cmdEsc.Run(); err != nil {
				log.Printf("[claude] Warning: failed to send ESC: %v", err)
			}

			// Brief pause to let dialog dismiss
			time.Sleep(100 * time.Millisecond)

			c.processState = ProcessReady
			return nil
		}

		// Check if process died
		if c.pid != 0 {
			proc, err := process.NewProcess(int32(c.pid))
			if err != nil {
				output, _ := c.tmux.CapturePane(c.windowName, 20)
				c.processState = ProcessDead
				if output != "" {
					return fmt.Errorf("Claude process died during startup. Window output:\n%s", output)
				}
				return fmt.Errorf("Claude process died during startup")
			}
			running, _ := proc.IsRunning()
			if !running {
				c.processState = ProcessDead
				return fmt.Errorf("Claude process exited during startup")
			}
		}

		time.Sleep(pollInterval)
	}

	// Timeout - capture output for debugging
	output, _ := c.tmux.CapturePane(c.windowName, 30)
	return fmt.Errorf("timeout waiting for /status dialog (%.1fs). Window output:\n%s",
		timeout.Seconds(), output)
}

// verifyReady checks that Claude is running and ready
func (c *ClaudeSession) verifyReady() error {
	// 1. Check window exists
	if c.windowName == "" || !c.tmux.WindowExists(c.windowName) {
		return fmt.Errorf("window %s does not exist", c.windowName)
	}

	// 2. Check PID is valid
	if c.pid == 0 {
		return fmt.Errorf("no PID tracked")
	}

	// 3. Check process is running
	proc, err := process.NewProcess(int32(c.pid))
	if err != nil {
		c.processState = ProcessDead
		return fmt.Errorf("process %d not found: %w", c.pid, err)
	}

	running, _ := proc.IsRunning()
	if !running {
		c.processState = ProcessDead
		return fmt.Errorf("process %d not running", c.pid)
	}

	// 4. Verify it's OUR Claude (cmdline contains session ID)
	cmdline, err := proc.Cmdline()
	if err != nil {
		return fmt.Errorf("failed to get cmdline for process %d: %w", c.pid, err)
	}
	if !strings.Contains(cmdline, c.sessionID) {
		c.processState = ProcessDead
		return fmt.Errorf("process %d cmdline does not contain session ID", c.pid)
	}

	// 5. Check state
	if c.processState != ProcessReady && c.processState != ProcessBusy {
		return fmt.Errorf("session state is %s, not ready", c.processState)
	}

	return nil
}

// cleanup kills the Claude process and removes the window
func (c *ClaudeSession) cleanup() {
	if c.pid != 0 {
		log.Printf("[claude] Cleaning up process %d", c.pid)
		// Try graceful kill first, then force
		syscall.Kill(c.pid, syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
		syscall.Kill(c.pid, syscall.SIGKILL)
		c.pid = 0
	}
	if c.windowName != "" && c.tmux.WindowExists(c.windowName) {
		c.tmux.KillWindow(c.windowName)
	}
	c.processState = ProcessDead
}

// generateWindowName creates a human-readable window name
func (c *ClaudeSession) generateWindowName() string {
	word := randomWord()
	b := make([]byte, 1)
	rand.Read(b)
	num := int(b[0])%99 + 1
	return fmt.Sprintf("%s-%d", word, num)
}

// findSessionFile looks for a Claude Code session file and returns its path and size
// Returns ("", 0) if not found
// Claude Code stores sessions in ~/.claude/projects/<project>/<session-id>.jsonl
func (c *ClaudeSession) findSessionFile() (string, int64) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", 0
	}

	projectsDir := homeDir + "/.claude/projects"
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", 0
	}

	sessionFile := c.sessionID + ".jsonl"
	for _, entry := range entries {
		if entry.IsDir() {
			path := projectsDir + "/" + entry.Name() + "/" + sessionFile
			info, err := os.Stat(path)
			if err == nil {
				return path, info.Size()
			}
		}
	}

	return "", 0
}

// GetWindowOutput captures recent output from the tmux window
func (c *ClaudeSession) GetWindowOutput(lines int) (string, error) {
	if c.windowName == "" {
		return "", fmt.Errorf("no window name set")
	}
	return c.tmux.CapturePane(c.windowName, lines)
}

// Interrupt sends Ctrl+C to stop Claude
func (c *ClaudeSession) Interrupt() error {
	if c.windowName == "" {
		return fmt.Errorf("no window name set")
	}
	return c.tmux.SendInterrupt(c.windowName)
}

// Close destroys the tmux window for this session
func (c *ClaudeSession) Close() error {
	c.cleanup()
	return nil
}

// stripANSI removes ANSI escape codes from a string
func stripANSI(s string) string {
	// Simple regex-free approach: skip escape sequences
	var result strings.Builder
	inEscape := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			// End of escape sequence
			if (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z') {
				inEscape = false
			}
			continue
		}
		result.WriteByte(s[i])
	}
	return result.String()
}

func truncatePrompt(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
