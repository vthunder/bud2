package executive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// HookRunner discovers and runs lifecycle hook scripts from skill directories.
// Scripts live in hooks/bud/<EventName> inside each skill dir. Each event's
// scripts are chained: the stdout JSON of one script becomes the stdin of the
// next. If a script fails the payload is passed through unchanged.
type HookRunner struct {
	handlers map[string][]string // event name → ordered list of script absolute paths
}

// NewHookRunner creates a HookRunner with an empty handler set.
func NewHookRunner() *HookRunner {
	return &HookRunner{handlers: make(map[string][]string)}
}

// Discover scans skill directories for hooks/bud/ subdirectories.
// Each file inside hooks/bud/ is treated as a hook script named after the
// event it handles (e.g. "UserPromptSubmit", "SessionStart").
// Scripts are appended in the order skill dirs are provided.
func (h *HookRunner) Discover(skillDirs []string) {
	for _, dir := range skillDirs {
		hooksDir := filepath.Join(dir, "hooks", "bud")
		entries, err := os.ReadDir(hooksDir)
		if err != nil {
			continue // hooks/bud/ not present in this dir — skip silently
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			eventName := entry.Name()
			scriptPath := filepath.Join(hooksDir, eventName)
			h.handlers[eventName] = append(h.handlers[eventName], scriptPath)
			log.Printf("[hooks] discovered handler for %s: %s", eventName, scriptPath)
		}
	}
}

// Run executes all scripts registered for event, chaining payloads.
// The initial payload is augmented with {"event": eventName}.
// Each script receives the current payload as JSON on stdin; its stdout JSON
// becomes the payload for the next script. Non-zero exits log a warning and
// leave the payload unchanged for the next script in the chain.
// Returns the final payload after all scripts complete.
func (h *HookRunner) Run(event string, payload map[string]interface{}) (map[string]interface{}, error) {
	if payload == nil {
		payload = make(map[string]interface{})
	}
	payload["event"] = event

	scripts := h.handlers[event]
	if len(scripts) == 0 {
		return payload, nil
	}

	current := payload
	for _, scriptPath := range scripts {
		next, err := runHookScript(scriptPath, current)
		if err != nil {
			log.Printf("[hooks] WARNING: script %s (event %s) failed: %v", scriptPath, event, err)
			// Continue chain with unmodified payload
			continue
		}
		if next != nil {
			current = next
		}
	}

	return current, nil
}

// RunPreToolUse fires all registered PreToolUse scripts for the given tool call.
// Each script receives {"event":"PreToolUse","tool_name":name,"tool_input":input} on stdin.
// If any script exits with code 1, RunPreToolUse returns (true, stderr) to signal that the
// tool call should be blocked. Other non-zero exits are logged as warnings and do not block.
// Returns (false, "") if no scripts are registered or all scripts exit 0.
func (h *HookRunner) RunPreToolUse(toolName string, toolInput map[string]any) (blocked bool, reason string) {
	scripts := h.handlers["PreToolUse"]
	if len(scripts) == 0 {
		return false, ""
	}
	payload := map[string]interface{}{
		"event":      "PreToolUse",
		"tool_name":  toolName,
		"tool_input": toolInput,
	}
	for _, scriptPath := range scripts {
		blk, rsn, err := runHookScriptWithBlocking(scriptPath, payload)
		if err != nil {
			log.Printf("[hooks] WARNING: PreToolUse script %s failed: %v", scriptPath, err)
			continue
		}
		if blk {
			log.Printf("[hooks] PreToolUse script %s blocked tool %s: %s", scriptPath, toolName, rsn)
			return true, rsn
		}
	}
	return false, ""
}

// runHookScriptWithBlocking executes a hook script, differentiating exit code 1 (block)
// from other non-zero exits (error). Returns (blocked=true, reason=stderr, nil) for exit 1.
// Returns (false, "", error) for other non-zero exits or execution failures.
// Returns (false, "", nil) on success.
func runHookScriptWithBlocking(scriptPath string, payload map[string]interface{}) (blocked bool, reason string, err error) {
	data, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return false, "", fmt.Errorf("marshal payload: %w", marshalErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, scriptPath)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1 {
			return true, strings.TrimSpace(stderr.String()), nil
		}
		return false, "", fmt.Errorf("execute: %w", runErr)
	}
	return false, "", nil
}

// runHookScript executes a single hook script with payload as JSON on stdin.
// Returns the parsed JSON stdout as the new payload, or nil if stdout is empty.
func runHookScript(scriptPath string, payload map[string]interface{}) (map[string]interface{}, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, scriptPath)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Env = os.Environ()

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("execute: %w", err)
	}

	if len(out) == 0 {
		return payload, nil // no output — return payload unchanged
	}

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse output JSON: %w", err)
	}

	return result, nil
}
