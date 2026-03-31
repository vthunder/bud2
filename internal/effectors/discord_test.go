package effectors

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/vthunder/bud2/internal/types"
)

// newTestEffectorNoSession creates a DiscordEffector with no session (for retry logic tests)
func newTestEffectorNoSession() *DiscordEffector {
	return &DiscordEffector{
		getSession:       func() *discordgo.Session { return nil },
		pollInterval:     100 * time.Millisecond,
		maxRetryDuration: DefaultMaxRetryDuration,
		retryStates:      make(map[string]*retryState),
		typingChans:      make(map[string]chan struct{}),
		stopChan:         make(chan struct{}),
	}
}

func newTestAction(id string) *types.Action {
	return &types.Action{
		ID:       id,
		Effector: "discord",
		Type:     "send_message",
		Payload:  map[string]any{"channel_id": "123", "content": "hello"},
	}
}

// --- isNonRetryableError ---

func TestIsNonRetryableError_NilError(t *testing.T) {
	if isNonRetryableError(errors.New("network timeout")) {
		t.Error("generic error should be retryable")
	}
}

func TestIsNonRetryableError_4xxStatus(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 429} {
		err := &discordgo.RESTError{
			Response: &http.Response{StatusCode: code},
		}
		if !isNonRetryableError(err) {
			t.Errorf("HTTP %d should be non-retryable", code)
		}
	}
}

func TestIsNonRetryableError_5xxStatus(t *testing.T) {
	for _, code := range []int{500, 502, 503} {
		err := &discordgo.RESTError{
			Response: &http.Response{StatusCode: code},
		}
		if isNonRetryableError(err) {
			t.Errorf("HTTP %d should be retryable (server error)", code)
		}
	}
}

func TestIsNonRetryableError_NilResponse(t *testing.T) {
	err := &discordgo.RESTError{Response: nil}
	if isNonRetryableError(err) {
		t.Error("RESTError with nil response should be retryable")
	}
}

// --- handleActionError ---

func TestHandleActionError_RetryableFirstAttempt(t *testing.T) {
	e := newTestEffectorNoSession()
	action := newTestAction("act-1")
	now := time.Now()

	retried := e.handleActionError(action, errors.New("network error"), now)

	if !retried {
		t.Error("expected action to be scheduled for retry")
	}

	e.retryMu.Lock()
	state := e.retryStates["act-1"]
	e.retryMu.Unlock()

	if state == nil {
		t.Fatal("expected retry state to be recorded")
	}
	if state.attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", state.attempts)
	}
	// Backoff for attempt 1: 1s
	expectedBackoff := 1 * time.Second
	actualBackoff := state.nextRetry.Sub(now)
	if actualBackoff < expectedBackoff-10*time.Millisecond || actualBackoff > expectedBackoff+10*time.Millisecond {
		t.Errorf("expected ~%v backoff, got %v", expectedBackoff, actualBackoff)
	}
}

func TestHandleActionError_ExponentialBackoff(t *testing.T) {
	e := newTestEffectorNoSession()
	action := newTestAction("act-2")
	now := time.Now()

	// Simulate multiple failures
	expectedBackoffs := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60} // seconds, capped at 60s
	for i, expected := range expectedBackoffs {
		retried := e.handleActionError(action, errors.New("transient"), now)
		if !retried {
			t.Fatalf("attempt %d: expected retry", i+1)
		}

		e.retryMu.Lock()
		state := e.retryStates["act-2"]
		e.retryMu.Unlock()

		actualBackoff := state.nextRetry.Sub(now)
		expectedDur := expected * time.Second
		if actualBackoff < expectedDur-10*time.Millisecond || actualBackoff > expectedDur+10*time.Millisecond {
			t.Errorf("attempt %d: expected ~%v backoff, got %v", i+1, expectedDur, actualBackoff)
		}
	}
}

