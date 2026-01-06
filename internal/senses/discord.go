package senses

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/vthunder/bud2/internal/memory"
)

// DiscordSense listens to Discord and produces percepts
type DiscordSense struct {
	session   *discordgo.Session
	channelID string
	ownerID   string
	botID     string
	inbox     *memory.Inbox
}

// DiscordConfig holds Discord connection settings
type DiscordConfig struct {
	Token     string
	ChannelID string
	OwnerID   string
}

// NewDiscordSense creates a new Discord sense
func NewDiscordSense(cfg DiscordConfig, inbox *memory.Inbox) (*DiscordSense, error) {
	session, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	sense := &DiscordSense{
		session:   session,
		channelID: cfg.ChannelID,
		ownerID:   cfg.OwnerID,
		inbox:     inbox,
	}

	// Register message handler
	session.AddHandler(sense.handleMessage)

	// We only need message content
	session.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent

	return sense, nil
}

// Start connects to Discord and begins listening
func (d *DiscordSense) Start() error {
	if err := d.session.Open(); err != nil {
		return fmt.Errorf("failed to open Discord connection: %w", err)
	}

	// Get bot's user ID for self-filtering
	d.botID = d.session.State.User.ID
	log.Printf("[discord-sense] Connected as %s", d.session.State.User.Username)

	return nil
}

// Stop disconnects from Discord
func (d *DiscordSense) Stop() error {
	return d.session.Close()
}

// Session returns the underlying Discord session (for sharing with effector)
func (d *DiscordSense) Session() *discordgo.Session {
	return d.session
}

// handleMessage processes incoming Discord messages
func (d *DiscordSense) handleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore messages from self
	if m.Author.ID == d.botID {
		return
	}

	// Only process messages from configured channel (if set)
	if d.channelID != "" && m.ChannelID != d.channelID {
		return
	}

	// Build extra data for intensity/tag computation later
	extra := map[string]any{
		"is_owner":      m.Author.ID == d.ownerID,
		"is_dm":         m.GuildID == "",
		"mentions_bot":  d.mentionsBot(m),
		"has_urgent_kw": d.hasUrgentKeyword(m.Content),
	}

	// Create inbox message
	msg := &memory.InboxMessage{
		ID:        fmt.Sprintf("discord-%s-%s", m.ChannelID, m.ID),
		Content:   m.Content,
		ChannelID: m.ChannelID,
		AuthorID:  m.Author.ID,
		Author:    m.Author.Username,
		Extra:     extra,
	}

	log.Printf("[discord-sense] Message: %s (from: %s)", truncate(m.Content, 50), m.Author.Username)

	// Write to inbox
	if d.inbox != nil {
		d.inbox.Add(msg)
	}
}

// hasUrgentKeyword checks for urgent keywords in content
func (d *DiscordSense) hasUrgentKeyword(content string) bool {
	lc := strings.ToLower(content)
	urgentKeywords := []string{"urgent", "asap", "help", "error", "broken", "emergency"}
	for _, kw := range urgentKeywords {
		if strings.Contains(lc, kw) {
			return true
		}
	}
	return false
}

// mentionsBot checks if the message mentions the bot
func (d *DiscordSense) mentionsBot(m *discordgo.MessageCreate) bool {
	for _, mention := range m.Mentions {
		if mention.ID == d.botID {
			return true
		}
	}
	return false
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
