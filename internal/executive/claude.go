package executive

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ClaudeSession manages a Claude Code session for a thread
type ClaudeSession struct {
	threadID  string
	sessionID string
	tmux      *Tmux
	mu        sync.Mutex

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
		threadID:  threadID,
		sessionID: generateUUID(),
		tmux:      tmux,
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
// This is for visual debugging - you can attach to the tmux session
func (c *ClaudeSession) SendPromptInteractive(prompt string, cfg ClaudeConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Sanitize thread ID for tmux window name (replace dots with dashes)
	safeThreadID := strings.ReplaceAll(c.threadID, ".", "-")
	windowName := fmt.Sprintf("thread-%s", safeThreadID)

	// Ensure tmux session exists
	if err := c.tmux.EnsureSession(); err != nil {
		return fmt.Errorf("failed to ensure tmux session: %w", err)
	}

	// Check if window already exists (resuming session)
	isNewWindow := !c.tmux.WindowExists(windowName)
	if isNewWindow {
		// Create window and start Claude
		if err := c.tmux.CreateWindow(windowName); err != nil {
			return fmt.Errorf("failed to create window: %w", err)
		}

		// Start Claude in the window
		claudeCmd := fmt.Sprintf("claude --session-id %s", c.sessionID)
		if cfg.Model != "" {
			claudeCmd += fmt.Sprintf(" --model %s", cfg.Model)
		}

		// Small delay then start Claude
		time.Sleep(100 * time.Millisecond)
		if err := c.tmux.SendKeys(windowName, claudeCmd); err != nil {
			return fmt.Errorf("failed to start claude: %w", err)
		}

		// Wait for Claude to fully initialize (OAuth, plugins, etc.)
		log.Printf("[claude] Waiting for Claude to initialize in window %s...", windowName)
		time.Sleep(5 * time.Second)
	}

	// Send the prompt
	log.Printf("[claude] Sending interactive prompt to window %s: %s", windowName, truncatePrompt(prompt, 100))

	// For multiline prompts, collapse to single line for interactive mode
	singleLinePrompt := strings.ReplaceAll(prompt, "\n", " ")

	// Use send-keys with the prompt text directly
	target := fmt.Sprintf("%s:%s", c.tmux.session, windowName)
	cmd := exec.Command("tmux", "send-keys", "-t", target, singleLinePrompt, "Enter")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to send prompt: %w", err)
	}

	return nil
}

// GetWindowOutput captures recent output from the tmux window
func (c *ClaudeSession) GetWindowOutput(lines int) (string, error) {
	safeThreadID := strings.ReplaceAll(c.threadID, ".", "-")
	windowName := fmt.Sprintf("thread-%s", safeThreadID)
	return c.tmux.CapturePane(windowName, lines)
}

// Interrupt sends Ctrl+C to stop Claude
func (c *ClaudeSession) Interrupt() error {
	safeThreadID := strings.ReplaceAll(c.threadID, ".", "-")
	windowName := fmt.Sprintf("thread-%s", safeThreadID)
	return c.tmux.SendInterrupt(windowName)
}

// Close destroys the tmux window for this session
func (c *ClaudeSession) Close() error {
	safeThreadID := strings.ReplaceAll(c.threadID, ".", "-")
	windowName := fmt.Sprintf("thread-%s", safeThreadID)
	return c.tmux.KillWindow(windowName)
}

func truncatePrompt(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