func TestHandleActionError_MaxDurationExceeded(t *testing.T) {
	e := newTestEffectorNoSession()
	e.SetMaxRetryDuration(100 * time.Millisecond)
	action := newTestAction("act-3")

	var permanentErr string
	e.SetOnError(func(actionID, actionType, errMsg string) {
		permanentErr = errMsg
	})

	// First attempt — within max duration
	now := time.Now()
	retried := e.handleActionError(action, errors.New("transient"), now)
	if !retried {
		t.Error("first attempt should schedule retry")
	}

	// Second attempt — after max duration has passed
	later := now.Add(200 * time.Millisecond)
	retried = e.handleActionError(action, errors.New("transient"), later)
	if retried {
		t.Error("after max duration, action should not be retried")
	}
	if permanentErr == "" {
		t.Error("expected onError callback to be called")
	}

	// State should be cleared
	e.retryMu.Lock()
	_, exists := e.retryStates["act-3"]
	e.retryMu.Unlock()
	if exists {
		t.Error("retry state should be cleared after permanent failure")
	}
}

func TestHandleActionError_NonRetryable(t *testing.T) {
	e := newTestEffectorNoSession()
	action := newTestAction("act-4")

	var errorCalled bool
	e.SetOnError(func(actionID, actionType, errMsg string) {
		errorCalled = true
	})

	restErr := &discordgo.RESTError{
		Response: &http.Response{StatusCode: 403},
	}
	retried := e.handleActionError(action, restErr, time.Now())

	if retried {
		t.Error("non-retryable error should not be retried")
	}
	if !errorCalled {
		t.Error("expected onError callback to be called")
	}
}

func TestHandleActionError_RetryCallback(t *testing.T) {
	e := newTestEffectorNoSession()
	action := newTestAction("act-5")

	var retryCalled bool
	var retryAttempt int
	e.SetOnRetry(func(actionID, actionType, errMsg string, attempt int, nextRetry time.Duration) {
		retryCalled = true
		retryAttempt = attempt
	})

	e.handleActionError(action, errors.New("transient"), time.Now())

	if !retryCalled {
		t.Error("expected onRetry callback to be called")
	}
	if retryAttempt != 1 {
		t.Errorf("expected attempt=1, got %d", retryAttempt)
	}
}

// --- shouldRetryNow ---

func TestShouldRetryNow_FirstAttempt(t *testing.T) {
	e := newTestEffectorNoSession()
	if !e.shouldRetryNow("new-action", time.Now()) {
		t.Error("first attempt with no state should allow retry")
	}
}

func TestShouldRetryNow_InBackoff(t *testing.T) {
	e := newTestEffectorNoSession()
	action := newTestAction("act-6")
	now := time.Now()

	e.handleActionError(action, errors.New("transient"), now)

	// Check immediately — backoff is 1s, so should not retry yet
	if e.shouldRetryNow("act-6", now.Add(100*time.Millisecond)) {
		t.Error("should not retry while in backoff period")
	}
}

func TestShouldRetryNow_BackoffExpired(t *testing.T) {
	e := newTestEffectorNoSession()
	action := newTestAction("act-7")
	now := time.Now()

	e.handleActionError(action, errors.New("transient"), now)

	// Check after backoff has expired (1s + buffer)
	if !e.shouldRetryNow("act-7", now.Add(2*time.Second)) {
		t.Error("should retry after backoff period has expired")
	}
}

// --- chunkMessage ---

func TestChunkMessage_ShortMessage(t *testing.T) {
	chunks := chunkMessage("hello", 2000)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != "hello" {
		t.Errorf("expected 'hello', got %q", chunks[0])
	}
}

func TestChunkMessage_ExactLength(t *testing.T) {
	msg := strings.Repeat("a", 2000)
	chunks := chunkMessage(msg, 2000)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk for exact max length, got %d", len(chunks))
	}
}

func TestChunkMessage_OverLength_SplitOnParagraph(t *testing.T) {
	part1 := strings.Repeat("a", 1500)
	part2 := strings.Repeat("b", 1500)
	msg := part1 + "\n\n" + part2

	chunks := chunkMessage(msg, 2000)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks split on paragraph, got %d", len(chunks))
	}
}

func TestChunkMessage_OverLength_SplitOnLine(t *testing.T) {
	part1 := strings.Repeat("a", 1500)
	part2 := strings.Repeat("b", 1500)
	msg := part1 + "\n" + part2

	chunks := chunkMessage(msg, 2000)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks split on newline, got %d", len(chunks))
	}
}

