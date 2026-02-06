package effectors

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/types"
)

// TestEffector is a simple effector for synthetic mode testing
// It captures all actions to a JSONL file instead of sending to Discord
type TestEffector struct {
	outputPath string
	mu         sync.Mutex
}

// NewTestEffector creates a test effector that writes to the given path
func NewTestEffector(statePath string) *TestEffector {
	outputPath := filepath.Join(statePath, "system", "test_output.jsonl")
	return &TestEffector{
		outputPath: outputPath,
	}
}

// Submit queues an action (immediately executes in test mode)
func (e *TestEffector) Submit(action *types.Action) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Immediately write to output file
	f, err := os.OpenFile(e.outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[test-effector] Failed to write action: %v", err)
		return
	}
	defer f.Close()

	// Write action as JSONL
	data, err := json.Marshal(action)
	if err != nil {
		log.Printf("[test-effector] Failed to marshal action: %v", err)
		return
	}

	f.Write(data)
	f.WriteString("\n")

	// Log for debugging
	if action.Type == "send_message" {
		if content, ok := action.Payload["content"].(string); ok {
			displayContent := content
			if len(displayContent) > 80 {
				displayContent = displayContent[:80] + "..."
			}
			log.Printf("[test-effector] Message: %s", displayContent)
		}
	}
}

// Start is a no-op for test effector (no background polling needed)
func (e *TestEffector) Start() {
	log.Println("[test-effector] Started (no-op)")
}

// Stop is a no-op for test effector
func (e *TestEffector) Stop() {
	log.Println("[test-effector] Stopped (no-op)")
}

// SetOnSend is a no-op for test effector
func (e *TestEffector) SetOnSend(callback func(channelID, content string)) {
	// No-op - test mode doesn't need memory capture
}

// SetOnAction is a no-op for test effector
func (e *TestEffector) SetOnAction(callback func(actionType, channelID, content, source string)) {
	// No-op - test mode doesn't need activity logging
}

// SetOnError is a no-op for test effector
func (e *TestEffector) SetOnError(callback func(actionID, actionType, errMsg string)) {
	// No-op - test mode doesn't need error logging
}

// SetOnRetry is a no-op for test effector
func (e *TestEffector) SetOnRetry(callback func(actionID, actionType, errMsg string, attempt int, nextRetry time.Duration)) {
	// No-op - test mode doesn't retry
}

// SetPendingInteractionCallback is a no-op for test effector
func (e *TestEffector) SetPendingInteractionCallback(callback func(channelID string) *PendingInteraction) {
	// No-op - test mode doesn't handle interactions
}

// SetMaxRetryDuration is a no-op for test effector
func (e *TestEffector) SetMaxRetryDuration(d time.Duration) {
	// No-op - test mode doesn't retry
}

// ClearOutput clears the test output file (useful for test setup)
func (e *TestEffector) ClearOutput() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := os.Remove(e.outputPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to clear test output: %w", err)
	}
	return nil
}
