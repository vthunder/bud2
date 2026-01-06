package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/types"
)

// InboxMessage represents a message written to the inbox
type InboxMessage struct {
	ID        string         `json:"id"`
	Content   string         `json:"content"`
	ChannelID string         `json:"channel_id"`
	AuthorID  string         `json:"author_id,omitempty"`
	Author    string         `json:"author,omitempty"`
	Timestamp time.Time      `json:"timestamp,omitempty"`
	Status    string         `json:"status"` // pending, processed
	Extra     map[string]any `json:"extra,omitempty"`
}

// Inbox manages incoming messages from senses
type Inbox struct {
	mu         sync.RWMutex
	messages   map[string]*InboxMessage
	path       string
	lastOffset int64 // Track file position for incremental reads
}

// NewInbox creates a new inbox
func NewInbox(path string) *Inbox {
	return &Inbox{
		messages: make(map[string]*InboxMessage),
		path:     path,
	}
}

// Add queues a new message
func (i *Inbox) Add(msg *InboxMessage) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if msg.ID == "" {
		msg.ID = fmt.Sprintf("msg-%d", time.Now().UnixNano())
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

// ToPercept converts an InboxMessage to a Percept
func (msg *InboxMessage) ToPercept() *types.Percept {
	channelID := msg.ChannelID
	if channelID == "" {
		channelID = "synthetic"
	}

	// Compute conversation_id from channel + time bucket (5-minute windows)
	// This allows clustering without hardcoding Discord-specific rules
	timeBucket := msg.Timestamp.Unix() / 300 // 5-minute buckets
	conversationID := fmt.Sprintf("%s-%d", channelID, timeBucket)

	return &types.Percept{
		ID:        fmt.Sprintf("inbox-%s", msg.ID),
		Source:    "inbox",
		Type:      "message",
		Intensity: 0.8, // treat inbox messages as high priority
		Timestamp: msg.Timestamp,
		Tags:      []string{"inbox"},
		Data: map[string]any{
			"channel_id":  channelID,
			"message_id":  msg.ID,
			"author_id":   msg.AuthorID,
			"author_name": msg.Author,
			"content":     msg.Content,
		},
		Features: map[string]any{
			"conversation_id": conversationID, // sense-defined clustering feature
		},
	}
}

// Load reads inbox from JSONL file
func (i *Inbox) Load() error {
	i.mu.Lock()
	defer i.mu.Unlock()

	file, err := os.Open(i.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	i.messages = make(map[string]*InboxMessage)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var msg InboxMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue // skip malformed lines
		}
		i.messages[msg.ID] = &msg
	}

	// Track file position for incremental polling
	i.lastOffset, _ = file.Seek(0, io.SeekEnd)

	return scanner.Err()
}

// Save writes inbox to JSONL file
func (i *Inbox) Save() error {
	i.mu.RLock()
	defer i.mu.RUnlock()

	file, err := os.Create(i.path)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, msg := range i.messages {
		data, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		file.Write(data)
		file.WriteString("\n")
	}

	return nil
}

// Append adds a message and appends to file
func (i *Inbox) Append(msg *InboxMessage) error {
	i.Add(msg)

	i.mu.RLock()
	defer i.mu.RUnlock()

	file, err := os.OpenFile(i.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	file.Write(data)
	file.WriteString("\n")

	return nil
}

// Poll checks for new entries in the file (written by external processes)
// Returns the new messages found
func (i *Inbox) Poll() ([]*InboxMessage, error) {
	file, err := os.Open(i.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Seek to where we left off
	i.mu.RLock()
	offset := i.lastOffset
	i.mu.RUnlock()

	if offset > 0 {
		_, err = file.Seek(offset, io.SeekStart)
		if err != nil {
			return nil, err
		}
	}

	// Read new entries
	scanner := bufio.NewScanner(file)
	var newMessages []*InboxMessage

	i.mu.Lock()
	defer i.mu.Unlock()

	for scanner.Scan() {
		var msg InboxMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue // skip malformed lines
		}
		// Only add if not already present (avoid duplicates)
		if _, exists := i.messages[msg.ID]; !exists {
			msg.Status = "pending"
			i.messages[msg.ID] = &msg
			newMessages = append(newMessages, &msg)
		}
	}

	// Update offset to current position
	newOffset, _ := file.Seek(0, io.SeekCurrent)
	i.lastOffset = newOffset

	return newMessages, scanner.Err()
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
