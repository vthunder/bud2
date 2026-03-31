package effectors

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/vthunder/bud2/internal/logging"
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
		logging.Debug("discord-effector", "Completed action %s (%s)", action.ID, action.Type)
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
	case "send_file":
		return e.sendFile(action)
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
// It first converts markdown tables to code blocks, then splits with awareness
// of code fence boundaries to avoid breaking them mid-block.
func chunkMessage(content string, maxLen int) []string {
	content = convertMarkdownTables(content)

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

		chunk, rest, openFence := splitCodeFenceAware(remaining, maxLen)

		if openFence != "" {
			// Split fell mid-fence: close the block before the cut, reopen it after.
			chunk = chunk + "\n```"
			rest = openFence + "\n" + rest
		}

		chunks = append(chunks, strings.TrimRight(chunk, " \n"))
		remaining = strings.TrimLeft(rest, " \n")
	}

	return chunks
}

// splitCodeFenceAware finds the best split point within maxLen, preferring
// clean code fence boundaries. Returns the chunk to send, the remaining
// content, and an openFence string. When openFence is non-empty the split fell
// inside a code block; the caller should append ` ``` ` to chunk and prepend
// openFence (e.g. "```go") to rest.
func splitCodeFenceAware(content string, maxLen int) (chunk, rest, openFence string) {
	if len(content) <= maxLen {
		return content, "", ""
	}

	inFence := false
	currentLang := ""
	lastCleanFenceEnd := -1 // byte pos after last closing-fence line within preferred range

	fenceRangeStart := maxLen - 300
	if fenceRangeStart < 0 {
		fenceRangeStart = 0
	}

	pos := 0
	for pos < maxLen {
		newlineOff := strings.IndexByte(content[pos:], '\n')
		var lineEnd, afterLine int
		if newlineOff < 0 {
			lineEnd = len(content)
			afterLine = len(content)
		} else {
			lineEnd = pos + newlineOff
			afterLine = lineEnd + 1
		}

		line := content[pos:lineEnd]
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			lang := strings.TrimPrefix(trimmed, "```")
			if inFence {
				// Closing fence — record as a clean split boundary if in range.
				inFence = false
				currentLang = ""
				if afterLine <= maxLen && afterLine >= fenceRangeStart {
					lastCleanFenceEnd = afterLine
				}
			} else {
				// Opening fence.
				inFence = true
				currentLang = lang
			}
		}

		if afterLine >= maxLen {
			break
		}
		pos = afterLine
	}

	// Prefer a clean fence boundary (split right after a closing ```).
	if lastCleanFenceEnd > 0 {
		return content[:lastCleanFenceEnd], content[lastCleanFenceEnd:], ""
	}

	// Fall back to content-aware split.
	if inFence {
		// Reserve 4 bytes for the "\n```" we'll append to close the fence.
		adjustedMax := maxLen - 4
		if adjustedMax < 1 {
			adjustedMax = 1
		}
		splitAt := findSplitPoint(content, adjustedMax)
		return content[:splitAt], content[splitAt:], "```" + currentLang
	}
	splitAt := findSplitPoint(content, maxLen)
	return content[:splitAt], content[splitAt:], ""
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

// isTableHeaderRow reports whether a line looks like a markdown table header.
func isTableHeaderRow(line string) bool {
	t := strings.TrimSpace(line)
	return len(t) > 1 && strings.HasPrefix(t, "|") && strings.HasSuffix(t, "|")
}

// isTableSeparatorRow reports whether a line looks like a markdown table separator.
func isTableSeparatorRow(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "|") && strings.Contains(t, "---")
}

// isTableDataRow reports whether a line looks like a markdown table data row.
func isTableDataRow(line string) bool {
	t := strings.TrimSpace(line)
	return len(t) > 1 && strings.HasPrefix(t, "|")
}

