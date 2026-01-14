package senses

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/vthunder/bud2/internal/memory"
)

// DefaultMaxDisconnectDuration is how long to allow disconnection before hard reset
const DefaultMaxDisconnectDuration = 10 * time.Minute

// DiscordSense listens to Discord and produces percepts
type DiscordSense struct {
	session   *discordgo.Session
	token     string // stored for hard reset
	channelID string
	ownerID   string
	botID     string
	inbox     *memory.Inbox

	// Connection health tracking
	mu               sync.RWMutex
	connected        bool
	lastConnected    time.Time
	lastDisconnected time.Time
	disconnectCount  int
	hardResetCount   int

	// Health monitor
	stopMonitor       chan struct{}
	maxDisconnectDur  time.Duration
	onProlongedOutage func(duration time.Duration) // callback for prolonged outage
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
		session:          session,
		token:            cfg.Token,
		channelID:        cfg.ChannelID,
		ownerID:          cfg.OwnerID,
		inbox:            inbox,
		maxDisconnectDur: DefaultMaxDisconnectDuration,
	}

	// Register handlers
	session.AddHandler(sense.handleMessage)
	session.AddHandler(sense.handleConnect)
	session.AddHandler(sense.handleDisconnect)
	session.AddHandler(sense.handleResumed)

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

	// Mark as connected (Connect event may not have fired yet)
	d.mu.Lock()
	d.connected = true
	d.lastConnected = time.Now()
	d.mu.Unlock()

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
		"dialogue_act":  classifyDialogueAct(m.Content),
	}

	// Capture reply chain if this is a reply to another message
	if m.MessageReference != nil && m.MessageReference.MessageID != "" {
		extra["reply_to"] = fmt.Sprintf("discord-%s-%s", m.MessageReference.ChannelID, m.MessageReference.MessageID)
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

// classifyDialogueAct performs rule-based dialogue act classification
// Returns: backchannel, question, command, greeting, or statement
func classifyDialogueAct(content string) string {
	content = strings.TrimSpace(content)
	lc := strings.ToLower(content)

	// Backchannel - short acknowledgments
	backchannels := []string{
		"ok", "okay", "k", "yes", "yeah", "yep", "yup", "no", "nope", "nah",
		"thanks", "thx", "ty", "thank you", "cool", "great", "nice", "good",
		"got it", "understood", "i see", "right", "sure", "alright", "uh-huh",
		"mm", "mmm", "mhm", "hm", "hmm", "ah", "oh", "lol", "haha", "heh",
		"👍", "✓", "✔", ":thumbsup:", ":+1:",
	}
	if len(content) < 20 {
		for _, bc := range backchannels {
			if lc == bc || lc == bc+"." || lc == bc+"!" {
				return "backchannel"
			}
		}
	}

	// Questions - ends with ? or starts with question words
	if strings.HasSuffix(content, "?") {
		return "question"
	}
	questionStarters := []string{"what", "when", "where", "who", "why", "how", "can", "could", "would", "will", "is", "are", "do", "does", "did"}
	words := strings.Fields(lc)
	if len(words) > 0 {
		for _, qs := range questionStarters {
			if words[0] == qs {
				return "question"
			}
		}
	}

	// Commands/requests - imperatives
	commandStarters := []string{"please", "can you", "could you", "would you", "show", "tell", "find", "get", "create", "make", "add", "remove", "delete", "run", "check", "help"}
	for _, cs := range commandStarters {
		if strings.HasPrefix(lc, cs) {
			return "command"
		}
	}

	// Greetings
	greetings := []string{"hello", "hi", "hey", "good morning", "good afternoon", "good evening", "bye", "goodbye", "see you", "later", "gn", "good night"}
	for _, g := range greetings {
		if strings.HasPrefix(lc, g) || lc == g {
			return "greeting"
		}
	}

	// Default to statement
	return "statement"
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

// handleConnect is called when the websocket connects
func (d *DiscordSense) handleConnect(s *discordgo.Session, e *discordgo.Connect) {
	d.mu.Lock()
	d.connected = true
	d.lastConnected = time.Now()
	disconnectDuration := time.Duration(0)
	if !d.lastDisconnected.IsZero() {
		disconnectDuration = d.lastConnected.Sub(d.lastDisconnected)
	}
	d.mu.Unlock()

	if disconnectDuration > 0 {
		log.Printf("[discord-sense] Connected (was disconnected for %v)", disconnectDuration.Round(time.Second))
	} else {
		log.Printf("[discord-sense] Connected")
	}
}

// handleDisconnect is called when the websocket disconnects
func (d *DiscordSense) handleDisconnect(s *discordgo.Session, e *discordgo.Disconnect) {
	d.mu.Lock()
	d.connected = false
	d.lastDisconnected = time.Now()
	d.disconnectCount++
	count := d.disconnectCount
	connectedDuration := time.Duration(0)
	if !d.lastConnected.IsZero() {
		connectedDuration = d.lastDisconnected.Sub(d.lastConnected)
	}
	d.mu.Unlock()

	log.Printf("[discord-sense] Disconnected (was connected for %v, total disconnects: %d)",
		connectedDuration.Round(time.Second), count)
}

// handleResumed is called when the session successfully resumes
func (d *DiscordSense) handleResumed(s *discordgo.Session, e *discordgo.Resumed) {
	d.mu.Lock()
	d.connected = true
	d.lastConnected = time.Now()
	d.mu.Unlock()

	log.Printf("[discord-sense] Session resumed")
}

// IsConnected returns whether the Discord connection is currently active
func (d *DiscordSense) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.connected
}

// DisconnectedDuration returns how long we've been disconnected (0 if connected)
func (d *DiscordSense) DisconnectedDuration() time.Duration {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.connected {
		return 0
	}
	if d.lastDisconnected.IsZero() {
		return 0
	}
	return time.Since(d.lastDisconnected)
}

// ConnectionHealth returns connection statistics
func (d *DiscordSense) ConnectionHealth() (connected bool, disconnectCount int, lastConnected, lastDisconnected time.Time) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.connected, d.disconnectCount, d.lastConnected, d.lastDisconnected
}

