package effectors

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/vthunder/bud2/internal/types"
)

// retryState tracks retry information for an action
type retryState struct {
	attempts     int
	firstFailure time.Time
	nextRetry    time.Time
}

// DiscordEffector sends messages to Discord
type DiscordEffector struct {
	getSession       func() *discordgo.Session // getter to always use current session
	pollInterval     time.Duration
	maxRetryDuration time.Duration // how long to retry before giving up (default 5 min)
	pollFile         func() (int, error)
	getActions       func() []*types.Action
	markComplete     func(id string)
	markFailed       func(id string)
	onSend           func(channelID, content string)
	onAction         func(actionType, channelID, content, source string)
	onError          func(actionID, actionType, errMsg string)           // permanent failure
	onRetry          func(actionID, actionType, errMsg string, attempt int, nextRetry time.Duration) // transient failure, will retry
	stopChan         chan struct{}

	// Retry state tracking
	retryMu     sync.Mutex
	retryStates map[string]*retryState

	// Typing indicator state
	typingMu    sync.Mutex
	typingChans map[string]chan struct{}
}

// DefaultMaxRetryDuration is how long to retry transient failures before giving up
const DefaultMaxRetryDuration = 5 * time.Minute

// NewDiscordEffector creates a Discord effector
// getSession is a getter that returns the current session (supports reconnection/hard reset)
func NewDiscordEffector(
	getSession func() *discordgo.Session,
	pollFile func() (int, error),
	getActions func() []*types.Action,
	markComplete func(id string),
	markFailed func(id string),
) *DiscordEffector {
	return &DiscordEffector{
		getSession:       getSession,
		pollInterval:     100 * time.Millisecond,
		maxRetryDuration: DefaultMaxRetryDuration,
		pollFile:         pollFile,
		getActions:       getActions,
		markComplete:     markComplete,
		markFailed:       markFailed,
		stopChan:         make(chan struct{}),
		retryStates:      make(map[string]*retryState),
		typingChans:      make(map[string]chan struct{}),
	}
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

	now := time.Now()
	actions := e.getActions()
	for _, action := range actions {
		if action.Effector != "discord" {
			continue
		}
		if action.Status != "pending" {
			continue
		}

		// Check if we should retry yet (exponential backoff)
		if !e.shouldRetryNow(action.ID, now) {
			continue
		}

		err := e.executeAction(action)
		if err != nil {
			e.handleActionError(action, err, now)
			continue
		}

		// Success - clean up retry state and mark complete
		e.clearRetryState(action.ID)
		e.markComplete(action.ID)
		log.Printf("[discord-effector] Completed action %s (%s)", action.ID, action.Type)
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

// handleActionError processes an error, implementing exponential backoff for retryable errors
func (e *DiscordEffector) handleActionError(action *types.Action, err error, now time.Time) {
	// Check if this is a non-retryable error (4xx client errors)
	if isNonRetryableError(err) {
		log.Printf("[discord-effector] Action %s failed permanently (non-retryable): %v", action.ID, err)
		e.clearRetryState(action.ID)
		if e.markFailed != nil {
			e.markFailed(action.ID)
		}
		if e.onError != nil {
			e.onError(action.ID, action.Type, err.Error())
		}
		return
	}

	// Retryable error - update retry state
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

	// Check if we've exceeded max retry duration
	elapsed := now.Sub(state.firstFailure)
	if elapsed >= e.maxRetryDuration {
		e.retryMu.Unlock()
		log.Printf("[discord-effector] Action %s failed permanently (max retry duration %v exceeded): %v", action.ID, e.maxRetryDuration, err)
		e.clearRetryState(action.ID)
		if e.markFailed != nil {
			e.markFailed(action.ID)
		}
		if e.onError != nil {
			e.onError(action.ID, action.Type, fmt.Sprintf("gave up after %v: %s", elapsed.Round(time.Second), err.Error()))
		}
		return
	}

	// Calculate next retry with exponential backoff: 1s, 2s, 4s, 8s, 16s, 32s, max 60s
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
		// 4xx errors are client errors - don't retry
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

	_, err := e.getSession().ChannelMessageSend(channelID, content)
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

	err := e.getSession().MessageReactionAdd(channelID, messageID, emoji)
	if err == nil && e.onAction != nil {
		source, _ := action.Payload["source"].(string)
		e.onAction("add_reaction", channelID, emoji, source)
	}
	return err
}

// StartTyping starts showing the typing indicator in a channel.
// The indicator is maintained until StopTyping is called.
func (e *DiscordEffector) StartTyping(channelID string) {
	if channelID == "" || e.getSession() == nil {
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
		if err := e.getSession().ChannelTyping(channelID); err != nil {
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
