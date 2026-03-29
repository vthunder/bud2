package executive

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	claudecode "github.com/severity1/claude-agent-sdk-go"
)

// receiveLoopCallbacks holds optional hooks called by receiveLoop for each
// message type.  The loop handles writeLog internally; callbacks only need to
// implement session-specific side-effects (updating state, firing events, etc).
type receiveLoopCallbacks struct {
	// OnStreamEvent is called for every StreamEvent that carries a non-empty SessionID.
	OnStreamEvent func(sessionID string)
	// OnThinking is called for every ThinkingBlock (after writeLog).
	OnThinking func(text string)
	// OnText is called for every TextBlock (after writeLog).
	OnText func(text string)
	// OnTool is called for every ToolUseBlock (after writeLog).
	OnTool func(name string, input map[string]any)
	// OnResult is called for the ResultMessage (after writeLog).
	OnResult func(m *claudecode.ResultMessage)
	// OnMsg is called for every message received (before type-switch).
	// Useful for callers that need to count messages (e.g. heartbeat).
	OnMsg func()
	// LogPrefix, when non-empty, enables generic loop-exit log lines.
	// e.g. "simple-session" or "subagent-a1b2c3d4"
	LogPrefix string
}

// receiveLoop drains client.ReceiveMessages, writes structured log entries to
// logFile, and dispatches to the callbacks in cb.  It returns when the channel
// is closed, a ResultMessage is received, or ctx is cancelled.
func receiveLoop(ctx context.Context, client claudecode.Client, logFile *os.File, cb receiveLoopCallbacks) error {
	msgCh := client.ReceiveMessages(ctx)
	msgCount := 0
loop:
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				break loop
			}
			msgCount++
			if cb.OnMsg != nil {
				cb.OnMsg()
			}
			switch m := msg.(type) {
			case *claudecode.StreamEvent:
				if m.SessionID != "" && cb.OnStreamEvent != nil {
					cb.OnStreamEvent(m.SessionID)
				}
			case *claudecode.AssistantMessage:
				for _, block := range m.Content {
					switch b := block.(type) {
					case *claudecode.ThinkingBlock:
						writeLog(logFile, "THINKING (%d chars)\n%s\n", len(b.Thinking), b.Thinking)
						if cb.OnThinking != nil {
							cb.OnThinking(b.Thinking)
						}
					case *claudecode.TextBlock:
						writeLog(logFile, "TEXT (%d chars)\n%s\n", len(b.Text), b.Text)
						if cb.OnText != nil {
							cb.OnText(b.Text)
						}
					case *claudecode.ToolUseBlock:
						writeLog(logFile, "TOOL: %s  %s", b.Name, summarizeToolInput(b.Name, b.Input))
						if cb.OnTool != nil {
							cb.OnTool(b.Name, b.Input)
						}
					}
				}
			case *claudecode.ResultMessage:
				writeLog(logFile, "DONE  turns=%d  duration=%dms  in=%d  out=%d  cache_read=%d",
					m.NumTurns, m.DurationMs,
					intFromUsage(safeUsage(m), "input_tokens"),
					intFromUsage(safeUsage(m), "output_tokens"),
					intFromUsage(safeUsage(m), "cache_read_input_tokens"))
				if cb.OnResult != nil {
					cb.OnResult(m)
				}
				break loop
			}
		case <-ctx.Done():
			if cb.LogPrefix != "" {
				log.Printf("[%s] Context cancelled, exiting receive loop (msgs_so_far=%d)", cb.LogPrefix, msgCount)
			}
			break loop
		}
	}
	if cb.LogPrefix != "" {
		log.Printf("[%s] ReceiveMessages loop exited (received %d messages)", cb.LogPrefix, msgCount)
	}
	return nil
}

// summarizeToolInput returns a short human-readable summary of a tool call's
// input.  It first tries a per-tool key lookup (more precise), then falls back
// to well-known generic keys, and finally to the first non-empty string field.
func summarizeToolInput(toolName string, input map[string]any) string {
	keyByTool := map[string]string{
		"Read":  "file_path",
		"Write": "file_path",
		"Edit":  "file_path",
		"Bash":  "command",
		"Glob":  "pattern",
		"Grep":  "pattern",
	}
	if key, ok := keyByTool[toolName]; ok {
		if v, ok := input[key].(string); ok && v != "" {
			v = strings.ReplaceAll(v, "\n", " ")
			if len(v) > 120 {
				v = v[:120] + "..."
			}
			return fmt.Sprintf("%s=%q", key, v)
		}
	}
	// Fallback: well-known generic keys in priority order.
	for _, key := range []string{"command", "file_path", "pattern", "message", "content", "task"} {
		if v, ok := input[key].(string); ok && v != "" {
			v = strings.ReplaceAll(v, "\n", " ")
			if len(v) > 120 {
				v = v[:120] + "..."
			}
			return fmt.Sprintf("%s=%q", key, v)
		}
	}
	// Fallback: first non-empty string value.
	for k, v := range input {
		if s, ok := v.(string); ok && s != "" {
			s = strings.ReplaceAll(s, "\n", " ")
			if len(s) > 120 {
				s = s[:120] + "..."
			}
			return fmt.Sprintf("%s=%q", k, s)
		}
	}
	return ""
}
