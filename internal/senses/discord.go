package senses

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/vthunder/bud2/internal/types"
)

// DiscordSense listens to Discord and produces percepts
type DiscordSense struct {
	session   *discordgo.Session
	channelID string
	ownerID   string
	botID     string
	onPercept func(*types.Percept)
}

// DiscordConfig holds Discord connection settings
type DiscordConfig struct {
	Token     string
	ChannelID string
	OwnerID   string
}

// NewDiscordSense creates a new Discord sense
func NewDiscordSense(cfg DiscordConfig, onPercept func(*types.Percept)) (*DiscordSense, error) {
	session, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	sense := &DiscordSense{
		session:   session,
		channelID: cfg.ChannelID,
		ownerID:   cfg.OwnerID,
		onPercept: onPercept,
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

	// Convert to percept
	percept := d.messageToPercept(m)

	log.Printf("[discord-sense] Percept: %s (intensity: %.2f, tags: %v)",
		truncate(m.Content, 50), percept.Intensity, percept.Tags)

	// Emit percept
	if d.onPercept != nil {
		d.onPercept(percept)
	}
}

// messageToPercept converts a Discord message to a percept
func (d *DiscordSense) messageToPercept(m *discordgo.MessageCreate) *types.Percept {
	intensity := d.computeIntensity(m)
	tags := d.computeTags(m)

	return &types.Percept{
		ID:        fmt.Sprintf("discord-%s-%s", m.ChannelID, m.ID),
		Source:    "discord",
		Type:      "message",
		Intensity: intensity,
		Timestamp: time.Now(),
		Tags:      tags,
		Data: map[string]any{
			"channel_id":   m.ChannelID,
			"message_id":   m.ID,
			"author_id":    m.Author.ID,
			"author_name":  m.Author.Username,
			"content":      m.Content,
			"is_dm":        m.GuildID == "",
			"mentions_bot": d.mentionsBot(m),
		},
	}
}

// computeIntensity determines signal strength (0.0-1.0)
func (d *DiscordSense) computeIntensity(m *discordgo.MessageCreate) float64 {
	intensity := 0.5 // base intensity

	// Owner messages are high priority
	if m.Author.ID == d.ownerID {
		intensity = 0.9
	}

	// DMs are high priority
	if m.GuildID == "" {
		intensity = max(intensity, 0.8)
	}

	// Bot mentions are high priority
	if d.mentionsBot(m) {
		intensity = max(intensity, 0.85)
	}

	// Urgent keywords boost intensity
	content := strings.ToLower(m.Content)
	urgentKeywords := []string{"urgent", "asap", "help", "error", "broken", "emergency"}
	for _, kw := range urgentKeywords {
		if strings.Contains(content, kw) {
			intensity = max(intensity, 0.8)
			break
		}
	}

	return intensity
}

// computeTags generates tags for the percept
func (d *DiscordSense) computeTags(m *discordgo.MessageCreate) []string {
	var tags []string

	if m.Author.ID == d.ownerID {
		tags = append(tags, "from:owner")
	}

	if m.GuildID == "" {
		tags = append(tags, "dm")
	}

	if d.mentionsBot(m) {
		tags = append(tags, "mention")
	}

	// Check for question
	if strings.Contains(m.Content, "?") {
		tags = append(tags, "question")
	}

	return tags
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

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
