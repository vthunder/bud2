package effectors

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/vthunder/bud2/internal/types"
)

// MaxDiscordMessageLength is Discord's maximum message length
const MaxDiscordMessageLength = 2000

// retryState tracks retry information for an action
type retryState struct {
	attempts     int
	firstFailure time.Time
	nextRetry    time.Time
}

// PendingInteraction contains info needed to follow up on a slash command
type PendingInteraction struct {
	Token string
	AppID string
}

// DiscordEffector sends messages to Discord
type DiscordEffector struct {
	getSession       func() *discordgo.Session
	pollInterval     time.Duration
	maxRetryDuration time.Duration
	pollFile         func() ([]*types.Action, error)
	onSend           func(channelID, content string)
	onAction         func(actionType, channelID, content, source string)
	onError          func(actionID, actionType, errMsg string)
	onRetry          func(actionID, actionType, errMsg string, attempt int, nextRetry time.Duration)
	stopChan         chan struct{}

	// Pending slash command interaction callback
	getPendingInteraction func(channelID string) *PendingInteraction

	// Pending actions (from poll or direct submit) awaiting execution
	pendingMu sync.Mutex
	pending   []*types.Action

	// Retry state tracking
	retryMu     sync.Mutex
	retryStates map[string]*retryState

	// Typing indicator state
	typingMu    sync.Mutex
	typingChans map[string]chan struct{}
}

// DefaultMaxRetryDuration is how long to retry transient failures before giving up
const DefaultMaxRetryDuration = 5 * time.Minute

// NewDiscordEffector creates a Discord effector.
// pollFile is called each tick to get new actions from the outbox file.
func NewDiscordEffector(
	getSession func() *discordgo.Session,
	pollFile func() ([]*types.Action, error),
) *DiscordEffector {
	return &DiscordEffector{
		getSession:       getSession,
		pollInterval:     100 * time.Millisecond,
		maxRetryDuration: DefaultMaxRetryDuration,
		pollFile:         pollFile,
		stopChan:         make(chan struct{}),
		retryStates:      make(map[string]*retryState),
		typingChans:      make(map[string]chan struct{}),
	}
}

// Submit adds an action directly (for in-process callers like reflexes).
func (e *DiscordEffector) Submit(action *types.Action) {
	e.pendingMu.Lock()
	e.pending = append(e.pending, action)
	e.pendingMu.Unlock()
}

// SetMaxRetryDuration configures how long to retry transient failures
func (e *DiscordEffector) SetMaxRetryDuration(d time.Duration) {
	e.maxRetryDuration = d
}

// SetOnSend sets a callback for when messages are sent (for memory capture)
func (e *DiscordEffector) SetOnSend(callback func(channelID, content string)) {
	e.onSend = callback
}

// SetOnAction sets a callback for when actions are executed (for activity logging)
func (e *DiscordEffector) SetOnAction(callback func(actionType, channelID, content, source string)) {
	e.onAction = callback
}

// SetOnError sets a callback for when actions fail permanently (for activity logging)
func (e *DiscordEffector) SetOnError(callback func(actionID, actionType, errMsg string)) {
	e.onError = callback
}

// SetOnRetry sets a callback for when actions fail transiently and will be retried
func (e *DiscordEffector) SetOnRetry(callback func(actionID, actionType, errMsg string, attempt int, nextRetry time.Duration)) {
	e.onRetry = callback
}

// SetPendingInteractionCallback sets the callback for retrieving pending slash command interactions
func (e *DiscordEffector) SetPendingInteractionCallback(callback func(channelID string) *PendingInteraction) {
	e.getPendingInteraction = callback
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
		actions, err := e.pollFile()
		if err != nil {
			log.Printf("[discord-effector] Poll error: %v", err)
		} else if len(actions) > 0 {
			log.Printf("[discord-effector] Found %d new actions from file", len(actions))
			e.pendingMu.Lock()
			e.pending = append(e.pending, actions...)
			e.pendingMu.Unlock()
		}
	}

	// Take a snapshot of pending actions to process
	e.pendingMu.Lock()
	toProcess := e.pending
	e.pending = nil
	e.pendingMu.Unlock()

	if len(toProcess) == 0 {
		return
	}

	now := time.Now()
	var stillPending []*types.Action

	for _, action := range toProcess {
		if action.Effector != "discord" {
			continue
		}

		// Check if we should retry yet (exponential backoff)
		if !e.shouldRetryNow(action.ID, now) {
			stillPending = append(stillPending, action)
			continue
		}

		err := e.executeAction(action)
		if err != nil {
			if e.handleActionError(action, err, now) {
				// Action will be retried — keep it pending
				stillPending = append(stillPending, action)
			}
			continue
		}

		// Success
		e.clearRetryState(action.ID)
		log.Printf("[discord-effector] Completed action %s (%s)", action.ID, action.Type)
	}

	// Put back actions that need retry
	if len(stillPending) > 0 {
		e.pendingMu.Lock()
		e.pending = append(stillPending, e.pending...)
		e.pendingMu.Unlock()
	}
}

