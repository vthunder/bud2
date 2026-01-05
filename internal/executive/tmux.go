package executive

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	// SessionName is the tmux session for bud2
	SessionName = "bud2"
)

// Tmux manages tmux sessions and windows for executive threads
type Tmux struct {
	session string
}

// NewTmux creates a new tmux manager
func NewTmux() *Tmux {
	return &Tmux{
		session: SessionName,
	}
}

// EnsureSession creates the bud2 tmux session if it doesn't exist
func (t *Tmux) EnsureSession() error {
	if t.SessionExists() {
		return nil
	}

	// Create detached session with a monitor window
	cmd := exec.Command("tmux", "new-session", "-d", "-s", t.session, "-n", "monitor")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create tmux session: %w", err)
	}

	return nil
}

// SessionExists checks if the bud2 session exists
func (t *Tmux) SessionExists() bool {
	cmd := exec.Command("tmux", "has-session", "-t", t.session)
	return cmd.Run() == nil
}

// WindowExists checks if a window exists in the session
func (t *Tmux) WindowExists(windowName string) bool {
	cmd := exec.Command("tmux", "list-windows", "-t", t.session, "-F", "#{window_name}")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	for _, name := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if name == windowName {
			return true
		}
	}
	return false
}

// CreateWindow creates a new window in the session
func (t *Tmux) CreateWindow(windowName string) error {
	if t.WindowExists(windowName) {
		return nil // already exists
	}

	cmd := exec.Command("tmux", "new-window", "-t", t.session, "-n", windowName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create window %s: %w", windowName, err)
	}

	return nil
}

// KillWindow destroys a window
func (t *Tmux) KillWindow(windowName string) error {
	if !t.WindowExists(windowName) {
		return nil // already gone
	}

	target := fmt.Sprintf("%s:%s", t.session, windowName)
	cmd := exec.Command("tmux", "kill-window", "-t", target)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to kill window %s: %w", windowName, err)
	}

	return nil
}

// SendKeys sends keystrokes to a window (like typing)
func (t *Tmux) SendKeys(windowName string, keys string) error {
	target := fmt.Sprintf("%s:%s", t.session, windowName)
	cmd := exec.Command("tmux", "send-keys", "-t", target, keys, "Enter")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to send keys to %s: %w", windowName, err)
	}

	return nil
}

// SendKeysLiteral sends keys without pressing Enter
func (t *Tmux) SendKeysLiteral(windowName string, keys string) error {
	target := fmt.Sprintf("%s:%s", t.session, windowName)
	cmd := exec.Command("tmux", "send-keys", "-t", target, "-l", keys)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to send keys to %s: %w", windowName, err)
	}

	return nil
}

// RunCommand runs a command in a window (creates window if needed)
func (t *Tmux) RunCommand(windowName string, command string) error {
	if err := t.CreateWindow(windowName); err != nil {
		return err
	}

	return t.SendKeys(windowName, command)
}

// CapturePane captures the current content of a window's pane
func (t *Tmux) CapturePane(windowName string, lines int) (string, error) {
	target := fmt.Sprintf("%s:%s", t.session, windowName)

	args := []string{"capture-pane", "-t", target, "-p"}
	if lines > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", lines))
	}

	cmd := exec.Command("tmux", args...)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to capture pane %s: %w", windowName, err)
	}

	return string(output), nil
}

// ListWindows returns all window names in the session
func (t *Tmux) ListWindows() ([]string, error) {
	cmd := exec.Command("tmux", "list-windows", "-t", t.session, "-F", "#{window_name}")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list windows: %w", err)
	}

	names := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(names) == 1 && names[0] == "" {
		return []string{}, nil
	}
	return names, nil
}

// SendInterrupt sends Ctrl+C to a window
func (t *Tmux) SendInterrupt(windowName string) error {
	target := fmt.Sprintf("%s:%s", t.session, windowName)
	cmd := exec.Command("tmux", "send-keys", "-t", target, "C-c")
	return cmd.Run()
}

// SelectWindow brings focus to a window (for visual debugging)
func (t *Tmux) SelectWindow(windowName string) error {
	target := fmt.Sprintf("%s:%s", t.session, windowName)
	cmd := exec.Command("tmux", "select-window", "-t", target)
	return cmd.Run()
}
