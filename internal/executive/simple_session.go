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

const (
	// MainWindowName is the single tmux window for the Claude session
	MainWindowName = "bud-main"
)

// SimpleSession manages a single persistent Claude session
// This replaces the multi-session SessionManager approach with a simpler model:
// - One Claude process in one tmux window
// - Persists across focus switches
// - Context switching = memory retrieval, not process management
type SimpleSession struct {
	mu sync.Mutex

	tmux      *Tmux
	sessionID string
	pid       int
	state     ProcessState
	startedAt time.Time

	// Track what's been sent to this session
	seenItemIDs map[string]bool

	// Track conversation buffer sync to avoid re-sending already-seen context
	// Only buffer entries after this time need to be sent
	lastBufferSync time.Time

	// Callbacks
	onToolCall func(name string, args map[string]any) (string, error)
	onOutput   func(text string)
}

// NewSimpleSession creates a new simple session manager
func NewSimpleSession(tmux *Tmux) *SimpleSession {
	return &SimpleSession{
		tmux:        tmux,
		sessionID:   generateSessionUUID(),
		seenItemIDs: make(map[string]bool),
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

// LastBufferSync returns when the buffer was last synced to this session
func (s *SimpleSession) LastBufferSync() time.Time {
	return s.lastBufferSync
}

// UpdateBufferSync updates the buffer sync timestamp
// Call this after sending a prompt that included buffer content
func (s *SimpleSession) UpdateBufferSync(t time.Time) {
	s.lastBufferSync = t
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

// IsReady returns true if Claude is running and ready
func (s *SimpleSession) IsReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == ProcessReady || s.state == ProcessBusy
}

// EnsureRunning ensures Claude is running, starting it if needed
func (s *SimpleSession) EnsureRunning(cfg ClaudeConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If we have a PID, verify it's still good
	if s.pid != 0 {
		if err := s.verifyRunning(); err == nil {
			return nil // All good
		}
		log.Printf("[simple-session] Process verification failed, restarting...")
		s.cleanup()
	}

	// Start fresh
	if err := s.tmux.EnsureSession(); err != nil {
		return fmt.Errorf("failed to ensure tmux session: %w", err)
	}

	// Kill any existing bud-main window (stale state)
	if s.tmux.WindowExists(MainWindowName) {
		log.Printf("[simple-session] Killing stale window %s", MainWindowName)
		s.tmux.KillWindow(MainWindowName)
		time.Sleep(200 * time.Millisecond)
	}

	// Create window
	if err := s.tmux.CreateWindow(MainWindowName); err != nil {
		return fmt.Errorf("failed to create window: %w", err)
	}

	// Start Claude
	if err := s.startClaude(cfg); err != nil {
		s.tmux.KillWindow(MainWindowName)
		return fmt.Errorf("failed to start Claude: %w", err)
	}

	// Wait for ready
	log.Printf("[simple-session] Waiting for Claude to initialize (pid=%d)...", s.pid)
	if err := s.waitForReady(90 * time.Second); err != nil {
		s.cleanup()
		return fmt.Errorf("Claude failed to initialize: %w", err)
	}

	log.Printf("[simple-session] Claude ready (pid=%d, session=%s)", s.pid, s.sessionID)
	return nil
}

// startClaude starts Claude and captures its PID
func (s *SimpleSession) startClaude(cfg ClaudeConfig) error {
	s.state = ProcessStarting
	s.startedAt = time.Now()

	pidFile := fmt.Sprintf("/tmp/bud-claude-%s.pid", MainWindowName)

	// Determine which flag to use
	sessionFlag := "--session-id"
	sessionFile := s.findSessionFile()
	if sessionFile != "" {
		info, err := os.Stat(sessionFile)
		if err == nil && info.Size() >= 100 {
			log.Printf("[simple-session] Found valid session file, using --continue")
			sessionFlag = "--continue"
		} else if err == nil {
			log.Printf("[simple-session] Found invalid session file (%d bytes), deleting", info.Size())
			os.Remove(sessionFile)
		}
	}

	// Build Claude command
	os.Remove(pidFile)
	claudeCmd := fmt.Sprintf("claude --dangerously-skip-permissions %s %s", sessionFlag, s.sessionID)
	if cfg.Model != "" {
		claudeCmd += fmt.Sprintf(" --model %s", cfg.Model)
	}

	wrapper := fmt.Sprintf("echo $$ > %s && exec %s", pidFile, claudeCmd)
	log.Printf("[simple-session] Starting Claude: %s", claudeCmd)

	if err := s.tmux.SendKeys(MainWindowName, wrapper); err != nil {
		s.state = ProcessDead
		return fmt.Errorf("failed to send claude command: %w", err)
	}

	// Poll for PID file
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			pidStr := strings.TrimSpace(string(data))
			if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
				s.pid = pid
				os.Remove(pidFile)
				log.Printf("[simple-session] Captured PID %d", s.pid)
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	s.state = ProcessDead
	return fmt.Errorf("timeout waiting for Claude PID")
}

// waitForReady waits for Claude to be ready using /status
func (s *SimpleSession) waitForReady(timeout time.Duration) error {
	target := fmt.Sprintf("%s:%s", s.tmux.session, MainWindowName)

	const (
		pollInterval        = 200 * time.Millisecond
		statusRetryInterval = 2 * time.Second
	)

	deadline := time.Now().Add(timeout)
	startTime := time.Now()
	var lastStatusSent time.Time

	time.Sleep(1 * time.Second) // Initial delay

	for time.Now().Before(deadline) {
		// Send /status periodically
		if lastStatusSent.IsZero() || time.Since(lastStatusSent) >= statusRetryInterval {
			log.Printf("[simple-session] Sending /status to detect readiness...")
			exec.Command("tmux", "send-keys", "-t", target, "Escape").Run()
			time.Sleep(50 * time.Millisecond)
			exec.Command("tmux", "send-keys", "-t", target, "-l", "/status").Run()
			time.Sleep(50 * time.Millisecond)
			exec.Command("tmux", "send-keys", "-t", target, "Enter").Run()
			lastStatusSent = time.Now()
		}

		// Check for status dialog
		output, err := s.tmux.CapturePane(MainWindowName, 30)
		if err == nil && strings.Contains(strings.ToLower(output), "to cancel") {
			log.Printf("[simple-session] Detected /status dialog after %.1fs, sending ESC",
				time.Since(startTime).Seconds())

			exec.Command("tmux", "send-keys", "-t", target, "Escape").Run()
			time.Sleep(100 * time.Millisecond)

			s.state = ProcessReady
			return nil
		}

		// Check if process died
		if s.pid != 0 {
			proc, err := process.NewProcess(int32(s.pid))
			if err != nil {
				s.state = ProcessDead
				output, _ := s.tmux.CapturePane(MainWindowName, 20)
				return fmt.Errorf("Claude process died during startup. Output:\n%s", output)
			}
			running, _ := proc.IsRunning()
			if !running {
				s.state = ProcessDead
				return fmt.Errorf("Claude process exited during startup")
			}
		}

		time.Sleep(pollInterval)
	}

	output, _ := s.tmux.CapturePane(MainWindowName, 30)
	return fmt.Errorf("timeout waiting for Claude (%.1fs). Output:\n%s", timeout.Seconds(), output)
}

// verifyRunning checks that Claude is still running
func (s *SimpleSession) verifyRunning() error {
	if !s.tmux.WindowExists(MainWindowName) {
		return fmt.Errorf("window does not exist")
	}

	if s.pid == 0 {
		return fmt.Errorf("no PID tracked")
	}

	proc, err := process.NewProcess(int32(s.pid))
	if err != nil {
		s.state = ProcessDead
		return fmt.Errorf("process not found: %w", err)
	}

	running, _ := proc.IsRunning()
	if !running {
		s.state = ProcessDead
		return fmt.Errorf("process not running")
	}

	cmdline, err := proc.Cmdline()
	if err != nil {
		return fmt.Errorf("failed to get cmdline: %w", err)
	}
	if !strings.Contains(cmdline, s.sessionID) {
		s.state = ProcessDead
		return fmt.Errorf("process cmdline does not contain session ID")
	}

	return nil
}

// cleanup kills the Claude process
func (s *SimpleSession) cleanup() {
	if s.pid != 0 {
		log.Printf("[simple-session] Cleaning up process %d", s.pid)
		syscall.Kill(s.pid, syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
		syscall.Kill(s.pid, syscall.SIGKILL)
		s.pid = 0
	}
	if s.tmux.WindowExists(MainWindowName) {
		s.tmux.KillWindow(MainWindowName)
	}
	s.state = ProcessDead
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

// SendPrompt sends a prompt to Claude interactively
func (s *SimpleSession) SendPrompt(prompt string, cfg ClaudeConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure Claude is running
	if err := s.ensureRunningLocked(cfg); err != nil {
		return fmt.Errorf("failed to ensure Claude running: %w", err)
	}

	s.state = ProcessBusy
	defer func() {
		if s.state == ProcessBusy {
			s.state = ProcessReady
		}
	}()

	// Send the prompt
	log.Printf("[simple-session] Sending prompt (pid=%d): %s", s.pid, truncatePrompt(prompt, 100))

	target := fmt.Sprintf("%s:%s", s.tmux.session, MainWindowName)

	// Send prompt text literally
	cmdText := exec.Command("tmux", "send-keys", "-t", target, "-l", prompt)
	if err := cmdText.Run(); err != nil {
		return fmt.Errorf("failed to send prompt text: %w", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Send Enter with retry
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

// ensureRunningLocked ensures Claude is running (must hold lock)
func (s *SimpleSession) ensureRunningLocked(cfg ClaudeConfig) error {
	if s.pid != 0 {
		if err := s.verifyRunning(); err == nil {
			return nil
		}
		log.Printf("[simple-session] Process verification failed, restarting...")
		s.cleanup()
	}

	// Release lock for startup (it takes time)
	s.mu.Unlock()
	err := s.EnsureRunning(cfg)
	s.mu.Lock()
	return err
}

// SendPromptPrint sends a prompt using print mode (non-interactive)
func (s *SimpleSession) SendPromptPrint(ctx context.Context, prompt string, cfg ClaudeConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	args := []string{
		"--print",
		"--session-id", s.sessionID,
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

// GetWindowOutput captures recent output from the tmux window
func (s *SimpleSession) GetWindowOutput(lines int) (string, error) {
	return s.tmux.CapturePane(MainWindowName, lines)
}

// Interrupt sends Ctrl+C to stop Claude
func (s *SimpleSession) Interrupt() error {
	return s.tmux.SendInterrupt(MainWindowName)
}

// Close destroys the session
func (s *SimpleSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanup()
	return nil
}

// Reset clears the session state for a fresh start
func (s *SimpleSession) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanup()
	s.sessionID = generateSessionUUID()
	s.seenItemIDs = make(map[string]bool)
	s.lastBufferSync = time.Time{} // Reset buffer sync so full buffer is sent on first prompt
	s.state = ProcessNone
}