// shouldRetryNow checks if enough time has passed for the next retry attempt
func (e *DiscordEffector) shouldRetryNow(actionID string, now time.Time) bool {
	e.retryMu.Lock()
	defer e.retryMu.Unlock()

	state, exists := e.retryStates[actionID]
	if !exists {
		return true // First attempt
	}
	return now.After(state.nextRetry)
}

// handleActionError processes an error. Returns true if the action should be retried.
func (e *DiscordEffector) handleActionError(action *types.Action, err error, now time.Time) bool {
	// Non-retryable (4xx client errors) — drop it
	if isNonRetryableError(err) {
		log.Printf("[discord-effector] Action %s failed permanently (non-retryable): %v", action.ID, err)
		e.clearRetryState(action.ID)
		if e.onError != nil {
			e.onError(action.ID, action.Type, err.Error())
		}
		return false
	}

	// Retryable — update retry state
	e.retryMu.Lock()
	state, exists := e.retryStates[action.ID]
	if !exists {
		state = &retryState{
			attempts:     0,
			firstFailure: now,
		}
		e.retryStates[action.ID] = state
	}
	state.attempts++

	// Exceeded max retry duration — give up
	elapsed := now.Sub(state.firstFailure)
	if elapsed >= e.maxRetryDuration {
		e.retryMu.Unlock()
		log.Printf("[discord-effector] Action %s failed permanently (max retry duration %v exceeded): %v", action.ID, e.maxRetryDuration, err)
		e.clearRetryState(action.ID)
		if e.onError != nil {
			e.onError(action.ID, action.Type, fmt.Sprintf("gave up after %v: %s", elapsed.Round(time.Second), err.Error()))
		}
		return false
	}

	// Exponential backoff: 1s, 2s, 4s, 8s, 16s, 32s, max 60s
	backoff := time.Duration(1<<uint(state.attempts-1)) * time.Second
	if backoff > 60*time.Second {
		backoff = 60 * time.Second
	}
	state.nextRetry = now.Add(backoff)
	attempt := state.attempts
	e.retryMu.Unlock()

	log.Printf("[discord-effector] Action %s failed (attempt %d, retry in %v): %v", action.ID, attempt, backoff, err)
	if e.onRetry != nil {
		e.onRetry(action.ID, action.Type, err.Error(), attempt, backoff)
	}
	return true
}

// clearRetryState removes retry tracking for an action
func (e *DiscordEffector) clearRetryState(actionID string) {
	e.retryMu.Lock()
	delete(e.retryStates, actionID)
	e.retryMu.Unlock()
}

