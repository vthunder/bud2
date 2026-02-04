package memory

import (
	"fmt"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/types"
)

// InboxMessage represents a message in the unified inbox queue.
// Types:
//   - "message" (default): External input from Discord, synthetic, etc.
//   - "signal": Control signals (session done, etc.) - not converted to percepts
//   - "impulse": Internal motivations (task due, idea, etc.)
type InboxMessage struct {
	ID        string         `json:"id"`
	Type      string         `json:"type,omitempty"`    // message (default), signal, impulse
	Subtype   string         `json:"subtype,omitempty"` // signal: done; impulse: task/idea/system; message: thought
	Content   string         `json:"content"`
	ChannelID string         `json:"channel_id,omitempty"`
	AuthorID  string         `json:"author_id,omitempty"`
	Author    string         `json:"author,omitempty"`
	Timestamp time.Time      `json:"timestamp,omitempty"`
	Status    string         `json:"status"`             // pending, processed
	Priority  int            `json:"priority,omitempty"` // for impulses (1=highest)
	Extra     map[string]any `json:"extra,omitempty"`
}

// NewInboxMessageFromImpulse converts an Impulse to an InboxMessage
func NewInboxMessageFromImpulse(impulse *types.Impulse) *InboxMessage {
	// Extract priority from impulse data if present
	priority := 2 // default medium priority
	if p, ok := impulse.Data["priority"].(int); ok {
		priority = p
	}

	return &InboxMessage{
		ID:        impulse.ID,
		Type:      "impulse",
		Subtype:   string(impulse.Source), // task, idea, system
		Content:   impulse.Description,
		Timestamp: impulse.Timestamp,
		Status:    "pending",
		Priority:  priority,
		Extra: map[string]any{
			"impulse_type": impulse.Type, // due, upcoming, recurring, explore, wake
			"intensity":    impulse.Intensity,
			"data":         impulse.Data,
		},
	}
}

// Inbox manages incoming messages from senses (in-memory only)
type Inbox struct {
	mu       sync.RWMutex
	messages map[string]*InboxMessage
}

// NewInbox creates a new inbox
func NewInbox() *Inbox {
	return &Inbox{
		messages: make(map[string]*InboxMessage),
	}
}

// Add queues a new message (only if not already present)
func (i *Inbox) Add(msg *InboxMessage) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if msg.ID == "" {
		msg.ID = fmt.Sprintf("msg-%d", time.Now().UnixNano())
	}

	// Don't overwrite existing messages - prevents duplicate processing
	// if Discord delivers the same message multiple times
	if _, exists := i.messages[msg.ID]; exists {
		return
	}

	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	msg.Status = "pending"
	i.messages[msg.ID] = msg
}

// GetPending returns all pending messages
func (i *Inbox) GetPending() []*InboxMessage {
	i.mu.RLock()
	defer i.mu.RUnlock()

	result := make([]*InboxMessage, 0)
	for _, msg := range i.messages {
		if msg.Status == "pending" {
			result = append(result, msg)
		}
	}
	return result
}

// MarkProcessed marks a message as processed
func (i *Inbox) MarkProcessed(id string) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if msg, ok := i.messages[id]; ok {
		msg.Status = "processed"
	}
}

// ToPercept converts an InboxMessage to a Percept.
// Returns nil for signals (they don't need memory/consolidation).
func (msg *InboxMessage) ToPercept() *types.Percept {
	switch msg.Type {
	case "signal":
		return nil // signals don't become percepts
	case "impulse":
		return msg.impulseToPercept()
	default:
		return msg.messageToPercept()
	}
}

// messageToPercept converts a message-type inbox entry to a Percept
func (msg *InboxMessage) messageToPercept() *types.Percept {
	channelID := msg.ChannelID
	if channelID == "" {
		channelID = "synthetic"
	}

	// Compute conversation_id from channel + time bucket (5-minute windows)
	timeBucket := msg.Timestamp.Unix() / 300 // 5-minute buckets
	conversationID := fmt.Sprintf("%s-%d", channelID, timeBucket)

	// Handle thought subtype (Bud's own thoughts)
	source := "inbox"
	msgType := "message"
	intensity := 0.8
	author := msg.Author

	if msg.Subtype == "thought" {
		source = "bud"
		msgType = "thought"
		intensity = 0.9 // thoughts are high priority for memory
		author = "Bud"
	}

	// Build percept data with core fields
	data := map[string]any{
		"channel_id": channelID,
		"message_id": msg.ID,
		"author_id":  msg.AuthorID,
		"author":     author,
		"content":    msg.Content,
	}

	// Copy Extra fields (e.g., slash_command, interaction_token for Discord slash commands)
	for k, v := range msg.Extra {
		data[k] = v
	}

	return &types.Percept{
		ID:        fmt.Sprintf("inbox-%s", msg.ID),
		Source:    source,
		Type:      msgType,
		Intensity: intensity,
		Timestamp: msg.Timestamp,
		Tags:      []string{source},
		Data:      data,
		Features: map[string]any{
			"conversation_id": conversationID,
		},
	}
}

// impulseToPercept converts an impulse-type inbox entry to a Percept
func (msg *InboxMessage) impulseToPercept() *types.Percept {
	// Get intensity from Extra, default based on subtype
	intensity := 0.5
	if i, ok := msg.Extra["intensity"].(float64); ok {
		intensity = i
	}

	// Get impulse type (due, upcoming, recurring, explore, wake)
	impulseType := "unknown"
	if t, ok := msg.Extra["impulse_type"].(string); ok {
		impulseType = t
	}

	// Build percept data with content and priority
	data := map[string]any{
		"content":     msg.Content,
		"description": msg.Content,
		"priority":    msg.Priority,
	}

	// Pass through all Extra fields for reflex access
	// (e.g., calendar reminders have event_title, event_start, meet_link, etc.)
	for k, v := range msg.Extra {
		if k != "intensity" && k != "impulse_type" { // Already handled above
			data[k] = v
		}
	}

	// Also preserve original impulse data if present
	if impulseData, ok := msg.Extra["data"]; ok {
		data["impulse"] = impulseData
	}

	return &types.Percept{
		ID:        msg.ID,
		Source:    "impulse:" + msg.Subtype, // impulse:task, impulse:idea, impulse:system
		Type:      impulseType,              // due, upcoming, recurring, explore, wake
		Intensity: intensity,
		Timestamp: msg.Timestamp,
		Tags:      []string{"internal", msg.Subtype},
		Data:      data,
	}
}

// CleanupProcessed removes processed messages older than maxAge
func (i *Inbox) CleanupProcessed(maxAge time.Duration) int {
	i.mu.Lock()
	defer i.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	cleaned := 0
	for id, msg := range i.messages {
		if msg.Status == "processed" && msg.Timestamp.Before(cutoff) {
			delete(i.messages, id)
			cleaned++
		}
	}
	return cleaned
}
