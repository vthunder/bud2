package effectors

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/vthunder/bud2/internal/types"
)

// DiscordEffector sends messages to Discord
type DiscordEffector struct {
	session      *discordgo.Session
	pollInterval time.Duration
	pollFile     func() (int, error) // Poll for new actions from file (MCP writes)
	getActions   func() []*types.Action
	markComplete func(id string)
	onSend       func(channelID, content string)                   // called when message is sent (for memory)
	onAction     func(actionType, channelID, content, source string) // called when action is executed (for activity log)
	stopChan     chan struct{}

	// Typing indicator state
	typingMu    sync.Mutex
	typingChans map[string]chan struct{} // channel ID -> stop channel
}

// NewDiscordEffector creates a Discord effector
// It shares the session with the sense (or creates its own)
func NewDiscordEffector(
	session *discordgo.Session,
	pollFile func() (int, error),
	getActions func() []*types.Action,
	markComplete func(id string),
) *DiscordEffector {
	return &DiscordEffector{
		session:      session,
		pollInterval: 100 * time.Millisecond,
		pollFile:     pollFile,
		getActions:   getActions,
		markComplete: markComplete,
		stopChan:     make(chan struct{}),
		typingChans:  make(map[string]chan struct{}),
	}
}

// SetOnSend sets a callback for when messages are sent (for memory capture)
func (e *DiscordEffector) SetOnSend(callback func(channelID, content string)) {
	e.onSend = callback
}

// SetOnAction sets a callback for when actions are executed (for activity logging)
func (e *DiscordEffector) SetOnAction(callback func(actionType, channelID, content, source string)) {
	e.onAction = callback
}

// Start begins polling the outbox for actions
func (e *DiscordEffector) Start() {
	go e.pollLoop()
	log.Println("[discord-effector] Started")
}

// Stop halts the effector
func (e *DiscordEffector) Stop() {
	close(e.stopChan)
}

func (e *DiscordEffector) pollLoop() {
	ticker := time.NewTicker(e.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-e.stopChan:
			return
		case <-ticker.C:
			e.processActions()
		}
	}
}

func (e *DiscordEffector) processActions() {
	// Poll file for new actions (written by MCP server)
	if e.pollFile != nil {
		newCount, err := e.pollFile()
		if err != nil {
			log.Printf("[discord-effector] Poll error: %v", err)
		} else if newCount > 0 {
			log.Printf("[discord-effector] Found %d new actions from file", newCount)
		}
	}

	actions := e.getActions()
	for _, action := range actions {
		if action.Effector != "discord" {
			continue
		}
		if action.Status != "pending" {
			continue
		}

		err := e.executeAction(action)
		if err != nil {
			log.Printf("[discord-effector] Failed action %s: %v", action.ID, err)
			// TODO: mark as failed, retry logic
			continue
		}

		e.markComplete(action.ID)
		log.Printf("[discord-effector] Completed action %s (%s)", action.ID, action.Type)
	}
}

func (e *DiscordEffector) executeAction(action *types.Action) error {
	switch action.Type {
	case "send_message":
		return e.sendMessage(action)
	case "add_reaction":
		return e.addReaction(action)
	default:
		return fmt.Errorf("unknown action type: %s", action.Type)
	}
}

func (e *DiscordEffector) sendMessage(action *types.Action) error {
	channelID, ok := action.Payload["channel_id"].(string)
	if !ok {
		return fmt.Errorf("missing channel_id")
	}

	content, ok := action.Payload["content"].(string)
	if !ok {
		return fmt.Errorf("missing content")
	}

	_, err := e.session.ChannelMessageSend(channelID, content)
	if err == nil {
		if e.onSend != nil {
			e.onSend(channelID, content)
		}
		if e.onAction != nil {
			source, _ := action.Payload["source"].(string)
			e.onAction("send_message", channelID, content, source)
		}
	}
	return err
}

func (e *DiscordEffector) addReaction(action *types.Action) error {
	channelID, ok := action.Payload["channel_id"].(string)
	if !ok {
		return fmt.Errorf("missing channel_id")
	}

	messageID, ok := action.Payload["message_id"].(string)
	if !ok {
		return fmt.Errorf("missing message_id")
	}

	emoji, ok := action.Payload["emoji"].(string)
	if !ok {
		return fmt.Errorf("missing emoji")
	}

	err := e.session.MessageReactionAdd(channelID, messageID, emoji)
	if err == nil && e.onAction != nil {
		source, _ := action.Payload["source"].(string)
		e.onAction("add_reaction", channelID, emoji, source)
	}
	return err
}

// StartTyping starts showing the typing indicator in a channel.
// The indicator is maintained until StopTyping is called.
func (e *DiscordEffector) StartTyping(channelID string) {
	if channelID == "" || e.session == nil {
		return
	}

	e.typingMu.Lock()
	defer e.typingMu.Unlock()

	// Already typing in this channel
	if _, exists := e.typingChans[channelID]; exists {
		return
	}

	stopChan := make(chan struct{})
	e.typingChans[channelID] = stopChan

	// Start typing indicator goroutine
	go func() {
		// Send initial typing indicator
		if err := e.session.ChannelTyping(channelID); err != nil {
			log.Printf("[discord-effector] Failed to start typing: %v", err)
			return
		}
		log.Printf("[discord-effector] Started typing in channel %s", channelID)

		// Discord typing indicators expire after ~10 seconds, so refresh every 8 seconds
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				log.Printf("[discord-effector] Stopped typing in channel %s", channelID)
				return
			case <-ticker.C:
				if err := e.session.ChannelTyping(channelID); err != nil {
					log.Printf("[discord-effector] Failed to refresh typing: %v", err)
					return
				}
			}
		}
	}()
}

// StopTyping stops the typing indicator in a channel
func (e *DiscordEffector) StopTyping(channelID string) {
	if channelID == "" {
		return
	}

	e.typingMu.Lock()
	defer e.typingMu.Unlock()

	if stopChan, exists := e.typingChans[channelID]; exists {
		close(stopChan)
		delete(e.typingChans, channelID)
	}
}

// StopAllTyping stops all typing indicators (used during shutdown)
func (e *DiscordEffector) StopAllTyping() {
	e.typingMu.Lock()
	defer e.typingMu.Unlock()

	for channelID, stopChan := range e.typingChans {
		close(stopChan)
		delete(e.typingChans, channelID)
		log.Printf("[discord-effector] Stopped typing in channel %s (shutdown)", channelID)
	}
}
