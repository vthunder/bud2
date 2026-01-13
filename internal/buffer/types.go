package buffer

import "time"

// DialogueAct represents the pragmatic function of an utterance
type DialogueAct string

const (
	ActBackchannel DialogueAct = "backchannel" // "yes", "ok", "uh-huh" - acknowledgment
	ActQuestion    DialogueAct = "question"    // interrogative
	ActStatement   DialogueAct = "statement"   // declarative
	ActCommand     DialogueAct = "command"     // imperative/request
	ActGreeting    DialogueAct = "greeting"    // social opening/closing
	ActUnknown     DialogueAct = ""            // not classified
)

// Entry represents a single message in the conversation buffer
type Entry struct {
	ID          string      `json:"id"`
	Author      string      `json:"author"`
	AuthorID    string      `json:"author_id"`
	Content     string      `json:"content"`
	Timestamp   time.Time   `json:"timestamp"`
	ChannelID   string      `json:"channel_id"`
	DialogueAct DialogueAct `json:"dialogue_act,omitempty"`
	ReplyTo     string      `json:"reply_to,omitempty"` // ID of message being replied to
	TokenCount  int         `json:"token_count"`        // estimated token count
}

// Scope identifies what conversation this buffer belongs to
type Scope struct {
	Type string `json:"type"` // "channel" or "focus"
	ID   string `json:"id"`   // channel ID or focus UUID
}

// ScopeChannel creates a channel-scoped buffer identifier
func ScopeChannel(channelID string) Scope {
	return Scope{Type: "channel", ID: channelID}
}

// ScopeFocus creates a focus-scoped buffer identifier
func ScopeFocus(focusID string) Scope {
	return Scope{Type: "focus", ID: focusID}
}

// String returns a string representation of the scope
func (s Scope) String() string {
	return s.Type + ":" + s.ID
}

// BufferState represents the persistent state of a conversation buffer
type BufferState struct {
	Scope      Scope     `json:"scope"`
	RawEntries []Entry   `json:"raw_entries"`  // recent raw messages
	Summary    string    `json:"summary"`      // compressed older context
	TokenCount int       `json:"token_count"`  // total tokens in raw
	UpdatedAt  time.Time `json:"updated_at"`
}
