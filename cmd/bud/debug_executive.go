package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/vthunder/bud2/internal/executive"
)

const debugListenerID = "discord-debug"

// executiveDebugger streams live executive session events into a Discord thread.
// Call Toggle to start/stop; it creates a new thread each time it starts.
type executiveDebugger struct {
	exec    *executive.ExecutiveV2
	session *discordgo.Session

	mu        sync.Mutex
	active    bool
	startedAt time.Time
	threadID  string
	eventCh   chan executive.DebugEvent
	done      chan struct{}
}

func newExecutiveDebugger(exec *executive.ExecutiveV2, session *discordgo.Session) *executiveDebugger {
	return &executiveDebugger{exec: exec, session: session}
}

// Toggle starts or stops the debug stream. Returns an ephemeral response message.
func (d *executiveDebugger) Toggle(channelID string) string {
	d.mu.Lock()

	if d.active {
		d.stopLocked()
		d.mu.Unlock()
		return "⏹ Debug stream stopped."
	}

	thread, err := d.session.ThreadStart(channelID, "🔍 Executive Debug", discordgo.ChannelTypeGuildPublicThread, 60)
	if err != nil {
		d.mu.Unlock()
		return fmt.Sprintf("❌ Failed to create debug thread: %v", err)
	}

	d.threadID = thread.ID
	d.active = true
	d.startedAt = time.Now()
	d.eventCh = make(chan executive.DebugEvent, 256)
	d.done = make(chan struct{})

	go d.runDispatcher()

	// Release the lock before registering the listener — AddDebugListener replays
	// history synchronously, which calls handleEvent, which needs to acquire d.mu.
	d.mu.Unlock()

	d.exec.AddDebugListener(debugListenerID, func(event executive.DebugEvent) {
		d.handleEvent(event)
	})

	return fmt.Sprintf("🔍 Debug stream active → <#%s>", thread.ID)
}

func (d *executiveDebugger) stopLocked() {
	d.exec.RemoveDebugListener(debugListenerID)
	if d.done != nil {
		close(d.done)
		d.done = nil
	}
	d.active = false
	d.threadID = ""
}

// handleEvent is called from notifyDebug fan-out — must never block.
func (d *executiveDebugger) handleEvent(event executive.DebugEvent) {
	d.mu.Lock()
	eventCh := d.eventCh
	d.mu.Unlock()

	if eventCh == nil {
		return
	}
	select {
	case eventCh <- event:
	default: // drop if buffer full — non-blocking
	}
}

// runDispatcher reads events from eventCh and posts them to Discord.
// Text events are batched; all other event types are posted immediately.
func (d *executiveDebugger) runDispatcher() {
	var textBuf strings.Builder
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	d.mu.Lock()
	threadID := d.threadID
	startedAt := d.startedAt
	eventCh := d.eventCh
	d.mu.Unlock()

	flushText := func() {
		if textBuf.Len() == 0 {
			return
		}
		text := textBuf.String()
		textBuf.Reset()
		if len(text) > 1900 {
			text = text[:1900] + "…"
		}
		d.post(threadID, "💬 "+text)
	}

	timePrefix := func(at time.Time) string {
		if !at.IsZero() && at.Before(startedAt) {
			return fmt.Sprintf("[%s] ", at.Format("15:04:05"))
		}
		return ""
	}

	for {
		select {
		case event := <-eventCh:
			pfx := timePrefix(event.At)
			switch event.Type {
			case executive.DebugEventSessionStart:
				flushText()
				d.post(threadID, fmt.Sprintf("%s▶ **Session started** | %s\n> %s", pfx, event.Priority, event.Focus))

			case executive.DebugEventText:
				text := event.Text
				if pfx != "" {
					text = pfx + text
				}
				textBuf.WriteString(text)
				if textBuf.Len() >= 1500 {
					flushText()
				}

			case executive.DebugEventToolCall:
				flushText()
				d.post(threadID, fmt.Sprintf("%s🔧 `%s` %s", pfx, event.Tool, formatDebugArgs(event.Args)))

			case executive.DebugEventSessionEnd:
				flushText()
				var sb strings.Builder
				sb.WriteString(pfx)
				sb.WriteString("⏹ **Session ended**")
				if event.Duration > 0 {
					sb.WriteString(fmt.Sprintf(" | %.1fs", event.Duration))
				}
				if event.Usage != nil {
					sb.WriteString(fmt.Sprintf(" | turns=%d in=%d out=%d",
						event.Usage.NumTurns, event.Usage.InputTokens, event.Usage.OutputTokens))
				}
				d.post(threadID, sb.String())
			}

		case <-ticker.C:
			flushText()

		case <-d.done:
			flushText()
			return
		}
	}
}

func (d *executiveDebugger) post(threadID, content string) {
	_, err := d.session.ChannelMessageSend(threadID, content)
	if err != nil {
		log.Printf("[debug-executive] post to thread %s failed: %v", threadID, err)
	}
}

// formatDebugArgs renders tool arguments as a short inline string.
func formatDebugArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	var parts []string
	// Put "command" or "path"/"file_path" first for readability
	priority := []string{"command", "path", "file_path", "pattern", "query"}
	seen := map[string]bool{}
	for _, k := range priority {
		if v, ok := args[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", k, truncateDebug(fmt.Sprintf("%v", v), 120)))
			seen[k] = true
		}
	}
	for k, v := range args {
		if !seen[k] {
			parts = append(parts, fmt.Sprintf("%s=%s", k, truncateDebug(fmt.Sprintf("%v", v), 60)))
		}
	}
	result := strings.Join(parts, " ")
	if len(result) > 400 {
		result = result[:400] + "…"
	}
	return result
}

func truncateDebug(s string, n int) string {
	// Strip newlines for inline display
	s = strings.ReplaceAll(s, "\n", "↵")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
