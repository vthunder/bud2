package effectors

import (
	"fmt"
	"log"
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
	onSend       func(channelID, content string) // called when message is sent (for memory)
	stopChan     chan struct{}
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
	}
}

// SetOnSend sets a callback for when messages are sent (for memory capture)
func (e *DiscordEffector) SetOnSend(callback func(channelID, content string)) {
	e.onSend = callback
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
	if err == nil && e.onSend != nil {
		e.onSend(channelID, content)
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

	return e.session.MessageReactionAdd(channelID, messageID, emoji)
}