// isNonRetryableError checks if an error is a client error (4xx) that shouldn't be retried
func isNonRetryableError(err error) bool {
	if restErr, ok := err.(*discordgo.RESTError); ok {
		if restErr.Response != nil && restErr.Response.StatusCode >= 400 && restErr.Response.StatusCode < 500 {
			return true
		}
	}
	return false
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

	// Check for pending slash command interaction (needs followup response instead of regular message)
	if e.getPendingInteraction != nil {
		if interaction := e.getPendingInteraction(channelID); interaction != nil {
			return e.sendInteractionFollowup(interaction, content)
		}
	}

	// Chunk message if too long
	chunks := chunkMessage(content, MaxDiscordMessageLength)

	for i, chunk := range chunks {
		_, err := e.getSession().ChannelMessageSend(channelID, chunk)
		if err != nil {
			return fmt.Errorf("failed to send chunk %d/%d: %w", i+1, len(chunks), err)
		}

		// Small delay between chunks to maintain order
		if i < len(chunks)-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Callbacks use full content
	if e.onSend != nil {
		e.onSend(channelID, content)
	}
	if e.onAction != nil {
		source, _ := action.Payload["source"].(string)
		e.onAction("send_message", channelID, content, source)
	}
	return nil
}

// sendInteractionFollowup edits the deferred response for a slash command
func (e *DiscordEffector) sendInteractionFollowup(interaction *PendingInteraction, content string) error {
	session := e.getSession()

	chunks := chunkMessage(content, MaxDiscordMessageLength)

	// First chunk edits the original deferred response
	_, err := session.InteractionResponseEdit(&discordgo.Interaction{
		AppID: interaction.AppID,
		Token: interaction.Token,
	}, &discordgo.WebhookEdit{
		Content: &chunks[0],
	})
	if err != nil {
		return fmt.Errorf("failed to edit interaction response: %w", err)
	}
	log.Printf("[discord-effector] Edited interaction response (followup)")

	// Additional chunks sent as followup messages
	for i := 1; i < len(chunks); i++ {
		_, err := session.FollowupMessageCreate(&discordgo.Interaction{
			AppID: interaction.AppID,
			Token: interaction.Token,
		}, true, &discordgo.WebhookParams{
			Content: chunks[i],
		})
		if err != nil {
			return fmt.Errorf("failed to send followup chunk %d/%d: %w", i+1, len(chunks), err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if e.onSend != nil {
		e.onSend("", content)
	}
	if e.onAction != nil {
		e.onAction("interaction_followup", "", content, "")
	}
	return nil
}

// chunkMessage splits a message into chunks that fit within maxLen.
// It tries to split on paragraph boundaries, then line boundaries, then word boundaries.
func chunkMessage(content string, maxLen int) []string {
	if len(content) <= maxLen {
		return []string{content}
	}

	var chunks []string
	remaining := content

	for len(remaining) > 0 {
		if len(remaining) <= maxLen {
			chunks = append(chunks, remaining)
			break
		}

		splitAt := findSplitPoint(remaining, maxLen)
		chunks = append(chunks, strings.TrimRight(remaining[:splitAt], " \n"))
		remaining = strings.TrimLeft(remaining[splitAt:], " \n")
	}

	return chunks
}

// findSplitPoint finds the best place to split content within maxLen.
func findSplitPoint(content string, maxLen int) int {
	if len(content) <= maxLen {
		return len(content)
	}

	searchArea := content[:maxLen]

	if idx := strings.LastIndex(searchArea, "\n\n"); idx > maxLen/2 {
		return idx + 2
	}
	if idx := strings.LastIndex(searchArea, "\n"); idx > maxLen/2 {
		return idx + 1
	}
	if idx := strings.LastIndex(searchArea, " "); idx > maxLen/2 {
		return idx + 1
	}
	return maxLen
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

	err := e.getSession().MessageReactionAdd(channelID, messageID, emoji)
	if err == nil && e.onAction != nil {
		source, _ := action.Payload["source"].(string)
		e.onAction("add_reaction", channelID, emoji, source)
	}
	return err
}

// StartTyping starts showing the typing indicator in a channel.
func (e *DiscordEffector) StartTyping(channelID string) {
	if channelID == "" || e.getSession() == nil {
		return
	}

	// Only start typing for valid Discord snowflake IDs (numeric strings)
	if _, err := strconv.ParseUint(channelID, 10, 64); err != nil {
		return
	}

	e.typingMu.Lock()
	defer e.typingMu.Unlock()

	if _, exists := e.typingChans[channelID]; exists {
		return
	}

	stopChan := make(chan struct{})
	e.typingChans[channelID] = stopChan

	go func() {
		if err := e.getSession().ChannelTyping(channelID); err != nil {
			log.Printf("[discord-effector] Failed to start typing: %v", err)
			return
		}
		log.Printf("[discord-effector] Started typing in channel %s", channelID)

		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				log.Printf("[discord-effector] Stopped typing in channel %s", channelID)
				return
			case <-ticker.C:
				if err := e.getSession().ChannelTyping(channelID); err != nil {
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