// SetMaxDisconnectDuration sets how long to tolerate disconnection before hard reset
func (d *DiscordSense) SetMaxDisconnectDuration(dur time.Duration) {
	d.mu.Lock()
	d.maxDisconnectDur = dur
	d.mu.Unlock()
}

// SetOnProlongedOutage sets a callback that fires when disconnected for too long
// The callback receives the duration of the outage. This is called BEFORE hard reset.
func (d *DiscordSense) SetOnProlongedOutage(callback func(duration time.Duration)) {
	d.mu.Lock()
	d.onProlongedOutage = callback
	d.mu.Unlock()
}

// StartHealthMonitor begins monitoring connection health and auto-resets on prolonged outage
func (d *DiscordSense) StartHealthMonitor() {
	d.mu.Lock()
	if d.stopMonitor != nil {
		d.mu.Unlock()
		return // Already running
	}
	d.stopMonitor = make(chan struct{})
	d.mu.Unlock()

	go d.healthMonitorLoop()
	log.Printf("[discord-sense] Health monitor started (max disconnect: %v)", d.maxDisconnectDur)
}

// StopHealthMonitor stops the health monitor
func (d *DiscordSense) StopHealthMonitor() {
	d.mu.Lock()
	if d.stopMonitor != nil {
		close(d.stopMonitor)
		d.stopMonitor = nil
	}
	d.mu.Unlock()
}

func (d *DiscordSense) healthMonitorLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopMonitor:
			return
		case <-ticker.C:
			d.checkConnectionHealth()
		}
	}
}

func (d *DiscordSense) checkConnectionHealth() {
	disconnectDur := d.DisconnectedDuration()
	if disconnectDur == 0 {
		return // Connected, all good
	}

	d.mu.RLock()
	maxDur := d.maxDisconnectDur
	callback := d.onProlongedOutage
	d.mu.RUnlock()

	if disconnectDur >= maxDur {
		log.Printf("[discord-sense] Disconnected for %v (max %v), triggering hard reset",
			disconnectDur.Round(time.Second), maxDur)

		if callback != nil {
			callback(disconnectDur)
		}

		if err := d.HardReset(); err != nil {
			log.Printf("[discord-sense] Hard reset failed: %v", err)
		}
	}
}

// HardReset completely closes and recreates the Discord session
// Use this when the normal reconnection mechanism has failed
func (d *DiscordSense) HardReset() error {
	log.Printf("[discord-sense] Performing hard reset...")

	// Close existing session
	if d.session != nil {
		d.session.Close()
	}

	// Create new session
	session, err := discordgo.New("Bot " + d.token)
	if err != nil {
		return fmt.Errorf("failed to create new Discord session: %w", err)
	}

	// Re-register handlers
	session.AddHandler(d.handleMessage)
	session.AddHandler(d.handleConnect)
	session.AddHandler(d.handleDisconnect)
	session.AddHandler(d.handleResumed)
	session.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent

	// Open connection
	if err := session.Open(); err != nil {
		return fmt.Errorf("failed to open new Discord connection: %w", err)
	}

	// Update state
	d.mu.Lock()
	d.session = session
	d.botID = session.State.User.ID
	d.connected = true
	d.lastConnected = time.Now()
	d.hardResetCount++
	resetCount := d.hardResetCount
	d.mu.Unlock()

	log.Printf("[discord-sense] Hard reset successful (total resets: %d)", resetCount)
	return nil
}