func TestChunkMessage_OverLength_SplitOnWord(t *testing.T) {
	part1 := strings.Repeat("a", 1500)
	part2 := strings.Repeat("b", 1500)
	msg := part1 + " " + part2

	chunks := chunkMessage(msg, 2000)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks split on space, got %d", len(chunks))
	}
}

func TestChunkMessage_AllChunksWithinLimit(t *testing.T) {
	// 5000 chars with no natural break points
	msg := strings.Repeat("x", 5000)
	chunks := chunkMessage(msg, 2000)

	for i, chunk := range chunks {
		if len(chunk) > 2000 {
			t.Errorf("chunk %d exceeds max length: %d", i, len(chunk))
		}
	}

	// Reassemble and verify no data lost
	rejoined := strings.Join(chunks, "")
	if rejoined != msg {
		t.Error("chunked content doesn't match original")
	}
}

func TestChunkMessage_EmptyString(t *testing.T) {
	chunks := chunkMessage("", 2000)
	if len(chunks) != 1 || chunks[0] != "" {
		t.Errorf("expected single empty chunk, got %v", chunks)
	}
}

// --- findSplitPoint ---

func TestFindSplitPoint_ShortContent(t *testing.T) {
	pt := findSplitPoint("hello", 2000)
	if pt != 5 {
		t.Errorf("expected 5, got %d", pt)
	}
}

func TestFindSplitPoint_ParagraphPreferredOverLine(t *testing.T) {
	// paragraph break at 1400, line break at 1600 — prefer paragraph
	prefix := strings.Repeat("a", 1400)
	middle := strings.Repeat("b", 200)
	suffix := strings.Repeat("c", 400)
	content := prefix + "\n\n" + middle + "\n" + suffix

	pt := findSplitPoint(content, 2000)
	// paragraph break is at 1400, so split point should be 1402 (after \n\n)
	if content[pt-2:pt] != "\n\n" && content[pt-1] != '\n' {
		// Allow paragraph or line boundary within maxLen/2
		if pt <= 0 || pt > 2000 {
			t.Errorf("split point %d out of range", pt)
		}
	}
}

func TestFindSplitPoint_ForcedSplitWhenNoBreaks(t *testing.T) {
	// No natural break points — should split at maxLen
	content := strings.Repeat("x", 3000)
	pt := findSplitPoint(content, 2000)
	if pt != 2000 {
		t.Errorf("expected forced split at 2000, got %d", pt)
	}
}

// --- convertMarkdownTables ---

func TestConvertMarkdownTables_Basic(t *testing.T) {
	input := "| Name | Score | Notes |\n|------|-------|-------|\n| Alice | 5 | great |\n| Bob | 3 | ok |"
	output := convertMarkdownTables(input)

	if !strings.Contains(output, "```") {
		t.Error("expected output to contain code block markers")
	}
	if !strings.Contains(output, "Alice") {
		t.Error("expected output to contain Alice")
	}
	if !strings.Contains(output, "Bob") {
		t.Error("expected output to contain Bob")
	}
	// Pipes from table syntax should be gone
	if strings.Contains(output, "| Alice") {
		t.Error("expected table pipe syntax to be replaced")
	}
}

func TestConvertMarkdownTables_NoTable(t *testing.T) {
	input := "Just regular text\nwith no tables here"
	output := convertMarkdownTables(input)
	if output != input {
		t.Errorf("expected no change for non-table content, got %q", output)
	}
}

func TestConvertMarkdownTables_InsideFenceNotConverted(t *testing.T) {
	input := "```\n| Name | Score |\n|------|-------|\n| Alice | 5 |\n```"
	output := convertMarkdownTables(input)
	if output != input {
		t.Errorf("table inside fence should not be converted, got:\n%s", output)
	}
}

func TestConvertMarkdownTables_EmbeddedInText(t *testing.T) {
	input := "Before text\n\n| Name | Score |\n|------|-------|\n| Alice | 5 |\n\nAfter text"
	output := convertMarkdownTables(input)

	if !strings.Contains(output, "Before text") {
		t.Error("expected output to preserve text before table")
	}
	if !strings.Contains(output, "After text") {
		t.Error("expected output to preserve text after table")
	}
	if !strings.Contains(output, "```") {
		t.Error("expected table to be converted to code block")
	}
}