// parseTableCells splits a markdown table row into trimmed cell strings.
func parseTableCells(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

// convertTable converts a slice of markdown table lines to a ``` code block
// with fixed-width aligned columns.
func convertTable(tableLines []string) []string {
	if len(tableLines) < 3 {
		return tableLines
	}

	headers := parseTableCells(tableLines[0])
	// tableLines[1] is the separator row — skip it.
	numCols := len(headers)

	var dataRows [][]string
	for _, line := range tableLines[2:] {
		dataRows = append(dataRows, parseTableCells(line))
	}

	// Compute per-column width.
	widths := make([]int, numCols)
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range dataRows {
		for i := 0; i < numCols && i < len(row); i++ {
			if len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}

	out := []string{"```"}
	out = append(out, fmtTableRow(headers, widths))
	out = append(out, fmtTableSep(widths))
	for _, row := range dataRows {
		out = append(out, fmtTableRow(row, widths))
	}
	out = append(out, "```")
	return out
}

func fmtTableRow(cells []string, widths []int) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		var cell string
		if i < len(cells) {
			cell = cells[i]
		}
		parts[i] = cell + strings.Repeat(" ", w-len(cell))
	}
	return strings.Join(parts, "  ")
}

func fmtTableSep(widths []int) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		if w < 1 {
			w = 1
		}
		parts[i] = strings.Repeat("-", w)
	}
	return strings.Join(parts, "  ")
}

// convertMarkdownTables replaces markdown table blocks with fixed-width text
// inside ``` code blocks. Tables inside existing code fences are left unchanged.
func convertMarkdownTables(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inFence := false
	i := 0

	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Track code fence state — skip table conversion inside fences.
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			result = append(result, line)
			i++
			continue
		}

		// Look for table: header + separator + ≥1 data row, outside any fence.
		if !inFence && isTableHeaderRow(line) && i+1 < len(lines) && isTableSeparatorRow(lines[i+1]) {
			tableLines := []string{line, lines[i+1]}
			j := i + 2
			for j < len(lines) && isTableDataRow(lines[j]) {
				tableLines = append(tableLines, lines[j])
				j++
			}

			if j > i+2 { // at least one data row found
				result = append(result, convertTable(tableLines)...)
				i = j
				continue
			}
		}

		result = append(result, line)
		i++
	}

	return strings.Join(result, "\n")
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

func (e *DiscordEffector) sendFile(action *types.Action) error {
	channelID, ok := action.Payload["channel_id"].(string)
	if !ok {
		return fmt.Errorf("missing channel_id")
	}

	filePath, ok := action.Payload["file_path"].(string)
	if !ok {
		return fmt.Errorf("missing file_path")
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %q: %w", filePath, err)
	}
	defer f.Close()

	name := filepath.Base(filePath)
	message, _ := action.Payload["message"].(string)

	var ms *discordgo.MessageSend
	if message != "" {
		ms = &discordgo.MessageSend{
			Content: message,
			Files: []*discordgo.File{
				{Name: name, Reader: f},
			},
		}
	} else {
		ms = &discordgo.MessageSend{
			Files: []*discordgo.File{
				{Name: name, Reader: io.Reader(f)},
			},
		}
	}

	_, err = e.getSession().ChannelMessageSendComplex(channelID, ms)
	if err != nil {
		return fmt.Errorf("failed to send file: %w", err)
	}

	if e.onAction != nil {
		source, _ := action.Payload["source"].(string)
		e.onAction("send_file", channelID, name, source)
	}
	return nil
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
			logging.Debug("discord-effector", "Failed to start typing: %v", err)
			return
		}
		logging.Debug("discord-effector", "Started typing")

		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				logging.Debug("discord-effector", "Stopped typing")
				return
			case <-ticker.C:
				if err := e.getSession().ChannelTyping(channelID); err != nil {
					logging.Debug("discord-effector", "Failed to refresh typing: %v", err)
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

	for _, stopChan := range e.typingChans {
		close(stopChan)
	}
	e.typingChans = make(map[string]chan struct{})
}