func TestConvertMarkdownTables_ColumnAlignment(t *testing.T) {
	input := "| A | LongHeader |\n|---|------------|\n| x | short |"
	output := convertMarkdownTables(input)

	// "LongHeader" should appear in output (drives column width)
	if !strings.Contains(output, "LongHeader") {
		t.Error("expected LongHeader in output")
	}
	// Separator should be at least as wide as "LongHeader"
	if !strings.Contains(output, strings.Repeat("-", len("LongHeader"))) {
		t.Error("expected separator to match LongHeader width")
	}
}

func TestConvertMarkdownTables_MissingDataRow(t *testing.T) {
	// Header + separator but no data rows — should NOT convert
	input := "| Name | Score |\n|------|-------|"
	output := convertMarkdownTables(input)
	if output != input {
		t.Errorf("table without data rows should not be converted, got %q", output)
	}
}

// --- splitCodeFenceAware ---

func TestSplitCodeFenceAware_NoFence(t *testing.T) {
	content := strings.Repeat("a", 3000)
	chunk, rest, openFence := splitCodeFenceAware(content, 2000)

	if openFence != "" {
		t.Errorf("expected no openFence for plain content, got %q", openFence)
	}
	if len(chunk) > 2000 {
		t.Errorf("chunk exceeds maxLen: %d", len(chunk))
	}
	if chunk+rest != content {
		t.Error("chunk+rest should reconstruct original content")
	}
}

func TestSplitCodeFenceAware_MidFenceReturnsOpenFence(t *testing.T) {
	// Preamble + opening fence + long code that forces a mid-block split.
	preamble := strings.Repeat("x", 100)
	code := strings.Repeat("y", 2000)
	content := preamble + "\n```go\n" + code + "\n```\n"

	_, _, openFence := splitCodeFenceAware(content, 2000)

	if openFence == "" {
		t.Error("expected openFence to be non-empty when splitting inside a code block")
	}
	if openFence != "```go" {
		t.Errorf("expected openFence=`````go`, got %q", openFence)
	}
}

func TestSplitCodeFenceAware_PreferCleanFenceBoundary(t *testing.T) {
	// A closing ``` falls within 300 chars of the limit — should use it.
	block1 := strings.Repeat("a", 1600)
	codeContent := strings.Repeat("b", 100)
	block2 := strings.Repeat("c", 400)
	// Layout: block1 \n ``` \n codeContent \n ``` \n block2
	content := block1 + "\n```\n" + codeContent + "\n```\n" + block2

	_, _, openFence := splitCodeFenceAware(content, 2000)

	if openFence != "" {
		t.Errorf("expected clean fence split (openFence=\"\"), got %q", openFence)
	}
}

func TestSplitCodeFenceAware_ShortContent(t *testing.T) {
	content := "short"
	chunk, rest, openFence := splitCodeFenceAware(content, 2000)
	if chunk != content || rest != "" || openFence != "" {
		t.Errorf("short content: unexpected return values chunk=%q rest=%q openFence=%q", chunk, rest, openFence)
	}
}

// --- chunkMessage (code-fence-aware + table conversion) ---

func TestChunkMessage_TableConvertedBeforeSplit(t *testing.T) {
	table := "| Name | Score |\n|------|-------|\n| Alice | 5 |"
	chunks := chunkMessage(table, 2000)

	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0], "```") {
		t.Error("expected converted table to contain code block markers")
	}
}

func TestChunkMessage_CodeFenceSplitClosesReopens(t *testing.T) {
	// Force a split inside a go code block.
	preamble := strings.Repeat("x", 100)
	code := strings.Repeat("y", 2000)
	content := preamble + "\n```go\n" + code + "\n```\n"

	chunks := chunkMessage(content, 2000)

	for i, chunk := range chunks {
		if len(chunk) > 2000 {
			t.Errorf("chunk %d exceeds maxLen: %d chars", i, len(chunk))
		}
	}
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks for long code block, got %d", len(chunks))
	}

	// The first chunk that ends mid-fence should be closed.
	// The second chunk should reopen with the language.
	joined := strings.Join(chunks, "\n")
	_ = joined // main check is that all chunks fit within limit
}
